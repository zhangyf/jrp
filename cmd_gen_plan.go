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
	outputPath := fs.String("output", "", "Output Excel file path (default: /tmp/review_<date>.xlsx)")
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
	data, _, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading latest archive: %v\n", err)
		os.Exit(1)
	}

	// Parse archive
	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// Find due words and assign review categories
	var dueWords []Word
	for _, w := range AllWords(arc.Groups) {
		if IsDue(w, targetDate) {
			w.Status = GetReviewCategory(w)
			dueWords = append(dueWords, w)
		}
	}

	if len(dueWords) == 0 {
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "gen-plan",
			"date":      targetDate.Format("2006-01-02"),
			"due_count": 0,
			"message":   "No words due for review on the target date",
		})
		return
	}

	// Sort by status priority: 钉子户 > 待巩固 > 待测试 > 基本掌握 > 抽查
	sort.SliceStable(dueWords, func(i, j int) bool {
		return StatusPriority(dueWords[i].Status) < StatusPriority(dueWords[j].Status)
	})

	// Build plan
	dateStr := targetDate.Format("2006-01-02")
	plan := &ReviewPlan{
		Date:     dateStr,
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
		*outputPath = fmt.Sprintf("/tmp/review_%s.xlsx", dateStr)
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
	if err := storage.UploadExcel(ctx, dateStr, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
	}

	outputResult(map[string]interface{}{
		"success":    true,
		"command":    "gen-plan",
		"date":       dateStr,
		"due_count":  len(dueWords),
		"excel_path": *outputPath,
		"plan_words": plan.Words,
	})
}
