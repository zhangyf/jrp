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

	// Download and parse each archive, extract stats
	var snapshots []StatsSnapshot
	for _, e := range entries {
		data, err := storage.store.GetAll(ctx, e.key)
		if err != nil {
			continue
		}

		arc, err := ParseArchive(string(data), lang)
		if err != nil {
			continue
		}

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
