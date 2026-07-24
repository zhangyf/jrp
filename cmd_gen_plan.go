package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func runGenPlan(fs *flag.FlagSet, lang string) {
	outputPath := fs.String("output", "", "Output Excel file path (default: outputs/review_<date>_vA.B.xlsx)")
	sentencesFile := fs.String("sentences", "", "JSON file with sentence exercises [{\"chinese\":\"...\",\"answer\":\"...\"}]")
	dateFlag := fs.String("date", "", "Target date for review plan (YYYY-MM-DD). Defaults to today.")
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
	if d, maj, min, perr := ParseFilename(archiveFilename); perr == nil {
		arcMajor, arcMinor = maj, min
		arcDate = d
	} else {
		// Fallback: assume v1.0 on target date
		arcMajor, arcMinor = 1, 0
		arcDate = targetDate
	}

	// Parse archive
	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// If the latest archive is from a previous day (no archive exists for the
	// target date yet), initialize today's v1.0 archive so that the review plan
	// uses v1.0 instead of inheriting the previous day's version number. A
	// subsequent `record` will then bump to v1.1 on the same day.
	if !sameDay(arcDate, targetDate) {
		arcMajor, arcMinor = 1, 0
		AddChangelogEntry(arc, targetDate, arcMajor, arcMinor, "新日初始化（gen-plan）")
		newContent := WriteArchive(arc)
		newFilename := ArchiveFilename(lang, targetDate, arcMajor, arcMinor)
		if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to initialize today's archive: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Initialized new day archive: %s\n", newFilename)
		}
	}

	// Find due words, assign categories, sort, and build the plan (reused by server)
	dateStr := targetDate.Format("2006-01-02")
	plan := BuildDuePlan(arc, lang, targetDate)

	if len(plan.Words) == 0 {
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "gen-plan",
			"date":      dateStr,
			"due_count": 0,
			"message":   "No words due for review on the target date",
		})
		return
	}

	// Load sentence exercises from file (provided by AI)
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

	// Generate Excel
	if *outputPath == "" {
		*outputPath = fmt.Sprintf("outputs/review_%s_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
	}

	// Ensure output directory exists
	if dir := filepath.Dir(*outputPath); dir != "" {
		os.MkdirAll(dir, 0755)
	}

	if err := GenerateExcel(plan, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating Excel: %v\n", err)
		os.Exit(1)
	}

	// Upload plan JSON to COS
	if err := storage.UploadPlan(ctx, plan); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload plan: %v\n", err)
	}

	// Upload Excel to COS (backup)
	if err := storage.UploadExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
	}

	outputResult(map[string]interface{}{
		"success":    true,
		"command":    "gen-plan",
		"date":       dateStr,
		"due_count":  len(plan.Words),
		"excel_path": *outputPath,
		"plan_words": plan.Words,
	})
}

// BuildDuePlan finds all words due on targetDate, assigns review categories,
// sorts by status priority (钉子户 > 待巩固 > 待测试 > 基本掌握 > 抽查), and
// returns a numbered ReviewPlan (without sentences/Excel). Reused by the CLI
// and the HTTP server.
func BuildDuePlan(arc *Archive, lang string, targetDate time.Time) *ReviewPlan {
	var dueWords []Word
	for _, w := range AllWords(arc.Groups) {
		if IsDue(w, targetDate) {
			w.Status = GetReviewCategory(w)
			dueWords = append(dueWords, w)
		}
	}

	sort.SliceStable(dueWords, func(i, j int) bool {
		return StatusPriority(dueWords[i].Status) < StatusPriority(dueWords[j].Status)
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
