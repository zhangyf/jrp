package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

func runGenPlan(fs *flag.FlagSet, lang string) {
	outputPath := fs.String("output", "", "Output Excel file path (default: outputs/review_<date>_vA.B.xlsx)")
	sentencesFile := fs.String("sentences", "", "JSON file with sentence exercises [{\"chinese\":\"...\",\"answer\":\"...\"}]")
	dateFlag := fs.String("date", "", "Target date for review plan (YYYY-MM-DD). Defaults to today.")
	sentencesOnly := fs.Bool("sentences-only", false, "Sentences-only mode: no word section at all, drill sentences only (e.g. 50 sentences)")
	fs.Parse(cmdArgs)

	// Determine target date
	var targetDate time.Time
	var err error
	if *dateFlag != "" {
		targetDate, err = time.Parse("2006-01-02", *dateFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing --date (expected YYYY-MM-DD): %v\n", err)
			os.Exit(1)
		}
	} else {
		targetDate = time.Now()
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Download latest archive
	data, archiveFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading latest archive: %v\n", err)
		os.Exit(1)
	}

	// Parse version and date from archive filename (e.g. 日语学习进度档案_260717_v1.6.md → v1.6)
	var arcMajor, arcMinor int
	var arcDate time.Time
	if d, maj, min, perr := archive.ParseFilename(archiveFilename); perr == nil {
		arcMajor, arcMinor = maj, min
		arcDate = d
	} else {
		// Fallback: assume v1.0 on target date
		arcMajor, arcMinor = 1, 0
		arcDate = targetDate
	}

	// Parse archive
	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// If the latest archive is from a previous day (no archive exists for the
	// target date yet), initialize today's v1.0 archive so that the review plan
	// uses v1.0 instead of inheriting the previous day's version number. A
	// subsequent `record` will then bump to v1.1 on the same day.
	if !archive.SameDay(arcDate, targetDate) {
		arcMajor, arcMinor = 1, 0
		archive.AddChangelogEntry(arc, targetDate, arcMajor, arcMinor, "新日初始化（gen-plan）")
		cfg := LangConfigs[lang]
		newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
		newFilename := archive.Filename(LangConfigs[lang].FilePrefix, targetDate, arcMajor, arcMinor)
		if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to initialize today's archive: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Initialized new day archive: %s\n", newFilename)
		}
	}

	// Find due words, assign categories, sort, and build the plan (reused by server)
	dateStr := targetDate.Format("2006-01-02")
	meta, _ := storage.DownloadWordMeta(ctx)
	plan := BuildDuePlan(arc, lang, targetDate, holdSet(meta))

	// Sentences-only mode: drop the entire word section. Requires --sentences.
	if *sentencesOnly {
		if *sentencesFile == "" {
			fmt.Fprintf(os.Stderr, "Error: --sentences-only requires --sentences <file.json>\n")
			os.Exit(1)
		}
		plan.Words = nil
		plan.Kind = "sentences"
	}

	if len(plan.Words) == 0 && !*sentencesOnly {
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "gen-plan",
			"date":      dateStr,
			"due_count": 0,
			"message":   "No words due for review on the target date",
		})
		return
	}

	// Load sentence exercises from file (provided by AI).
	// Defensive nil-guard: BuildDuePlan always returns a fresh ReviewPlan with
	// nil Sentences, but explicit clearing prevents any future code path from
	// accidentally carrying over stale sentences from a previous invocation.
	plan.Sentences = nil
	if *sentencesFile != "" {
		var sentences []struct {
			Chinese string `json:"chinese"`
			Answer  string `json:"answer"`
		}
		if err := readJSONFile(*sentencesFile, &sentences); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading sentences file: %v\n", err)
			os.Exit(1)
		}
		for i, s := range sentences {
			plan.Sentences = append(plan.Sentences, PlanSentence{
				Number:  i + 1,
				Chinese: s.Chinese,
				Answer:  s.Answer,
			})
		}
	}

	// 句子轮换是一个版本事件，但只应在「当天已经出过带句子的复习文件」之后
	// 再次带 --sentences 生成时才 bump。判断依据：当天 changelog 里是否已有
	// 一条含「句子」标记的记录。
	//
	// 新的一天第一次带 --sentences 时，当天档案还是「新日初始化」的 v1.0，不该
	// bump（否则每天第一个复习文件会错误地从 v1.1 起步）。此时用当前版本号并
	// 写一条含「句子」标记的 changelog，供同一天第二次带句子时识别 bump。
	if *sentencesFile != "" {
		dayStr := targetDate.Format("060102")
		rotated := false
		for _, e := range arc.Changelog {
			if e.Date == dayStr && strings.Contains(e.Description, "句子") {
				rotated = true
				break
			}
		}

		desc := fmt.Sprintf("生成复习文件（含句子%d句）", len(plan.Sentences))
		if rotated {
			arcMinor++
			desc = fmt.Sprintf("重新生成复习文件（句子轮换，共%d句）", len(plan.Sentences))
		}
		if *sentencesOnly {
			desc = fmt.Sprintf("生成纯句子复习文件（共%d句）", len(plan.Sentences))
		}

		archive.AddChangelogEntry(arc, targetDate, arcMajor, arcMinor, desc)
		cfg := LangConfigs[lang]
		newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
		newFilename := archive.Filename(LangConfigs[lang].FilePrefix, targetDate, arcMajor, arcMinor)
		if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record sentence generation: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Recorded sentence generation: %s\n", newFilename)
		}
	}

	// Generate Excel
	if *outputPath == "" {
		if *sentencesOnly {
			*outputPath = fmt.Sprintf("outputs/review_%s_sentences_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
		} else {
			*outputPath = fmt.Sprintf("outputs/review_%s_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
		}
	}

	// Ensure output directory exists
	if dir := filepath.Dir(*outputPath); dir != "" {
		os.MkdirAll(dir, 0755)
	}

	if err := GenerateExcel(plan, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating Excel: %v\n", err)
		os.Exit(1)
	}

	// Sentences are only needed locally for the Excel file; the COS plan JSON
	// is consumed by `record` to map word numbers → text and does not need
	// sentences. Stripping them prevents cross-contamination when `serve`
	// regenerates and uploads the same plan key.
	// Exception: in sentences-only mode the sentences ARE the plan, keep them.
	sentenceCount := len(plan.Sentences)
	if !*sentencesOnly {
		plan.Sentences = nil
	}

	// Upload plan JSON to COS. Sentences-only uses its own key so the daily
	// word plan (needed by `record` to resolve numbers → words) is never
	// overwritten by a word-less plan.
	if *sentencesOnly {
		if err := storage.UploadSentencePlan(ctx, plan); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to upload sentence plan: %v\n", err)
		}
	} else if err := storage.UploadPlan(ctx, plan); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload plan: %v\n", err)
	}

	// Upload Excel to COS (backup)
	if *sentencesOnly {
		if err := storage.UploadSentenceExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
		}
	} else if err := storage.UploadExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
	}

	outputResult(map[string]interface{}{
		"success":        true,
		"command":        "gen-plan",
		"date":           dateStr,
		"kind":           plan.Kind,
		"due_count":      len(plan.Words),
		"sentence_count": sentenceCount,
		"excel_path":     *outputPath,
		"plan_words":     plan.Words,
	})
}

