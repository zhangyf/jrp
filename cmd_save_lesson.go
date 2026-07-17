package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func runSaveLesson(fs *flag.FlagSet, lang string) {
	filePath := fs.String("file", "", "Local file to upload as knowledge document")
	name := fs.String("name", "", "Remote filename for the knowledge document")
	fs.Parse(cmdArgs)

	if *filePath == "" {
		fmt.Fprintln(os.Stderr, "Error: --file is required")
		os.Exit(1)
	}
	if *name == "" {
		// Use the local filename if --name not specified
		*name = *filePath
		if idx := lastSlash(*filePath); idx >= 0 {
			*name = (*filePath)[idx+1:]
		}
	}

	data, err := os.ReadFile(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	if err := storage.UploadKnowledge(ctx, *name, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading knowledge document: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"command":  "save-lesson",
		"language": lang,
		"name":     *name,
		"size":     len(data),
	})
}

func lastSlash(s string) int {
	return lastIndexOf(s, "/")
}
