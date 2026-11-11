// cmd_sentence.go —— 句库维护 / 回炉变形类命令
//
// 腿一 R1：把原本平铺在根目录的 2 个 cmd_*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；命令行为、输出、存档字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找命令实现先看下面的来源清单。
//
// 来源与顶层声明：
// cmd_sentence.go              runSentenceMigrate, runSentenceLint, runSentenceBank, runSentencePreview, latestOrEmpty, firstOrEmpty
// cmd_sentence_review.go       runSentenceReviewTodo, runSentenceReviewStats, runSentenceReviewBank, checkReviewSentences, reportReviewCheck
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/words"
)

// ==========================================================================
// 原 cmd_sentence.go
// ==========================================================================

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
		docs[name] = sentence.Norm(string(data))
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
		if !strings.Contains(doc, sentence.Norm(s.Answer)) {
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
		var bank sentence.SentenceBank
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
		sentence.SortLessons(lessons)
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

	res, err := plan.ComposeSentences(ctx, storage, sentenceMeta{storage}, plan.Params{
		Today:     today,
		Arc:       nil, // 预检拿不到档案，不按闲置过滤，宁可全列出来看过全貌
		BaseCount: *count,
	})
	if err != nil {
		var le *plan.LoadError
		if errors.As(err, &le) && le.Stage == plan.StageBank {
			fmt.Fprintf(os.Stderr, "Error: 读取句库失败：%v\n", le.Err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
	picked := res.Sentences
	baseCount := res.BaseCount
	reviewCount := res.ReviewCount
	hist := res.History
	wrong := res.Wrong
	bank := res.Bank

	// 回炉句挑中了几句三圆 —— 圆级加权有没有生效，看这里。
	// sentence.BankSentence 不带锚点词，只能拿 answer 反查回炉库。
	circleStat := map[string]int{}
	if res.ReviewBank != nil {
		wordOf := make(map[string]string, len(res.ReviewBank.Sentences))
		for _, s := range res.ReviewBank.Sentences {
			wordOf[sentence.Norm(s.Answer)] = s.Word
		}
		rankName := []string{"三圆", "二圆", "一圆", "没标"}
		for _, e := range res.ReviewExtra {
			circleStat[rankName[sentence.CircleRank(res.Circle, wordOf[sentence.Norm(e.Answer)])]]++
		}
	}

	// 自检：近 7 天重复
	cutoff := today.AddDate(0, 0, -7)
	recent := map[string]bool{}
	for d, ss := range hist {
		dt, err := time.Parse("2006-01-02", d)
		if err != nil || dt.Before(cutoff) {
			continue
		}
		for _, s := range ss {
			recent[sentence.Norm(s)] = true
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
			openDue[sentence.Norm(w.Answer)] = true
		}
	}
	for _, p := range picked {
		n := sentence.Norm(p.Answer)
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
		n := sentence.Norm(p.Answer)
		if inner[n] {
			innerDup++
		}
		inner[n] = true
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "sentence-preview",
		"language":     lang,
		"date":         today.Format("2006-01-02"),
		"note":         "只读干跑，未写入任何数据",
		"pool_size":    len(bank.Sentences),
		"base_count":   baseCount,
		"review_added": reviewCount,
		"review_circle": map[string]interface{}{
			"三圆": circleStat["三圆"], "二圆": circleStat["二圆"],
			"一圆": circleStat["一圆"], "没标": circleStat["没标"],
			"ratio": res.CircleRatio,
		},
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

// ==========================================================================
// 原 cmd_sentence_review.go
// ==========================================================================

// runSentenceReviewTodo 只读体检：列出「够格回炉、但还没做变形句」的词，
// 并给每个词推荐几条能套的原型句骨架。
//
// 为什么需要它：回炉句库是**静态**的 —— Go 只负责挑句，不造词（没有分词器，
// 判断不出句子里哪个位置能换什么词），句库由 AI 生成后上传。所以档案里新进
// 「正确率≥80% 且闲置≥30天」状态的词不会自动变成变形句，得定期补。
// 这个命令把「谁还没做」和「能套哪句」一次列全，补一批只要几分钟。
//
//	jrp --lang ja sentence-review-todo
//	jrp --lang ja sentence-review-todo --limit 0           # 列出全部
//	jrp --lang ja sentence-review-todo --pending           # 连"在路上"的一起看
func runSentenceReviewTodo(fs *flag.FlagSet, lang string) {
	limit := fs.Int("limit", 50, "最多列出多少个待补词（0 = 全部）")
	nproto := fs.Int("protos", 3, "每个词推荐几条原型句骨架")
	withPending := fs.Bool("pending", false, "连「在路上」（正确率够但还没闲置满）的词一起列")
	withCovered := fs.Bool("all", false, "连「已有变形句」的合格词一起列（整体重建回炉库时用）")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	rb, err := storage.DownloadReviewBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
		os.Exit(1)
	}
	covered := map[string]bool{}
	for _, s := range rb.Sentences {
		if s.Word != "" {
			covered[s.Word] = true
		}
	}

	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取句库失败：%v\n", err)
		os.Exit(1)
	}
	byLesson := sentence.GroupProtosByLesson(bank.Sentences)

	data, _, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取档案失败：%v\n", err)
		os.Exit(1)
	}
	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 解析档案失败：%v\n", err)
		os.Exit(1)
	}
	meta, _ := storage.DownloadWordMeta(ctx) // 读不到就留空，不致命

	today := time.Now()
	type item struct {
		Word     string                  `json:"word"`
		Pos      string                  `json:"pos"`
		Def      string                  `json:"def"`
		Lesson   string                  `json:"lesson"`
		Reviews  int                     `json:"reviews"`
		Accuracy float64                 `json:"accuracy"`
		IdleDays int                     `json:"idle_days"`
		DueIn    int                     `json:"days_to_qualify,omitempty"` // 仅"在路上"的词有
		Protos   []sentence.ProtoSuggest `json:"prototypes,omitempty"`
	}
	var qualified, pending []item
	var young, weak, qualifiedTotal, coveredCount int

	seen := map[string]bool{}
	for _, g := range arc.Groups {
		for _, w := range g.Words {
			if w.Word == "" || seen[w.Word] {
				continue
			}
			seen[w.Word] = true
			acc := 0.0
			if w.ReviewCount > 0 {
				acc = float64(w.ReviewCount-w.ErrorCount) / float64(w.ReviewCount)
			}
			idle := sentence.WordIdleDays(w, today)
			pos, _, _, _ := words.LookupWordMeta(meta, w.Word)
			ln := sentence.GroupLesson(w.Group)
			it := item{
				Word: w.Word, Pos: pos, Def: w.Definition,
				Lesson: sentence.LessonLabel(ln), Reviews: w.ReviewCount,
				Accuracy: sentence.RoundAcc(acc), IdleDays: idle,
			}
			switch {
			case w.ReviewCount < 2:
				young++
			case acc < 0.8:
				weak++
			case idle >= sentence.DefaultReviewIdleDays:
				qualifiedTotal++
				if covered[w.Word] {
					coveredCount++
					if !*withCovered {
						break
					}
				}
				it.Protos = sentence.ProtoList(sentence.PickPrototypes(byLesson, ln, *nproto, pos))
				qualified = append(qualified, it)
			default:
				it.DueIn = sentence.DefaultReviewIdleDays - idle
				pending = append(pending, it)
			}
		}
	}

	// 闲置越久越该先补
	sort.SliceStable(qualified, func(i, j int) bool { return qualified[i].IdleDays > qualified[j].IdleDays })
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].DueIn < pending[j].DueIn })

	missing := len(qualified)
	if *limit > 0 && len(qualified) > *limit {
		qualified = qualified[:*limit]
	}

	out := map[string]interface{}{
		"success":  true,
		"command":  "sentence-review-todo",
		"language": lang,
		"note":     "只读体检，未写入任何数据",
		"criteria": map[string]interface{}{
			"min_reviews":   2,
			"min_accuracy":  0.8,
			"min_idle_days": sentence.DefaultReviewIdleDays,
		},
		"summary": map[string]interface{}{
			"archive_words": len(seen),
			"qualified":     qualifiedTotal,
			"covered":       coveredCount,
			"missing":       missing,
			"listed":        len(qualified),
			"pending":       len(pending),
			"young_lt2":     young,
			"weak_lt80":     weak,
			"review_bank":   len(rb.Sentences),
		},
		"items": qualified,
	}
	if *withPending {
		out["pending_items"] = pending
		buckets := map[string]int{}
		for _, p := range pending {
			switch {
			case p.DueIn <= 7:
				buckets["7天内"]++
			case p.DueIn <= 14:
				buckets["8-14天"]++
			case p.DueIn <= 21:
				buckets["15-21天"]++
			default:
				buckets["22-29天"]++
			}
		}
		out["pending_due_in"] = buckets
	}
	if missing == 0 {
		out["hint"] = "全部合格词都已经有变形句了，不用补。"
	} else {
		out["hint"] = "按 prototypes 里的原型句换一个词（助词序列别动），造好后用 sentence-bank 之外的回炉库上传入口写回。"
	}
	outputResult(out)
}

