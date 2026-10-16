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
	// 汇编文档 = 课文句子入口，存完直接把里面每一课的句子导进句库（查重后只加没有的）。
	// 第19课（2026-10-16）就是存了汇编却没进句库，造句一直练不到那一课 ——
	// 这条自动链路就是为了不再靠人记得补这一步。
	if lessons := lessonSectionsIn(string(data)); len(lessons) > 0 {
		out["lessons_in_doc"] = lessons
		added, perLesson, err := autoImportLessons(ctx, storage, lessons, map[string]string{*name: string(data)})
		if err != nil {
			out["auto_import_error"] = err.Error()
			out["next_step"] = "自动入库失败，手动补：jrp --lang " + lang + " sentence-import --lesson N --apply"
		} else {
			out["auto_import"] = map[string]interface{}{
				"new_count":  len(added),
				"per_lesson": perLesson,
			}
			if len(added) > 0 {
				out["added"] = added
				out["next_step"] = fmt.Sprintf("已入库 %d 句，可跑 sentence-lint 复核是否都是课本原文", len(added))
			} else {
				out["next_step"] = "这些课的句子句库里都有了，未改动"
			}
		}
	}
	outputResult(out)
}

func lastSlash(s string) int {
	return lastIndexOf(s, "/")
}
