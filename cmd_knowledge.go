package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// runListKnowledge lists all knowledge documents stored in COS.
func runListKnowledge(fs *flag.FlagSet, lang string) {
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	objs, err := storage.ListKnowledge(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing knowledge documents: %v\n", err)
		os.Exit(1)
	}

	type knowledgeItem struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var items []knowledgeItem
	for _, obj := range objs {
		items = append(items, knowledgeItem{
			Name: filepath.Base(obj.Key),
			Size: obj.Size,
		})
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "list-knowledge",
		"language":  lang,
		"count":     len(items),
		"knowledge": items,
	})
}

// runGetKnowledge downloads and prints a specific knowledge document from COS.
func runGetKnowledge(fs *flag.FlagSet, lang string) {
	name := fs.String("name", "", "Knowledge document filename to retrieve")
	fs.Parse(cmdArgs)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	data, err := storage.DownloadKnowledge(ctx, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading knowledge document: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"command":  "get-knowledge",
		"language": lang,
		"name":     *name,
		"size":     len(data),
		"content":  string(data),
	})
}
