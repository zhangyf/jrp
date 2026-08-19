package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

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

	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	w, _ := FindWord(arc.Groups, input.Word)
	if w == nil {
		fmt.Fprintf(os.Stderr, "Word not found: %s\n", input.Word)
		os.Exit(1)
	}

	// Guard against creating a duplicate entry.
	if existing, _ := FindWord(arc.Groups, input.NewWord); existing != nil && existing != w {
		fmt.Fprintf(os.Stderr, "Target form already exists in archive: %s\n", input.NewWord)
		os.Exit(1)
	}

	oldWord := w.Word
	w.Word = input.NewWord

	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, false)

	description := fmt.Sprintf("更新词形：%s → %s", oldWord, input.NewWord)
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)

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