// runSentenceReviewStats —— 只读体检：回炉变形句库「覆盖到哪儿了、难不难」。
//
// 三个问题一次答完：
//
//  1. 覆盖多少词：回炉库里有多少条、锚点词去重后多少个（跟 sentence-review-todo
//     的 qualified / covered 对得上）。
//
//  2. 覆盖多少课：变形句的 lesson 分布，旁边给主句库同课的句数做对照 ——
//     某课主句库有 30 句、变形句只有 2 句，说明那课的旧词基本还没开始回炉。
//
//  3. 难不难：变形句是「原型句换一个词」来的，长度跟原型句差不多，
//     所以句长（去空格字符数）就是现成的难度代理。跟主句库同课的平均句长
//     放一起比，就能看出变形句是不是集中在短句/低课次（= 偏简单）。
//
//     jrp --lang ja sentence-review-stats
func runSentenceReviewStats(fs *flag.FlagSet, lang string) {
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	rb, err := storage.DownloadReviewBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
		os.Exit(1)
	}
	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取句库失败：%v\n", err)
		os.Exit(1)
	}

	// 主句库按课：句数 + 平均句长，用来跟变形句对照
	type lessonStat struct {
		Count int
		Len   int
	}
	bankBy := map[int]*lessonStat{}
	bankTotal, bankLen := 0, 0
	for _, s := range bank.Sentences {
		if s.Answer == "" {
			continue
		}
		n := sentence.GroupLesson(s.Lesson)
		if bankBy[n] == nil {
			bankBy[n] = &lessonStat{}
		}
		l := sentence.JpLen(s.Answer)
		bankBy[n].Count++
		bankBy[n].Len += l
		bankTotal++
		bankLen += l
	}

	// 回炉库按课：句数 + 平均句长 + 覆盖词数
	revBy := map[int]*lessonStat{}
	words := map[int]map[string]bool{}
	revTotal, revLen := 0, 0
	seenWord := map[string]bool{}
	bySource := map[string]int{}
	for _, s := range rb.Sentences {
		if s.Answer == "" {
			continue
		}
		n := sentence.GroupLesson(s.Lesson)
		if revBy[n] == nil {
			revBy[n] = &lessonStat{}
			words[n] = map[string]bool{}
		}
		l := sentence.JpLen(s.Answer)
		revBy[n].Count++
		revBy[n].Len += l
		if s.Word != "" {
			words[n][s.Word] = true
			seenWord[s.Word] = true
		}
		if s.Source != "" {
			bySource[s.Source]++
		}
		revTotal++
		revLen += l
	}

	// 课号并集，升序；课号 0（解析不出来）放最后标「未标课」
	lessons := []int{}
	seen := map[int]bool{}
	for n := range bankBy {
		if !seen[n] {
			seen[n] = true
			lessons = append(lessons, n)
		}
	}
	for n := range revBy {
		if !seen[n] {
			seen[n] = true
			lessons = append(lessons, n)
		}
	}
	sort.Slice(lessons, func(i, j int) bool {
		if lessons[i] == 0 {
			return false
		}
		if lessons[j] == 0 {
			return true
		}
		return lessons[i] < lessons[j]
	})

	rows := make([]map[string]interface{}, 0, len(lessons))
	for _, n := range lessons {
		name := sentence.LessonLabel(n)
		if n == 0 {
			name = "未标课"
		}
		b, r := bankBy[n], revBy[n]
		row := map[string]interface{}{
			"lesson": name, "review": 0, "review_words": 0,
			"review_avg_len": 0, "bank": 0, "bank_avg_len": 0,
		}
		if r != nil && r.Count > 0 {
			row["review"] = r.Count
			row["review_words"] = len(words[n])
			row["review_avg_len"] = sentence.RoundAcc(float64(r.Len) / float64(r.Count))
		}
		if b != nil && b.Count > 0 {
			row["bank"] = b.Count
			row["bank_avg_len"] = sentence.RoundAcc(float64(b.Len) / float64(b.Count))
		}
		rows = append(rows, row)
	}

	avg := func(total, n int) float64 {
		if n == 0 {
			return 0
		}
		return sentence.RoundAcc(float64(total) / float64(n))
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"command":  "sentence-review-stats",
		"language": lang,
		"note":     "只读体检，未写入任何数据。句长=去空格后的字符数，作为难度代理（越长一般越难）。",
		"summary": map[string]interface{}{
			"review_sentences": revTotal,
			"review_words":     len(seenWord),
			"review_avg_len":   avg(revLen, revTotal),
			"bank_sentences":   bankTotal,
			"bank_avg_len":     avg(bankLen, bankTotal),
			"by_source":        bySource,
		},
		"by_lesson": rows,
	})
}

