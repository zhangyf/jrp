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

// Hard-word severity sections, split by accuracy rate.
const (
	HardSevere   = "🔥重度钉子户（正确率<30%）"
	HardModerate = "⚠️中度钉子户（正确率30~45%）"
	HardMild     = "💤轻度钉子户（正确率45~60%）"
)

// hardCategories is the section order for the hard-word Excel.
var hardCategories = []string{HardSevere, HardModerate, HardMild}

// Severity boundaries.
const (
	hardSevereMax   = 0.30
	hardModerateMax = 0.45
)

// hardSeverity buckets a word by accuracy.
//
// NOTE: the boundaries are independent of --min-accuracy. If the user lowers
// the threshold to 0.45, the MILD bucket simply ends up empty.
func hardSeverity(acc float64) string {
	switch {
	case acc < hardSevereMax:
		return HardSevere
	case acc < hardModerateMax:
		return HardModerate
	default:
		return HardMild
	}
}

// BuildHardPlan collects ALL hard words from the archive — deliberately without
// any IsDue() filtering, which is the essential difference from BuildDuePlan:
// gen-plan only surfaces words due today, whereas this is a full census.
//
// Words are sorted by accuracy ascending. Because the severity boundaries depend
// monotonically on accuracy, this ordering also yields the
// severe → moderate → mild section order for free. Numbering is continuous
// across sections.
func BuildHardPlan(arc *Archive, lang string, targetDate time.Time,
	minAcc float64, minRev int) *ReviewPlan {

	var hard []Word
	for _, w := range AllWords(arc.Groups) {
		if IsHardWord(w, minAcc, minRev) {
			hard = append(hard, w)
		}
	}

	sort.SliceStable(hard, func(i, j int) bool {
		ai, _ := Accuracy(hard[i])
		aj, _ := Accuracy(hard[j])
		if ai != aj {
			return ai < aj // lower accuracy first
		}
		if hard[i].ErrorCount != hard[j].ErrorCount {
			return hard[i].ErrorCount > hard[j].ErrorCount // more errors first
		}
		return hard[i].Word < hard[j].Word // stable tie-break
	})

	plan := &ReviewPlan{
		Date:        targetDate.Format("2006-01-02"),
		Language:    lang,
		Kind:        "hard",
		MinAccuracy: minAcc,
		MinReviews:  minRev,
	}
	for i, w := range hard {
		acc, _ := Accuracy(w)
		accVal := acc // take address of a per-iteration copy
		plan.Words = append(plan.Words, PlanWord{
			Number:      i + 1,
			Word:        w.Word,
			Definition:  w.Definition,
			Group:       w.Group,
			Status:      hardSeverity(acc),
			ErrorCount:  w.ErrorCount,
			ReviewCount: w.ReviewCount,
			Accuracy:    &accVal,
		})
	}
	return plan
}

func runExportHard(fs *flag.FlagSet, lang string) {
	outputPath := fs.String("output", "", "Output Excel file path (default: outputs/hard_words_<date>_vA.B.xlsx)")
	minAcc := fs.Float64("min-accuracy", DefaultHardMinAccuracy, "Accuracy threshold (0-1); words below this are hard words")
	minRev := fs.Int("min-reviews", DefaultHardMinReviews, "Minimum review count required to qualify as a hard word")
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
	if _, maj, min, perr := ParseFilename(archiveFilename); perr == nil {
		arcMajor, arcMinor = maj, min
	}

	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	dateStr := targetDate.Format("2006-01-02")
	plan := BuildHardPlan(arc, lang, targetDate, *minAcc, *minRev)

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

	if err := GenerateHardExcel(plan, *outputPath); err != nil {
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
