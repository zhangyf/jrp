// cmd_words.go —— 词库 / 词条写入类命令
//
// 腿一 R1：把原本平铺在根目录的 8 个 cmd_*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；命令行为、输出、存档字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找命令实现先看下面的来源清单。
//
// 来源与顶层声明：
// cmd_add_words.go             runAddWords
// cmd_dedupe.go                dupInfo, dupGroup, runDedupe, sumRemovals
// cmd_merge_words.go           runMergeWords, laterMMDD, splitMMDD
// cmd_normalize_words.go       isKana, isKanji, classifySegment, NormalizeWordForm, runNormalizeWords
// cmd_update_def.go            runUpdateDef
// cmd_update_word.go           runUpdateWord
// cmd_words.go              runWordMeta
// cmd_wordnote.go              runWordNote, listNotes
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

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/words"
)

// ==========================================================================
// 原 cmd_add_words.go
// ==========================================================================

// 释义撞车的纯规则已迁 internal/words（SameDefinitionWords / DefinitionCore /
// SameCoreWords），由 words.Add 用例统一调用。根包不再各存一份。
// circular / update-def 两个调用方直接调 words 包的同名导出函数。

func runAddWords(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with words to add (default: stdin)")
	groupName := fs.String("group", "", "Override group name for all words")
	majorBump := fs.Bool("major", false, "Force major version bump")
	fs.Parse(cmdArgs)

	// Read input
	var input AddWordsInput
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
	if *groupName != "" {
		input.Group = *groupName
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	// 加词的实现在 words_add.go，跟「录入新词」的 API 是同一份
	items := make([]WordToAdd, 0, len(input.Words))
	for _, w := range input.Words {
		items = append(items, WordToAdd{Word: w.Word, Definition: w.Definition})
	}

	res, err := addWordsToArchive(context.Background(), storage, input.Group, items, *majorBump)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	out := map[string]interface{}{
		"success":      true,
		"command":      "add-words",
		"added":        res.Added,
		"duplicates":   res.Duplicates,
		"old_filename": res.OldFilename,
		"new_filename": res.NewFilename,
		"version":      res.Version,
		"major_bump":   res.MajorBump,
		"total_words":  res.TotalWords,
	}
	if len(res.DupDefs) > 0 {
		out["dup_definitions"] = res.DupDefs
	}
	if len(res.DupCores) > 0 {
		out["dup_core_definitions"] = res.DupCores
	}
	outputResult(out)
}

// ==========================================================================
// 原 cmd_dedupe.go
// ==========================================================================

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

	arc, err := archive.ParseArchive(string(data), lang)
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

	totalBefore := len(review.AllWords(arc.Groups))

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

	totalAfter := len(review.AllWords(arc.Groups))
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
	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, false)

	removalCount := sumRemovals(dupGroups)
	description := fmt.Sprintf("去重：清理%d组重复词，删除%d条条目", len(dupGroups), removalCount)
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, description)

	cfg := LangConfigs[lang]
	newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
	newFilename := archive.Filename(LangConfigs[lang].FilePrefix, today, newMajor, newMinor)

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

// ==========================================================================
// 原 cmd_merge_words.go
// ==========================================================================

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

	arc, err := archive.ParseArchive(string(data), lang)
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
		iw.Status = review.DetermineStatus(*iw)

		mergedRow := fmt.Sprintf("%s | 复习%d 错%d 连%d | %s | %s",
			iw.Word, iw.ReviewCount, iw.ErrorCount, iw.ConsecutiveCorrect, iw.LastReview, iw.Status)

		remove[fl] = true
		reports = append(reports, mergeReport{
			From: m.From, Into: m.Into, FromRow: fromRow, IntoRow: intoRow,
			Merged: mergedRow, Group: iGroup, FromGrp: fGroup,
			Interval: review.GetWordInterval(*iw),
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

	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, false)

	names := make([]string, 0, len(reports))
	for _, r := range reports {
		names = append(names, fmt.Sprintf("%s→%s", r.From, r.Into))
	}
	sort.Strings(names)
	description := fmt.Sprintf("合并同词异形 %d 组（%s）：复习次数与错误数累加，删除冗余条目",
		len(reports), strings.Join(names, "、"))
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, description)

	cfg := LangConfigs[lang]
	newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
	newFilename := archive.Filename(LangConfigs[lang].FilePrefix, today, newMajor, newMinor)
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

// ==========================================================================
// 原 cmd_normalize_words.go
// ==========================================================================

// isKana reports whether r is hiragana, katakana, or a kana-adjacent mark
// (long vowel ー, middle dot ・, iteration marks).
func isKana(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x309F: // hiragana
		return true
	case r >= 0x30A0 && r <= 0x30FF: // katakana (incl. ー and ・)
		return true
	case r >= 0xFF66 && r <= 0xFF9F: // halfwidth katakana
		return true
	}
	return false
}

