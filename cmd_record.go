package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func runRecord(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with review results (default: stdin)")
	fs.Parse(cmdArgs)

	// Read input
	var input RecordInput
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

	// Download the review plan
	plan, err := storage.DownloadPlan(ctx, input.PlanDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading plan for date %s: %v\n", input.PlanDate, err)
		os.Exit(1)
	}

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

	// Build number → word mapping from plan
	planMap := make(map[int]string) // number → word text
	for _, pw := range plan.Words {
		planMap[pw.Number] = pw.Word
	}

	// Apply results
	today := time.Now()
	correctCount := 0
	wrongCount := 0
	notFound := 0

	for _, r := range input.WordResults {
		wordText, ok := planMap[r.Number]
		if !ok {
			notFound++
			continue
		}

		w, _ := FindWord(arc.Groups, wordText)
		if w == nil {
			notFound++
			continue
		}

		if r.Correct {
			RecordCorrect(w, today)
			correctCount++
		} else {
			RecordWrong(w, today)
			wrongCount++
		}
	}

	// Calculate version
	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, false)

	// Add changelog entry
	description := fmt.Sprintf("复习结果：%d词写对，%d词写错", correctCount, wrongCount)
	if notFound > 0 {
		description += fmt.Sprintf("（%d词未找到）", notFound)
	}
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	// Write and upload archive
	newContent := WriteArchive(arc)
	newFilename := ArchiveFilename(lang, today, newMajor, newMinor)

	if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading archive: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":      true,
		"command":      "record",
		"plan_date":    input.PlanDate,
		"correct":      correctCount,
		"wrong":        wrongCount,
		"not_found":    notFound,
		"old_filename": oldFilename,
		"new_filename": newFilename,
		"version":      fmt.Sprintf("v%d.%d", newMajor, newMinor),
		"total_words":  CountAllWords(arc.Groups),
	})
}
