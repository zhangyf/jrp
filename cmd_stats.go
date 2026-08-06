package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

func runStats(fs *flag.FlagSet, lang string) {
	days := fs.Int("days", 7, "Number of days to look back")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// List all archives
	objs, err := storage.ListAllArchives(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing archives: %v\n", err)
		os.Exit(1)
	}

	// Find unique dates within the last N days
	today := time.Now()
	cutoff := today.AddDate(0, 0, -*days)

	// Group archives by date, keep the latest version per date
	type dateEntry struct {
		date     time.Time
		filename string
		key      string
	}
	dateMap := make(map[string]dateEntry)

	for _, obj := range objs {
		filename := obj.Key
		fname := filename
		// Extract just the base name
		if idx := lastIndexOf(fname, "/"); idx >= 0 {
			fname = fname[idx+1:]
		}

		fdate, _, _, err := ParseFilename(fname)
		if err != nil {
			continue
		}

		if fdate.Before(cutoff) || fdate.After(today) {
			continue
		}

		dateKey := fdate.Format("060102")
		existing, exists := dateMap[dateKey]
		if !exists || fdate.After(existing.date) {
			dateMap[dateKey] = dateEntry{
				date:     fdate,
				filename: fname,
				key:      obj.Key,
			}
		}
	}

	// Sort by date
	var entries []dateEntry
	for _, e := range dateMap {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].date.Before(entries[j].date)
	})

	// Download and parse each archive, extract stats.
	// Also capture the last successfully parsed archive for detail breakdown.
	var snapshots []StatsSnapshot
	var lastArchive *Archive
	for _, e := range entries {
		data, err := storage.store.GetAll(ctx, e.key)
		if err != nil {
			continue
		}

		arc, err := ParseArchive(string(data), lang)
		if err != nil {
			continue
		}
		lastArchive = arc

		allWords := AllWords(arc.Groups)
		mastered, basic, needsConsol, untested := CountByStatus(allWords)

		// Get version from filename
		_, major, minor, _ := ParseFilename(e.filename)

		snapshots = append(snapshots, StatsSnapshot{
			Date:        e.date.Format("01/02"),
			Version:     fmt.Sprintf("v%d.%d", major, minor),
			Total:       len(allWords),
			Mastered:    mastered,
			Basic:       basic,
			NeedsConsol: needsConsol,
			Untested:    untested,
			Errors:      TotalErrors(arc.Groups),
		})
	}

	// Build detail from the last successfully parsed archive (reuse, no second download)
	var detail *StatsDetail
	if lastArchive != nil {
		detail = buildStatsDetail(lastArchive.Groups)
	}

	// Calculate changes
	changes := make(map[string]string)
	if len(snapshots) >= 2 {
		first := snapshots[0]
		last := snapshots[len(snapshots)-1]

		changes["total_change"] = fmt.Sprintf("%d → %d (%+d)", first.Total, last.Total, last.Total-first.Total)
		changes["mastered_change"] = fmt.Sprintf("%d → %d (%+d)", first.Mastered, last.Mastered, last.Mastered-first.Mastered)
		changes["basic_change"] = fmt.Sprintf("%d → %d (%+d)", first.Basic, last.Basic, last.Basic-first.Basic)
		changes["needs_consol_change"] = fmt.Sprintf("%d → %d (%+d)", first.NeedsConsol, last.NeedsConsol, last.NeedsConsol-first.NeedsConsol)
		changes["errors_change"] = fmt.Sprintf("%d → %d (%+d)", first.Errors, last.Errors, last.Errors-first.Errors)
		changes["period"] = fmt.Sprintf("%s ~ %s", first.Date, last.Date)
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "stats",
		"language":  lang,
		"days":      *days,
		"snapshots": snapshots,
		"changes":   changes,
		"detail":    detail,
	})
}

func lastIndexOf(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// buildStatsDetail extracts per-lesson distribution, accuracy buckets, hard-word
// counts, and top-reviewed words from the latest archive's parsed word groups.
func buildStatsDetail(groups []WordGroup) *StatsDetail {
	words := AllWords(groups)

	// Per-lesson distribution
	lessonMap := make(map[string]int)
	for _, w := range words {
		lessonMap[w.Group]++
	}
	byLesson := make([]LessonCount, 0, len(lessonMap))
	for lesson, count := range lessonMap {
		byLesson = append(byLesson, LessonCount{Lesson: lesson, Count: count})
	}
	sort.Slice(byLesson, func(i, j int) bool {
		return byLesson[i].Lesson < byLesson[j].Lesson
	})

	// Accuracy distribution (only reviewed words)
	accDist := map[string]int{
		"0-30%": 0, "30-60%": 0, "60-80%": 0, "80-90%": 0, "90-100%": 0,
	}
	for _, w := range words {
		acc, ok := Accuracy(w)
		if !ok {
			continue
		}
		switch {
		case acc < 0.3:
			accDist["0-30%"]++
		case acc < 0.6:
			accDist["30-60%"]++
		case acc < 0.8:
			accDist["60-80%"]++
		case acc < 0.9:
			accDist["80-90%"]++
		default:
			accDist["90-100%"]++
		}
	}

	// Hard word counts by severity (using canonical IsHardWord with defaults)
	var hw HardWordCounts
	for _, w := range words {
		if !IsHardWord(w, DefaultHardMinAccuracy, DefaultHardMinReviews) {
			continue
		}
		hw.Total++
		acc, _ := Accuracy(w)
		switch {
		case acc < 0.3:
			hw.Severe++
		case acc < 0.45:
			hw.Moderate++
		default:
			hw.Mild++
		}
	}

	// Top 10 most-reviewed words
	sort.Slice(words, func(i, j int) bool {
		return words[i].ReviewCount > words[j].ReviewCount
	})
	topN := 10
	if len(words) < topN {
		topN = len(words)
	}
	topReviewed := make([]TopReviewedWord, 0, topN)
	for i := 0; i < topN; i++ {
		w := words[i]
		acc, _ := Accuracy(w)
		topReviewed = append(topReviewed, TopReviewedWord{
			Word:     w.Word,
			Reviews:  w.ReviewCount,
			Errors:   w.ErrorCount,
			Accuracy: acc,
		})
	}

	return &StatsDetail{
		ByLesson:             byLesson,
		AccuracyDistribution: accDist,
		HardWords:            hw,
		TopReviewed:          topReviewed,
	}
}