// isKanji reports whether r is a CJK ideograph.
func isKanji(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // Extension A
		(r >= 0xF900 && r <= 0xFAFF) // Compatibility Ideographs
}

// classifySegment reports whether a string is predominantly kana or kanji.
// Words like 昼ご飯 mix both; the rule is: if it contains ANY kanji it is
// classified as the kanji side, otherwise kana.
func classifySegment(s string) (hasKana, hasKanji bool) {
	for _, r := range s {
		if isKanji(r) {
			hasKanji = true
		} else if isKana(r) {
			hasKana = true
		}
	}
	return
}

// NormalizeWordForm rewrites a word entry so the reading (kana) is outside the
// parentheses and the kanji spelling is inside:畏まりました(かしこまりました)
// becomes かしこまりました(畏まりました).
//
// Returns the normalized form and whether a change was made. Entries without
// parentheses, without kanji, or already in the correct orientation are
// returned unchanged.
func NormalizeWordForm(word string) (string, bool) {
	open := strings.IndexAny(word, "(（")
	if open < 0 {
		return word, false
	}

	// Find the matching close paren
	var closeIdx int = -1
	for i, r := range word[open:] {
		if r == ')' || r == '）' {
			closeIdx = open + i
			break
		}
	}
	if closeIdx < 0 {
		return word, false
	}

	outer := strings.TrimSpace(word[:open])
	inner := strings.TrimSpace(word[open+len("(") : closeIdx])
	// Anything trailing after the close paren (rare, e.g. "～です")
	tail := word[closeIdx+len(")"):]

	if outer == "" || inner == "" {
		return word, false
	}

	outerHasKana, outerHasKanji := classifySegment(outer)
	innerHasKana, innerHasKanji := classifySegment(inner)

	// Only swap when the orientation is unambiguously reversed:
	// outer contains kanji AND inner is pure kana.
	if outerHasKanji && innerHasKana && !innerHasKanji {
		return inner + "(" + outer + ")" + tail, true
	}

	// Already correct (outer pure kana, inner has kanji) or ambiguous — leave alone.
	_ = outerHasKana
	_ = innerHasKanji
	return word, false
}

func runNormalizeWords(fs *flag.FlagSet, lang string) {
	dryRun := fs.Bool("dry-run", false, "Report the planned changes without uploading")
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

	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// Collect changes
	type change struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	var changes []change

	for gi := range arc.Groups {
		for wi := range arc.Groups[gi].Words {
			w := &arc.Groups[gi].Words[wi]
			newForm, changed := NormalizeWordForm(w.Word)
			if !changed {
				continue
			}
			changes = append(changes, change{Old: w.Word, New: newForm})
			if !*dryRun {
				w.Word = newForm
			}
		}
	}

	totalWords := len(review.AllWords(arc.Groups))

	if *dryRun {
		outputResult(map[string]interface{}{
			"success":      true,
			"command":      "normalize-words",
			"dry_run":      true,
			"archive":      oldFilename,
			"total_words":  totalWords,
			"change_count": len(changes),
			"changes":      changes,
		})
		return
	}

	if len(changes) == 0 {
		outputResult(map[string]interface{}{
			"success":      true,
			"command":      "normalize-words",
			"archive":      oldFilename,
			"total_words":  totalWords,
			"change_count": 0,
			"message":      "All word forms already normalized; archive unchanged.",
		})
		return
	}

	// Back up the original archive to history/ BEFORE writing anything new.
	backupName := strings.TrimSuffix(oldFilename, ".md") +
		fmt.Sprintf("_backup_%s.md", time.Now().Format("20060102-150405"))
	if err := storage.UploadHistory(ctx, backupName, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading backup (aborting, archive untouched): %v\n", err)
		os.Exit(1)
	}

	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	today := time.Now()
	//20+ entries changed is a structural rewrite → major bump
	majorBump := len(changes) >= 20
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, majorBump)

	description := fmt.Sprintf("规范化词形：假名在外汉字在括号内（%d词）", len(changes))
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, description)

	cfg := LangConfigs[lang]
	newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
	newFilename := archive.Filename(LangConfigs[lang].FilePrefix, today, newMajor, newMinor)

	// Sanity check: word count must not change
	newTotal := len(review.AllWords(arc.Groups))
	if newTotal != totalWords {
		fmt.Fprintf(os.Stderr,
			"ABORT: word count changed %d → %d, refusing to upload. Backup at history/%s\n",
			totalWords, newTotal, backupName)
		os.Exit(1)
	}

	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "normalize-words",
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"backup":       "history/" + backupName,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
		"total_words":  totalWords,
		"change_count": len(changes),
		"changes":      changes,
	})
}

