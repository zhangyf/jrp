package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

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
	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// Find the word and update
	w, _ := FindWord(arc.Groups, input.Word)
	if w == nil {
		fmt.Fprintf(os.Stderr, "Word not found: %s\n", input.Word)
		os.Exit(1)
	}

	oldDef := w.Definition
	w.Definition = input.Definition

	// Calculate version
	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, false)

	// Add changelog entry
	description := fmt.Sprintf("更新释义：%s（%s → %s）", input.Word, oldDef, input.Definition)
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	// Write and upload
	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)

	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "update-def",
		"word":         input.Word,
		"old_def":      oldDef,
		"new_def":      input.Definition,
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
	})
}
