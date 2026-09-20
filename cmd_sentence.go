package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// 造句相关的维护命令。
//
//	sentence-migrate  只读预检：看现有 sentence_history / sentence_wrong 的结构与条数
//	sentence-lint     只读校验：句库里每条 answer 是否真的是课本原文
//	sentence-bank     句库管理：--file 上传、--dump 下载打印、--stats 统计

func runSentenceMigrate(fs *flag.FlagSet, lang string) {
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	hist, err := storage.DownloadSentenceHistory(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取 sentence_history 失败：%v\n", err)
		os.Exit(1)
	}
	wrong, err := storage.DownloadSentenceWrong(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取 sentence_wrong 失败：%v\n", err)
		os.Exit(1)
	}

	var dates []string
	totalAsked := 0
	for d, ss := range hist {
		dates = append(dates, d)
		totalAsked += len(ss)
	}
	sort.Strings(dates)

	open, archived := 0, 0
	for _, w := range wrong.Sentences {
		switch w.Status {
		case "open":
			open++
		default:
			archived++
		}
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"command":  "sentence-migrate",
		"language": lang,
		"note":     "只读预检，未写入任何数据",
		"history": map[string]interface{}{
			"days":        len(dates),
			"total_asked": totalAsked,
			"latest_date": latestOrEmpty(dates),
			"oldest_date": firstOrEmpty(dates),
		},
		"wrong": map[string]interface{}{
			"total":    len(wrong.Sentences),
			"open":     open,
			"archived": archived,
		},
	})
}

func runSentenceLint(fs *flag.FlagSet, lang string) {
	verbose := fs.Bool("verbose", false, "列出每条问题的句子")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取句库失败（还没建库？）：%v\n", err)
		os.Exit(1)
	}

	// 按 source_doc 分组，每个文档只下载一次。
	docs := map[string]string{}
	for _, s := range bank.Sentences {
		if s.SourceDoc != "" {
			docs[s.SourceDoc] = ""
		}
	}
	for name := range docs {
		data, err := storage.DownloadKnowledge(ctx, name)
		if err != nil {
			docs[name] = "" // 下载失败 → 该文档下的句子都报"查不到出处"
			continue
		}
		docs[name] = normSentence(string(data))
	}

	var problems []map[string]string
	checked := 0
	for _, s := range bank.Sentences {
		if s.Type == "transformed" {
			// 变形句是合法改造，只校验它能指回原型
			if s.Prototype == "" {
				problems = append(problems, map[string]string{
					"id": s.ID, "issue": "变形句未填 prototype",
				})
			}
			continue
		}
		if s.SourceDoc == "" {
			problems = append(problems, map[string]string{
				"id": s.ID, "answer": s.Answer, "issue": "未填 source_doc，无法校验是否原文",
			})
			continue
		}
		checked++
		doc := docs[s.SourceDoc]
		if doc == "" {
			problems = append(problems, map[string]string{
				"id": s.ID, "answer": s.Answer, "issue": "出处文档下载失败：" + s.SourceDoc,
			})
			continue
		}
		if !strings.Contains(doc, normSentence(s.Answer)) {
			problems = append(problems, map[string]string{
				"id": s.ID, "answer": s.Answer, "issue": "在出处文档里找不到，可能是 AI 编的",
			})
		}
	}

	out := map[string]interface{}{
		"success":       true,
		"command":       "sentence-lint",
		"language":      lang,
		"bank_version":  bank.Version,
		"bank_updated":  bank.Updated,
		"total":         len(bank.Sentences),
		"checked":       checked,
		"problem_count": len(problems),
	}
	if *verbose || len(problems) > 0 {
		out["problems"] = problems
	}
	outputResult(out)

	if len(problems) > 0 {
		os.Exit(2)
	}
}