// ==========================================================================
// 原 cmd_update_def.go
// ==========================================================================

func runUpdateDef(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with update info (default: stdin)")
	fs.Parse(cmdArgs)

	// Read input
	var input UpdateDefInput
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

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Download latest archive
	data, oldFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading latest archive: %v\n", err)
		os.Exit(1)
	}

	// Parse archive
	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// Find the word and update
	w, _ := review.FindWord(arc.Groups, input.Word)
	if w == nil {
		fmt.Fprintf(os.Stderr, "Word not found: %s\n", input.Word)
		os.Exit(1)
	}

	oldDef := w.Definition
	w.Definition = input.Definition

	// 改完还跟别的词一字不差的话，复习时照样分不出来 —— 点名提醒。
	sameAs := words.SameDefinitionWords(arc.Groups, input.Definition, w.Word)
	// 主部（括号前）撞车也要点名：不是一字不差照样没区分度。
	sameCoreAs := words.SameCoreWords(arc.Groups, input.Definition, w.Word)

	// Calculate version
	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, false)

	// Add changelog entry
	description := fmt.Sprintf("更新释义：%s（%s → %s）", input.Word, oldDef, input.Definition)
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, description)

	// Write and upload
	cfg := LangConfigs[lang]
	newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
	newFilename := archive.Filename(LangConfigs[lang].FilePrefix, today, newMajor, newMinor)

	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	out := map[string]interface{}{
		"success":      true,
		"command":      "update-def",
		"word":         input.Word,
		"old_def":      oldDef,
		"new_def":      input.Definition,
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
	}
	if len(sameAs) > 0 {
		out["same_def_as"] = sameAs
	}
	if len(sameCoreAs) > 0 {
		out["same_core_as"] = sameCoreAs
	}
	outputResult(out)
}

// ==========================================================================
// 原 cmd_update_word.go
// ==========================================================================

// runUpdateWord updates a word's target-language form (the 日语单词 column),
// used to fix typos like a stray long-vowel mark in the kanji annotation
// (e.g. さんぽします(散歩ー) → さんぽします(散歩します)).
func runUpdateWord(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with update info (default: stdin)")
	fs.Parse(cmdArgs)

	var input UpdateWordInput
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

	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	w, _ := review.FindWord(arc.Groups, input.Word)
	if w == nil {
		fmt.Fprintf(os.Stderr, "Word not found: %s\n", input.Word)
		os.Exit(1)
	}

	// Guard against creating a duplicate entry.
	if existing, _ := review.FindWord(arc.Groups, input.NewWord); existing != nil && existing != w {
		fmt.Fprintf(os.Stderr, "Target form already exists in archive: %s\n", input.NewWord)
		os.Exit(1)
	}

	oldWord := w.Word
	w.Word = input.NewWord

	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, false)

	description := fmt.Sprintf("更新词形：%s → %s", oldWord, input.NewWord)
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, description)

	cfg := LangConfigs[lang]
	newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
	newFilename := archive.Filename(LangConfigs[lang].FilePrefix, today, newMajor, newMinor)

	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "update-word",
		"old_word":     oldWord,
		"new_word":     input.NewWord,
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
	})
}

// ==========================================================================
// 原 cmd_words.go
// ==========================================================================

// runWordMeta 词性表的上传与查看。
//
//	jrp --lang ja word-meta --dump word_meta.json   # 下载到本地（改之前先 dump）
//	jrp --lang ja word-meta --file word_meta.json   # 上传（整份覆盖）
//	jrp --lang ja word-meta                         # 下载并打印统计
//
// 词性不在档案里，单独存一份 COS 上的 word_meta.json。
// 上传是整份覆盖 —— 词性表小（930 条约 60KB），没必要做增量合并。
func runWordMeta(fs *flag.FlagSet, lang string) {
	file := fs.String("file", "", "本地 word_meta.json 路径；不给则下载并打印统计")
	dump := fs.String("dump", "", "把词性表下载到本地文件（改之前先 dump，改完再 --file 上传）")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}
		var m WordMeta
		if err := jsonUnmarshal(data, &m); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			os.Exit(1)
		}
		if len(m.Items) == 0 {
			fmt.Fprintln(os.Stderr, "Error: items is empty, refusing to overwrite")
			os.Exit(1)
		}
		if err := storage.UploadWordMeta(ctx, &m); err != nil {
			fmt.Fprintf(os.Stderr, "Error uploading: %v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "word-meta", "uploaded": len(m.Items),
			"key": storage.wordMetaKey(),
		})
		return
	}

	m, err := storage.DownloadWordMeta(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading: %v\n", err)
		os.Exit(1)
	}

	// 上传是整份覆盖，所以必须先能完整导出，不然改一条就丢一批
	if *dump != "" {
		if err := os.WriteFile(*dump, []byte(toJSON(m)+"\n"), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing dump: %v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "word-meta", "dumped": len(m.Items), "path": *dump,
		})
		return
	}
	summary := map[string]int{}
	for _, p := range m.Items {
		key := p.Pos
		if p.Sub != "" {
			key = p.Pos + "·" + p.Sub
		}
		if key == "" {
			key = "未标注"
		}
		summary[key]++
	}
	keys := make([]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, map[string]interface{}{"pos": k, "count": summary[k]})
	}
	outputResult(map[string]interface{}{
		"success": true, "command": "word-meta", "total": len(m.Items),
		"updated": m.Updated, "note": m.Note, "summary": rows,
	})
}

