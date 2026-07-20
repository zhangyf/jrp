package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

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

	ctx := context.Background()

	// Download latest archive
	data, oldFilename, err := storage.DownloadLatestArchive(ctx)
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

	// Find or create group
	var targetGroup *WordGroup
	for i := range arc.Groups {
		if arc.Groups[i].Title == input.Group {
			targetGroup = &arc.Groups[i]
			break
		}
	}
	if targetGroup == nil {
		arc.Groups = append(arc.Groups, WordGroup{Title: input.Group})
		targetGroup = &arc.Groups[len(arc.Groups)-1]
	}

	// Pre-dedupe within input list (defensive — keeps semantics explicit,
	// not relying on FindWord seeing newly-appended entries). Keep first
	// occurrence's definition when the same word appears more than once.
	seen := make(map[string]bool, len(input.Words))
	deduped := make([]struct {
		Word       string `json:"word"`
		Definition string `json:"definition"`
	}, 0, len(input.Words))
	for _, w := range input.Words {
		if seen[w.Word] {
			continue
		}
		seen[w.Word] = true
		deduped = append(deduped, w)
	}
	input.Words = deduped

	// Add words (skip duplicates already in archive)
	added := 0
	duplicates := 0
	for _, w := range input.Words {
		// Check if word already exists
		if existing, _ := FindWord(arc.Groups, w.Word); existing != nil {
			duplicates++
			continue
		}
		targetGroup.Words = append(targetGroup.Words, Word{
			Word:       w.Word,
			Definition: w.Definition,
			Status:     "🔄待测试",
		})
		added++
	}

	// Determine if major bump is needed (20+ new words)
	needMajorBump := *majorBump || added >= 20

	// Calculate version
	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, needMajorBump)

	// Add changelog entry
	description := fmt.Sprintf("添加%d词", added)
	if duplicates > 0 {
		description += fmt.Sprintf("（跳过%d重复词）", duplicates)
	}
	if needMajorBump && !*majorBump {
		description += "；批量导入触发大版本"
	}
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	// Write archive
	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)

	// Upload
	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":       true,
		"command":       "add-words",
		"added":         added,
		"duplicates":    duplicates,
		"old_filename":  oldFilename,
		"new_filename":  newFilename,
		"version":       fmt.Sprintf("v%d.%d", newMajor, newMinor),
		"major_bump":    needMajorBump,
		"total_words":   CountAllWords(arc.Groups),
	})
}