// dueHold 读词性表拿挂起名单。读失败返回 nil —— 照样出题，
// 不因为一份展示增强用的配置读不到就把整个计划清空。
func dueHold(ctx context.Context, st *Storage) *WordMeta {
	m, err := st.DownloadWordMeta(ctx)
	if err != nil {
		return nil
	}
	return m
}

// holdSet 把词性表里的 hold 标记抽成一张「要跳过」的集合。
//
// ⚠️ meta 为 nil（读不到 word_meta.json）时返回 nil —— 拿不到挂起名单就一个都不排除，
// 宁可多出题也不能因为读不到配置而把整份计划清空。
func holdSet(meta *WordMeta) map[string]bool {
	if meta == nil || len(meta.Items) == 0 {
		return nil
	}
	out := map[string]bool{}
	for w, p := range meta.Items {
		if p.Hold {
			out[w] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// BuildDuePlan finds all words due on targetDate, assigns review categories,
// sorts by status priority (钉子户 > 待巩固 > 待测试 > 基本掌握 > 抽查), and
// returns a numbered ReviewPlan (without sentences/Excel). Reused by the CLI
// and the HTTP server.
//
// hold 里点名的词直接跳过（还没学到的课，2026-11-02 手册补录那批）。传 nil 表示不排除。
func BuildDuePlan(arc *Archive, lang string, targetDate time.Time, hold map[string]bool) *ReviewPlan {
	var dueWords []Word
	for _, w := range review.AllWords(arc.Groups) {
		if hold[w.Word] {
			continue
		}
		if review.IsDue(w, targetDate) {
			w.Status = review.GetReviewCategory(w)
			dueWords = append(dueWords, w)
		}
	}

	sort.SliceStable(dueWords, func(i, j int) bool {
		return review.StatusPriority(dueWords[i].Status) < review.StatusPriority(dueWords[j].Status)
	})

	plan := &ReviewPlan{
		Date:     targetDate.Format("2006-01-02"),
		Language: lang,
	}
	for i, w := range dueWords {
		plan.Words = append(plan.Words, PlanWord{
			Number:     i + 1,
			Word:       w.Word,
			Definition: w.Definition,
			Group:      w.Group,
			Status:     w.Status,
		})
	}
	return plan
}