// ==========================================================================
// 原 cmd_wordnote.go
// ==========================================================================

// runWordNote 维护词条的「备注」—— 主要写跟近义词怎么区分。
//
//	jrp --lang ja word-note --word とおります(通ります) --text "空间上从某处穿过；過ぎます 是时间流逝/超过某点"
//	jrp --lang ja word-note --word とおります(通ります) --clear
//	jrp --lang ja word-note --file notes.json     # {"词形": "备注"} 批量合并
//	jrp --lang ja word-note                       # 列出现有备注
//
// 备注存在 word_meta.json 的 items[词形].note，跟词性同一份表。
// 词义明确、不会跟别的词混的就不用写 —— 空着是正常的，不是缺数据。
// 页面上的兜底：这个词没写备注，就把释义括号里的那截当备注显示
// （见前端 app.defParts），所以老词不用补录也有东西看。
func runWordNote(fs *flag.FlagSet, lang string) {
	word := fs.String("word", "", "词形（档案里的完整形式，如 とおります(通ります)）")
	text := fs.String("text", "", "备注文字")
	clear := fs.Bool("clear", false, "清除这个词的备注")
	file := fs.String("file", "", "JSON 文件：{\"词形\": \"备注\"}，合并进已有的表")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	m, err := storage.DownloadWordMeta(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading word meta: %v\n", err)
		os.Exit(1)
	}
	if m.Items == nil {
		m.Items = map[string]WordPOS{}
	}

	// 没给任何写操作参数 → 只列出现有备注
	if *word == "" && *file == "" {
		listNotes(m)
		return
	}

	changed := 0
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}
		var incoming map[string]string
		if err := jsonUnmarshal(data, &incoming); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			os.Exit(1)
		}
		for w, note := range incoming {
			p := m.Items[w]
			if p.Note != note {
				changed++
			}
			p.Note = note
			m.Items[w] = p
		}
	}

	if *word != "" {
		w := strings.TrimSpace(*word)
		if _, ok := m.Items[w]; !ok {
			// 词形没命中多半是括号/汉字写法对不上，给几个候选比直接报错有用
			fmt.Fprintf(os.Stderr, "Error: 词表里没有这个词：%s\n", w)
			if near := words.NearKeys(m.Items, w); len(near) > 0 {
				fmt.Fprintf(os.Stderr, "是不是想找：%s\n", strings.Join(near, " / "))
			}
			os.Exit(1)
		}
		p := m.Items[w]
		if p.Note != *text {
			changed++
		}
		p.Note = ""
		if !*clear {
			p.Note = strings.TrimSpace(*text)
		}
		m.Items[w] = p
	}

	if changed == 0 {
		outputResult(map[string]interface{}{
			"success": true, "command": "word-note", "changed": 0,
			"message": "备注没有变化，未上传",
		})
		return
	}

	m.Updated = time.Now().Format("2006-01-02")
	if err := storage.UploadWordMeta(ctx, m); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading: %v\n", err)
		os.Exit(1)
	}
	outputResult(map[string]interface{}{
		"success": true, "command": "word-note", "changed": changed,
		"total": len(m.Items), "key": storage.wordMetaKey(),
	})
}

// listNotes 列出现有备注（没备注的词不列，否则 900 多行没法看）。
func listNotes(m *WordMeta) {
	rows := make([]map[string]interface{}, 0)
	keys := make([]string, 0, len(m.Items))
	for k := range m.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := m.Items[k]
		if strings.TrimSpace(p.Note) == "" {
			continue
		}
		rows = append(rows, map[string]interface{}{
			"word": k, "pos": p.Pos, "sub": p.Sub, "note": p.Note,
		})
	}
	outputResult(map[string]interface{}{
		"success": true, "command": "word-note", "count": len(rows),
		"total": len(m.Items), "notes": rows,
	})
}