func runSentenceBank(fs *flag.FlagSet, lang string) {
	file := fs.String("file", "", "本地句库 JSON 路径，上传覆盖 COS 上的句库")
	dump := fs.Bool("dump", false, "下载 COS 句库并打印到 stdout")
	stats := fs.Bool("stats", false, "只打印句库统计")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	switch {
	case *file != "":
		var bank SentenceBank
		if err := readJSONFile(*file, &bank); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取本地句库失败：%v\n", err)
			os.Exit(1)
		}
		if bank.Updated == "" {
			bank.Updated = time.Now().Format("2006-01-02")
		}
		if err := storage.UploadSentenceBank(ctx, &bank); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-bank",
			"uploaded":  len(bank.Sentences),
			"bank_key":  storage.sentenceBankKey(),
			"next_step": "跑 jrp --lang ja sentence-lint 校验是否都是课本原文",
			"bank":      bank,
		})

	case *dump:
		bank, err := storage.DownloadSentenceBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		outputResult(bank)

	default:
		bank, err := storage.DownloadSentenceBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取句库失败（还没建库？）：%v\n", err)
			os.Exit(1)
		}
		byLesson := map[string]int{}
		byType := map[string]int{}
		for _, s := range bank.Sentences {
			byLesson[s.Lesson]++
			t := s.Type
			if t == "" {
				t = "original"
			}
			byType[t]++
		}
		lessons := make([]string, 0, len(byLesson))
		for k := range byLesson {
			lessons = append(lessons, k)
		}
		sortLessons(lessons)
		outputResult(map[string]interface{}{
			"success":        true,
			"command":        "sentence-bank",
			"version":        bank.Version,
			"updated":        bank.Updated,
			"total":          len(bank.Sentences),
			"by_lesson":      byLesson,
			"lessons_sorted": lessons,
			"by_type":        byType,
			"bank_key":       storage.sentenceBankKey(),
		})
		_ = stats
	}
}

// runSentencePreview 只读干跑：算今天会出哪些造句，不写任何数据。
//
// 用来在真正出题前自检轮换是否正常（近 7 天有没有重复、跨课有没有摊平、
// 错句有没有被强制插入）。
func runSentencePreview(fs *flag.FlagSet, lang string) {
	count := fs.Int("count", 20, "出题句数（普通 20 / 纯句子 50）")
	date := fs.String("date", "", "YYYY-MM-DD，默认今天")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	today := time.Now()
	if *date != "" {
		today, err = time.Parse("2006-01-02", *date)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: --date 格式应为 YYYY-MM-DD\n")
			os.Exit(1)
		}
	}

	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取句库失败：%v\n", err)
		os.Exit(1)
	}
	hist, err := storage.DownloadSentenceHistory(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	wrong, err := storage.DownloadSentenceWrong(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	picked := BuildSentencePlan(bank, hist, wrong, today, *count)

	// 自检：近 7 天重复
	cutoff := today.AddDate(0, 0, -7)
	recent := map[string]bool{}
	for d, ss := range hist {
		dt, err := time.Parse("2006-01-02", d)
		if err != nil || dt.Before(cutoff) {
			continue
		}
		for _, s := range ss {
			recent[normSentence(s)] = true
		}
	}
	recentDup := 0
	forced := 0
	openDue := map[string]bool{}
	for _, w := range wrong.Sentences {
		if w.Status != "open" {
			continue
		}
		d, err := time.Parse("2006-01-02", w.NextDue)
		if err != nil || !today.Before(d) {
			openDue[normSentence(w.Answer)] = true
		}
	}
	for _, p := range picked {
		n := normSentence(p.Answer)
		if recent[n] && !openDue[n] {
			recentDup++
		}
		if openDue[n] {
			forced++
		}
	}

	// 当天内部重复
	inner := map[string]bool{}
	innerDup := 0
	for _, p := range picked {
		n := normSentence(p.Answer)
		if inner[n] {
			innerDup++
		}
		inner[n] = true
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "sentence-preview",
		"language":  lang,
		"date":      today.Format("2006-01-02"),
		"note":      "只读干跑，未写入任何数据",
		"pool_size": len(bank.Sentences),
		"self_check": map[string]interface{}{
			"recent_7d_repeat": recentDup, // 应为 0（错句除外）
			"inner_repeat":     innerDup,  // 必须为 0
			"forced_wrong":     forced,    // 今天该重出的错句数
			"open_wrong_total": len(openDue),
		},
		"count":     len(picked),
		"sentences": picked,
	})
}

func latestOrEmpty(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[len(ss)-1]
}

func firstOrEmpty(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