// ---------- 回炉句库读写（2026-11-03 补） ----------

// runSentenceReviewBank 回炉变形句库的上传/下载入口。
//
// 为什么以前没有它：句库由 AI 造（Go 里没分词器，判断不出句子里哪个位置
// 能换什么词），造完只能靠 COS 直传写回 —— 没校验、没去重、补一批得整库覆盖。
// 现在补批走 --merge：按锚点词去重只追加新的，已有的句子一条不动。
//
//	jrp --lang ja sentence-review-bank                    # 概览
//	jrp --lang ja sentence-review-bank --dump             # 下载整库打印到 stdout
//	jrp --lang ja sentence-review-bank --merge new.json   # 补批（推荐，只追加）
//	jrp --lang ja sentence-review-bank --file all.json    # 全量覆盖（先 --dump 下来改）
func runSentenceReviewBank(fs *flag.FlagSet, lang string) {
	file := fs.String("file", "", "本地 JSON 路径，上传**覆盖**整库")
	merge := fs.String("merge", "", "本地 JSON 路径，只**追加**（按锚点词去重，已存在则跳过）")
	dump := fs.Bool("dump", false, "下载 COS 回炉库并打印到 stdout")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	switch {
	case *dump:
		rb, err := storage.DownloadReviewBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(rb)
		return

	case *file != "":
		nb, err := sentence.LoadReviewFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取本地文件失败：%v\n", err)
			os.Exit(1)
		}
		fatal, warn := checkReviewSentences(ctx, storage, nb.Sentences, lang)
		reportReviewCheck(fatal, warn)
		if len(fatal) > 0 {
			fmt.Fprintln(os.Stderr, "Error: 校验没过，没有上传（改完再来）")
			os.Exit(1)
		}
		if nb.Updated == "" {
			nb.Updated = time.Now().Format("2006-01-02")
		}
		if nb.Version == 0 {
			nb.Version = 1
		}
		if err := storage.UploadReviewBank(ctx, nb); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-review-bank",
			"mode":      "file",
			"uploaded":  len(nb.Sentences),
			"bank_key":  storage.reviewBankKey(),
			"next_step": "跑 sentence-review-todo 看 missing 是否归零，再 sentence-preview 看今天会出哪些",
		})
		return

	case *merge != "":
		cur, err := storage.DownloadReviewBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		add, err := sentence.LoadReviewFile(*merge)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取本地文件失败：%v\n", err)
			os.Exit(1)
		}
		haveWord := map[string]bool{}
		haveAns := map[string]bool{}
		haveID := map[string]bool{}
		for _, s := range cur.Sentences {
			if s.Word != "" {
				haveWord[s.Word] = true
			}
			haveAns[sentence.Norm(s.Answer)] = true
			haveID[s.ID] = true
		}
		fatal, warn := checkReviewSentences(ctx, storage, add.Sentences, lang)
		reportReviewCheck(fatal, warn)
		if len(fatal) > 0 {
			fmt.Fprintln(os.Stderr, "Error: 校验没过，没有上传（改完再来）")
			os.Exit(1)
		}
		nextN := sentence.NextReviewIDNum(cur.Sentences)
		var added []ReviewSentence
		var skipped []string
		for _, s := range add.Sentences {
			switch {
			case s.Word != "" && haveWord[s.Word]:
				skipped = append(skipped, s.Word+"（已有变形句）")
				continue
			case haveAns[sentence.Norm(s.Answer)]:
				skipped = append(skipped, s.Answer+"（句子已存在）")
				continue
			}
			if s.ID == "" || haveID[s.ID] {
				for {
					nextN++
					id := fmt.Sprintf("R%03d", nextN)
					if !haveID[id] {
						s.ID = id
						break
					}
				}
			}
			if s.Type == "" {
				s.Type = "transformed"
			}
			haveID[s.ID] = true
			haveAns[sentence.Norm(s.Answer)] = true
			if s.Word != "" {
				haveWord[s.Word] = true
			}
			added = append(added, s)
		}
		if len(added) == 0 {
			outputResult(map[string]interface{}{
				"success": true, "command": "sentence-review-bank", "mode": "merge",
				"added": 0, "skipped": skipped,
				"hint": "没有新句子要加（可能这批词都已经有变形句了）",
			})
			return
		}
		cur.Sentences = append(cur.Sentences, added...)
		cur.Updated = time.Now().Format("2006-01-02")
		if cur.Version == 0 {
			cur.Version = 1
		}
		if err := storage.UploadReviewBank(ctx, cur); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-review-bank",
			"mode":      "merge",
			"added":     len(added),
			"total":     len(cur.Sentences),
			"skipped":   skipped,
			"bank_key":  storage.reviewBankKey(),
			"next_step": "跑 sentence-review-todo 看 missing 是否下降，再 sentence-preview",
		})
		return

	default:
		rb, err := storage.DownloadReviewBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		byLesson := map[string]int{}
		words := map[string]bool{}
		bySource := map[string]int{}
		for _, s := range rb.Sentences {
			byLesson[s.Lesson]++
			bySource[s.Source]++
			if s.Word != "" {
				words[s.Word] = true
			}
		}
		lessons := make([]string, 0, len(byLesson))
		for k := range byLesson {
			lessons = append(lessons, k)
		}
		sentence.SortLessons(lessons)
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-review-bank",
			"version":   rb.Version,
			"updated":   rb.Updated,
			"total":     len(rb.Sentences),
			"words":     len(words),
			"by_lesson": byLesson,
			"by_source": bySource,
			"bank_key":  storage.reviewBankKey(),
		})
	}
}

