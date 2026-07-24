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

	result, err := ApplyRecord(context.Background(), storage, lang, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	outputResult(result)
}

// RecordResultSummary is the outcome of applying review results to the archive.
type RecordResultSummary struct {
	Success     bool   `json:"success"`
	Command     string `json:"command"`
	PlanDate    string `json:"plan_date"`
	Correct     int    `json:"correct"`
	Wrong       int    `json:"wrong"`
	NotFound    int    `json:"not_found"`
	OldFilename string `json:"old_filename"`
	NewFilename string `json:"new_filename"`
	Version     string `json:"version"`
	TotalWords  int    `json:"total_words"`
}

// ApplyRecord downloads the plan + latest archive, applies word/sentence
// results, bumps the version, and uploads the updated archive to COS. It is a
// pure function reused by both the CLI (runRecord) and the HTTP server.
func ApplyRecord(ctx context.Context, storage *Storage, lang string, input RecordInput) (*RecordResultSummary, error) {
	// Download the review plan
	plan, err := storage.DownloadPlan(ctx, input.PlanDate)
	if err != nil {
		return nil, fmt.Errorf("downloading plan for date %s: %w", input.PlanDate, err)
	}

	// Download latest archive
	data, oldFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		return nil, fmt.Errorf("downloading latest archive: %w", err)
	}

	// Parse archive
	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		return nil, fmt.Errorf("parsing archive: %w", err)
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
		return nil, fmt.Errorf("uploading archive: %w", err)
	}

	return &RecordResultSummary{
		Success:     true,
		Command:     "record",
		PlanDate:    input.PlanDate,
		Correct:     correctCount,
		Wrong:       wrongCount,
		NotFound:    notFound,
		OldFilename: oldFilename,
		NewFilename: newFilename,
		Version:     fmt.Sprintf("v%d.%d", newMajor, newMinor),
		TotalWords:  CountAllWords(arc.Groups),
	}, nil
}
