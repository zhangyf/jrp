package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/review"
)

// 钉子户分区常量。值与分区顺序的唯一来源在 internal/offline
// （渲染侧同包持有 hardCategories），根包只留别名给 runExportHard
// 的分区计数 switch 用，避免两份字面量各写一遍。
const (
	HardSevere   = offline.HardSevere
	HardModerate = offline.HardModerate
	HardMild     = offline.HardMild
)

// BuildHardPlan / hardSeverity 已迁 internal/plan（P2），
// CLI export-hard 与 HTTP /api/hard、/api/export 共用同一份实现。

func runExportHard(fs *flag.FlagSet, lang string) {
	outputPath := fs.String("output", "", "Output Excel file path (default: outputs/hard_words_<date>_vA.B.xlsx)")
	minAcc := fs.Float64("min-accuracy", review.DefaultHardMinAccuracy, "Accuracy threshold (0-1); words below this are hard words")
	minRev := fs.Int("min-reviews", review.DefaultHardMinReviews, "Minimum review count required to qualify as a hard word")
	dateFlag := fs.String("date", "", "Target date used in the output filename (YYYY-MM-DD). Defaults to today.")
	fs.Parse(cmdArgs)

	if *minAcc <= 0 || *minAcc >= 1 {
		fmt.Fprintf(os.Stderr, "Error: --min-accuracy must be between 0 and 1 (exclusive), got %v\n", *minAcc)
		os.Exit(1)
	}
	if *minRev < 1 {
		fmt.Fprintf(os.Stderr, "Error: --min-reviews must be >= 1, got %d\n", *minRev)
		os.Exit(1)
	}

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

	// Parse version from the archive filename for output naming.
	//
	// Unlike gen-plan, this command does NOT initialize a new-day archive: it is
	// a read-only query, and merely looking at the hard-word list should not bump
	// the archive version. As a result the version here may still refer to the
	// previous day's archive, so archive_filename is reported for traceability.
	arcMajor, arcMinor := 1, 0
	if _, maj, min, perr := archive.ParseFilename(archiveFilename); perr == nil {
		arcMajor, arcMinor = maj, min
	}

	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	dateStr := targetDate.Format("2006-01-02")
	plan := plan.BuildHardPlan(arc, lang, targetDate, *minAcc, *minRev)

	if len(plan.Words) == 0 {
		outputResult(map[string]interface{}{
			"success":          true,
			"command":          "export-hard",
			"date":             dateStr,
			"language":         lang,
			"min_accuracy":     *minAcc,
			"min_reviews":      *minRev,
			"hard_count":       0,
			"archive_filename": archiveFilename,
			"message":          "No hard words found under the given thresholds",
		})
		return
	}

	// Count severity buckets
	severe, moderate, mild := 0, 0, 0
	for _, w := range plan.Words {
		switch w.Status {
		case HardSevere:
			severe++
		case HardModerate:
			moderate++
		case HardMild:
			mild++
		}
	}

	if *outputPath == "" {
		*outputPath = fmt.Sprintf("outputs/hard_words_%s_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
	}
	if dir := filepath.Dir(*outputPath); dir != "" {
		os.MkdirAll(dir, 0755)
	}

	if err := offline.GenerateHardExcel(plan, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating Excel: %v\n", err)
		os.Exit(1)
	}

	// Upload the plan JSON so that `record --hard` can resolve word numbers.
	if err := storage.UploadHardPlan(ctx, plan); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload hard plan: %v\n", err)
	}

	// Upload Excel to COS (backup)
	if err := storage.UploadHardExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
	}

	outputResult(map[string]interface{}{
		"success":          true,
		"command":          "export-hard",
		"date":             dateStr,
		"language":         lang,
		"min_accuracy":     *minAcc,
		"min_reviews":      *minRev,
		"hard_count":       len(plan.Words),
		"severe_count":     severe,
		"moderate_count":   moderate,
		"mild_count":       mild,
		"excel_path":       *outputPath,
		"plan_key":         fmt.Sprintf("plans/hard_%s.json", dateStr),
		"archive_filename": archiveFilename,
		"version":          fmt.Sprintf("v%d.%d", arcMajor, arcMinor),
		"plan_words":       plan.Words,
	})
}
