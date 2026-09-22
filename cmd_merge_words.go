package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// runMergeWords merges two archive entries that are the same word stored under
// two different surface forms (e.g. すし and すし(寿司)).
//
// This is the case `dedupe` cannot handle: dedupe only matches byte-identical
// word text, so a bare-kana entry and its kanji-annotated twin survive forever,
// splitting one word's review history into two rows. Each row then carries its
// own review count, error count and status, so accuracy is computed against a
// partial history and the word total is inflated.
//
// Merge rules (chosen to be conservative — never *downgrade* a word):
//   - ReviewCount: sum (both rows are real reviews of the same word)
//   - ErrorCount:  sum (every recorded error really happened)
//   - ConsecutiveCorrect: max (it is "correct streak since the last error";
//     summing two streaks would invent a streak that never occurred)
//   - LastReview:  the later of the two MM/DD values
//   - Status:      recomputed from the merged numbers via DetermineStatus
//
// The merge target (into) keeps its group; the source (from) row is deleted.
func runMergeWords(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with merge list (default: stdin)")
	dryRun := fs.Bool("dry-run", false, "Report the merge plan without uploading")
	fs.Parse(cmdArgs)

	var input struct {
		Language string `json:"language"`
		Merges   []struct {
			From string `json:"from"`
			Into string `json:"into"`
		} `json:"merges"`
	}

	if *inputFile != "" {
		if err := readJSONFile(*inputFile, &input); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := jsonDecoder(os.Stdin).Decode(&input); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
	}
	if input.Language == "" {
		input.Language = lang
	}
	if len(input.Merges) == 0 {
		fmt.Fprintf(os.Stderr, "No merges supplied\n")
		os.Exit(1)
	}

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

	// Locate both sides up front so a typo aborts before anything is mutated.
	type loc struct{ gi, wi int }
	locate := func(word string) (loc, *Word, string) {
		for gi := range arc.Groups {
			for wi := range arc.Groups[gi].Words {
				if arc.Groups[gi].Words[wi].Word == word {
					return loc{gi, wi}, &arc.Groups[gi].Words[wi], arc.Groups[gi].Title
				}
			}
		}
		return loc{}, nil, ""
	}

	type mergeReport struct {
		From     string `json:"from"`
		Into     string `json:"into"`
		FromRow  string `json:"from_row"`
		IntoRow  string `json:"into_row"`
		Merged   string `json:"merged_row"`
		Group    string `json:"group"`
		FromGrp  string `json:"from_group"`
		Interval int    `json:"next_interval_days"`
	}

	remove := make(map[loc]bool)
	var reports []mergeReport
	var fails []string

	for _, m := range input.Merges {
		if m.From == m.Into {
			fails = append(fails, fmt.Sprintf("from == into: %s", m.From))
			continue
		}
		fl, fw, fGroup := locate(m.From)
		_, iw, iGroup := locate(m.Into)
		if fw == nil {
			fails = append(fails, fmt.Sprintf("source not found: %s", m.From))
			continue
		}
		if iw == nil {
			fails = append(fails, fmt.Sprintf("target not found: %s", m.Into))
			continue
		}
		if _, bad := remove[fl]; bad {
			fails = append(fails, fmt.Sprintf("source already merged: %s", m.From))
			continue
		}

		fromRow := fmt.Sprintf("%s | 复习%d 错%d 连%d | %s | %s",
			fw.Word, fw.ReviewCount, fw.ErrorCount, fw.ConsecutiveCorrect, fw.LastReview, fw.Status)
		intoRow := fmt.Sprintf("%s | 复习%d 错%d 连%d | %s | %s",
			iw.Word, iw.ReviewCount, iw.ErrorCount, iw.ConsecutiveCorrect, iw.LastReview, iw.Status)
		iw.ReviewCount += fw.ReviewCount
		iw.ErrorCount += fw.ErrorCount
		if fw.ConsecutiveCorrect > iw.ConsecutiveCorrect {
			iw.ConsecutiveCorrect = fw.ConsecutiveCorrect
		}
		iw.LastReview = laterMMDD(iw.LastReview, fw.LastReview)
		iw.Status = DetermineStatus(*iw)

		mergedRow := fmt.Sprintf("%s | 复习%d 错%d 连%d | %s | %s",
			iw.Word, iw.ReviewCount, iw.ErrorCount, iw.ConsecutiveCorrect, iw.LastReview, iw.Status)

		remove[fl] = true
		reports = append(reports, mergeReport{
			From: m.From, Into: m.Into, FromRow: fromRow, IntoRow: intoRow,
			Merged: mergedRow, Group: iGroup, FromGrp: fGroup,
			Interval: GetWordInterval(*iw),
		})
	}

	if len(fails) > 0 {
		fmt.Fprintf(os.Stderr, "ABORT (archive untouched): %s\n", strings.Join(fails, "; "))
		os.Exit(1)
	}

	if *dryRun {
		outputResult(map[string]interface{}{
			"success": true,
			"command": "merge-words",
			"dry_run": true,
			"archive": oldFilename,
			"merges":  reports,
		})
		return
	}

	// Drop the source rows; drop groups that become empty.
	var cleaned []WordGroup
	for gi := range arc.Groups {
		var kept []Word
		for wi := range arc.Groups[gi].Words {
			if remove[loc{gi, wi}] {
				continue
			}
			kept = append(kept, arc.Groups[gi].Words[wi])
		}
		if len(kept) > 0 {
			cleaned = append(cleaned, WordGroup{Title: arc.Groups[gi].Title, Words: kept})
		}
	}
	arc.Groups = cleaned

	backupName := strings.TrimSuffix(oldFilename, ".md") +
		fmt.Sprintf("_backup_%s.md", time.Now().Format("20060102-150405"))
	if err := storage.UploadHistory(ctx, backupName, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading backup (aborting, archive untouched): %v\n", err)
		os.Exit(1)
	}

	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, false)

	names := make([]string, 0, len(reports))
	for _, r := range reports {
		names = append(names, fmt.Sprintf("%s→%s", r.From, r.Into))
	}
	sort.Strings(names)
	description := fmt.Sprintf("合并同词异形 %d 组（%s）：复习次数与错误数累加，删除冗余条目",
		len(reports), strings.Join(names, "、"))
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)
	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "merge-words",
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"backup":       "history/" + backupName,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
		"merged":       len(reports),
		"merges":       reports,
	})
}

// laterMMDD returns the later of two MM/DD strings. Both are bare month/day
// (the archive format), so this assumes the same year — sound for entries
// inside one archive, and the fallback returns whichever parsed.
func laterMMDD(a, b string) string {
	ma, da, okA := splitMMDD(a)
	mb, db, okB := splitMMDD(b)
	switch {
	case !okA && !okB:
		if strings.TrimSpace(a) == "" {
			return b
		}
		return a
	case !okA:
		return b
	case !okB:
		return a
	}
	if ma != mb {
		if ma > mb {
			return a
		}
		return b
	}
	if da >= db {
		return a
	}
	return b
}

func splitMMDD(s string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), "/")
	if len(parts) != 2 {
		return 0, 0, false
	}
	m, err1 := strconv.Atoi(parts[0])
	d, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return m, d, true
}
