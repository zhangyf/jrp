package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

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
	inner := strings.TrimSpace(word[open+len("("):closeIdx])
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

	arc, err := ParseArchive(string(data), lang)
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

	totalWords := len(AllWords(arc.Groups))

	if *dryRun {
		outputResult(map[string]interface{}{
			"success":       true,
			"command":       "normalize-words",
			"dry_run":       true,
			"archive":       oldFilename,
			"total_words":   totalWords,
			"change_count":  len(changes),
			"changes":       changes,
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

	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	//20+ entries changed is a structural rewrite → major bump
	majorBump := len(changes) >= 20
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, majorBump)

	description := fmt.Sprintf("规范化词形：假名在外汉字在括号内（%d词）", len(changes))
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)

	// Sanity check: word count must not change
	newTotal := len(AllWords(arc.Groups))
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
