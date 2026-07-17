package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

func runImport(fs *flag.FlagSet, lang string) {
	history := fs.Bool("history", false, "Import as historical archive (not current)")
	fs.Parse(cmdArgs)

	// Read archive content from stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	if len(data) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no input data received on stdin")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Try to extract filename from content or use a default
	// The archive content might have a title we can use
	content := string(data)
	arc, err := ParseArchive(content, lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// Generate filename from the latest changelog entry or today's date
	var filename string
	if len(arc.Changelog) > 0 {
		latest := arc.Changelog[len(arc.Changelog)-1]
		filename = fmt.Sprintf("%s_%s_%s.md", LangConfigs[lang].FilePrefix, latest.Date, latest.Version)
	} else {
		filename = fmt.Sprintf("%s_imported.md", LangConfigs[lang].FilePrefix)
	}

	if *history {
		err = storage.UploadHistory(ctx, filename, data)
	} else {
		err = storage.UploadArchive(ctx, filename, data)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading: %v\n", err)
		os.Exit(1)
	}

	totalWords := CountAllWords(arc.Groups)
	outputResult(map[string]interface{}{
		"success":    true,
		"command":    "import",
		"filename":   filename,
		"history":    *history,
		"word_count": totalWords,
		"groups":     len(arc.Groups),
	})
}
