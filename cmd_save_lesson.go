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

	out := map[string]interface{}{
		"success":  true,
		"command":  "save-lesson",
		"language": lang,
		"name":     *name,
		"size":     len(data),
	}
	// 汇编文档 = 课文句子入口。存完就提醒把这些课的句子补进句库 ——
	// 第19课（2026-10-16）就是存了汇编却没进句库，造句一直练不到。
	if lessons := lessonSectionsIn(string(data)); len(lessons) > 0 {
		out["lessons_in_doc"] = lessons
		out["next_step"] = fmt.Sprintf("把这些课的句子补进句库：jrp --lang %s sentence-import --lesson %d --apply"+
			"（文档里共 %d 课：%v），然后跑 sentence-lint", lang, lessons[len(lessons)-1], len(lessons), lessons)
	}
	outputResult(out)
}

func lastSlash(s string) int {
	return lastIndexOf(s, "/")
}
