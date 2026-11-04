package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

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
