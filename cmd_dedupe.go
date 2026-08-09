package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type dupInfo struct {
	Word       string `json:"word"`
	Group      string `json:"group"`
	Definition string `json:"definition"`
	ReviewCnt  int    `json:"review_count"`
	ErrorCnt   int    `json:"error_count"`
	Kept       bool   `json:"kept"`
}

type dupGroup struct {
	Word    string    `json:"word"`
	Entries []dupInfo `json:"entries"`
	Removed int       `json:"removed"`
}

func runDedupe(fs *flag.FlagSet, lang string) {
	dryRun := fs.Bool("dry-run", false, "Report duplicates without uploading")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	data, oldFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading latest archive: %v\n", err)
		os.Exit(1)
	}

	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// word text -> list of (groupIndex, wordIndex, *Word)
	type wordLoc struct {
		gi int
		wi int
		w  Word
	}
	wordMap := make(map[string][]wordLoc)

	for gi := range arc.Groups {
		for wi := range arc.Groups[gi].Words {
			w := arc.Groups[gi].Words[wi]
			wordMap[w.Word] = append(wordMap[w.Word], wordLoc{gi, wi, w})
		}
	}

	// Collect duplicates (words appearing >1 time)
	var dupGroups []dupGroup
	for _, locs := range wordMap {
		if len(locs) <= 1 {
			continue
		}

		// Find the best entry: highest ReviewCount; tie-break on ErrorCount (fewer is better)
		bestIdx := 0
		for i := 1; i < len(locs); i++ {
			if locs[i].w.ReviewCount > locs[bestIdx].w.ReviewCount {
				bestIdx = i
			} else if locs[i].w.ReviewCount == locs[bestIdx].w.ReviewCount &&
				locs[i].w.ErrorCount < locs[bestIdx].w.ErrorCount {
				bestIdx = i
			}
		}

		dg := dupGroup{Word: locs[0].w.Word}
		for i, loc := range locs {
			dg.Entries = append(dg.Entries, dupInfo{
				Word:       loc.w.Word,
				Group:      arc.Groups[loc.gi].Title,
				Definition: loc.w.Definition,
				ReviewCnt:  loc.w.ReviewCount,
				ErrorCnt:   loc.w.ErrorCount,
				Kept:       i == bestIdx,
			})
			if i != bestIdx {
				dg.Removed++
			}
		}
		dupGroups = append(dupGroups, dg)
	}

	totalBefore := len(AllWords(arc.Groups))

	if *dryRun {
		outputResult(map[string]interface{}{
			"success":      true,
			"command":      "dedupe",
			"dry_run":      true,
			"archive":      oldFilename,
			"total_before": totalBefore,
			"dup_groups":   len(dupGroups),
			"removals":     sumRemovals(dupGroups),
			"duplicates":   dupGroups,
		})
		return
	}

	if len(dupGroups) == 0 {
		outputResult(map[string]interface{}{
			"success":      true,
			"command":      "dedupe",
			"archive":      oldFilename,
			"total_before": totalBefore,
			"message":      "No duplicate entries found; archive unchanged.",
		})
		return
	}

	// Phase 2: remove duplicates from the archive
	// Build a set of (gi, wi) to remove
	type toRemove struct{ gi, wi int }
	removeSet := make(map[toRemove]bool)

	for _, locs := range wordMap {
		if len(locs) <= 1 {
			continue
		}
		// Find best
		bestIdx := 0
		for i := 1; i < len(locs); i++ {
			if locs[i].w.ReviewCount > locs[bestIdx].w.ReviewCount {
				bestIdx = i
			} else if locs[i].w.ReviewCount == locs[bestIdx].w.ReviewCount &&
				locs[i].w.ErrorCount < locs[bestIdx].w.ErrorCount {
				bestIdx = i
			}
		}
		for i, loc := range locs {
			if i != bestIdx {
				removeSet[toRemove{loc.gi, loc.wi}] = true
			}
		}
	}

	// Filter words in place, then remove empty groups
	var cleanedGroups []WordGroup
	for gi := range arc.Groups {
		var kept []Word
		for wi := range arc.Groups[gi].Words {
			if removeSet[toRemove{gi, wi}] {
				continue
			}
			kept = append(kept, arc.Groups[gi].Words[wi])
		}
		if len(kept) > 0 {
			cleanedGroups = append(cleanedGroups, WordGroup{
				Title: arc.Groups[gi].Title,
				Words: kept,
			})
		}
	}
	arc.Groups = cleanedGroups

	totalAfter := len(AllWords(arc.Groups))
	expectedAfter := totalBefore - sumRemovals(dupGroups)
	if totalAfter != expectedAfter {
		fmt.Fprintf(os.Stderr,
			"ABORT: word count assertion failed — expected %d after cleanup, got %d\n",
			expectedAfter, totalAfter)
		os.Exit(1)
	}

	// Backup original
	backupName := strings.TrimSuffix(oldFilename, ".md") +
		fmt.Sprintf("_backup_%s.md", time.Now().Format("20060102-150405"))
	if err := storage.UploadHistory(ctx, backupName, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading backup (aborting, archive untouched): %v\n", err)
		os.Exit(1)
	}

	// Version bump
	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, false)

	removalCount := sumRemovals(dupGroups)
	description := fmt.Sprintf("去重：清理%d组重复词，删除%d条条目", len(dupGroups), removalCount)
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)

	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "dedupe",
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"backup":       "history/" + backupName,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
		"total_before": totalBefore,
		"total_after":  totalAfter,
		"dup_groups":   len(dupGroups),
		"removals":     removalCount,
		"duplicates":   dupGroups,
	})
}

func sumRemovals(dups []dupGroup) int {
	total := 0
	for _, d := range dups {
		total += d.Removed
	}
	return total
}