// checkReviewSentences 上传前的体检，返回致命问题和提醒。
//
// 致命（阻断上传）：锚点词不在档案里（词形写错，挑句时查不到就会永远轮不到）、
// 原型句不在主句库里（变形句必须是课本原句的改造）、answer 空/跟课本原文撞车、
// id 重复。
// 提醒（只打印）：变形句跟原型句的空格分段数不一致 —— 变形规则是「只换一个词、
// 助词序列不动」，段数对不上多半是助词被改了，人看一眼再决定。
//
// 档案/主句库读不了时跳过对应那项（fail-open）：补批是人工活，
// 网络抖一下不该把整批卡住，但校验项会在输出里标注"未校验"。
func checkReviewSentences(ctx context.Context, st *Storage, ss []ReviewSentence, lang string) (fatal []string, warn []string) {
	bank, errBank := st.DownloadSentenceBank(ctx)
	protoBy := map[string]sentence.BankSentence{}
	bankAns := map[string]bool{}
	if errBank == nil && bank != nil {
		for _, s := range bank.Sentences {
			if s.Answer == "" {
				continue
			}
			protoBy[sentence.Norm(s.Answer)] = s
			bankAns[sentence.Norm(s.Answer)] = true
		}
	}
	arcWords := map[string]bool{}
	if data, _, err := st.DownloadLatestArchive(ctx); err == nil {
		if arc, err := archive.ParseArchive(string(data), lang); err == nil {
			for _, g := range arc.Groups {
				for _, w := range g.Words {
					if w.Word != "" {
						arcWords[w.Word] = true
					}
				}
			}
		}
	}

	seenID := map[string]bool{}
	seenAns := map[string]bool{}
	for i := range ss {
		s := &ss[i]
		tag := s.ID
		if tag == "" {
			tag = s.Word
		}
		if s.Word == "" {
			fatal = append(fatal, fmt.Sprintf("第 %d 条：缺 word（锚点词）", i+1))
		}
		if s.Answer == "" {
			fatal = append(fatal, fmt.Sprintf("%s：缺 answer", tag))
			continue
		}
		if s.Word != "" && len(arcWords) > 0 && !arcWords[s.Word] {
			fatal = append(fatal, fmt.Sprintf("%s：锚点词 %q 不在档案里（词形写错了？）", tag, s.Word))
		}
		if s.Prototype == "" {
			fatal = append(fatal, fmt.Sprintf("%s：缺 prototype（原型句）", tag))
		} else if len(protoBy) > 0 {
			if p, ok := protoBy[sentence.Norm(s.Prototype)]; !ok {
				fatal = append(fatal, fmt.Sprintf("%s：原型句不在主句库里 → %s", tag, s.Prototype))
			} else {
				if s.PrototypeID == "" {
					s.PrototypeID = p.ID
				}
				if s.Source == "" {
					s.Source = p.Source
				}
				if s.SourceDoc == "" {
					s.SourceDoc = p.SourceDoc
				}
				if len(strings.Fields(s.Prototype)) != len(strings.Fields(s.Answer)) {
					warn = append(warn, fmt.Sprintf("%s：分段数跟原型句不同（%d vs %d）—— 助词序列可能被改动了：%s",
						tag, len(strings.Fields(s.Prototype)), len(strings.Fields(s.Answer)), s.Answer))
				}
			}
		}
		if bankAns[sentence.Norm(s.Answer)] {
			fatal = append(fatal, fmt.Sprintf("%s：变形句跟课本原文一模一样 → %s", tag, s.Answer))
		}
		if seenAns[sentence.Norm(s.Answer)] {
			fatal = append(fatal, fmt.Sprintf("%s：这一批里句子重复 → %s", tag, s.Answer))
		}
		seenAns[sentence.Norm(s.Answer)] = true
		if s.ID != "" {
			if seenID[s.ID] {
				fatal = append(fatal, fmt.Sprintf("id 重复：%s", s.ID))
			}
			seenID[s.ID] = true
		}
	}
	if errBank != nil {
		warn = append(warn, "主句库读不了，原型句/撞车这两项没校验")
	}
	if len(arcWords) == 0 {
		warn = append(warn, "档案读不了，锚点词这项没校验")
	}
	return fatal, warn
}

func reportReviewCheck(fatal, warn []string) {
	if len(warn) > 0 {
		fmt.Fprintln(os.Stderr, "提醒：")
		for _, w := range warn {
			fmt.Fprintf(os.Stderr, "  - %s\n", w)
		}
	}
	if len(fatal) > 0 {
		fmt.Fprintln(os.Stderr, "致命：")
		for _, f := range fatal {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
	}
}
