package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// runSentenceImport 从「课本原文汇编」文档里自动抽某一课的课文句子，补进句库。
//
// 默认只干跑预览，--apply 才真的写回 COS。抽出来的句子一律是课本原文
// （生词表、"待补"占位一律跳过），写完可以跑 sentence-lint 复核。
//
//	jrp --lang ja sentence-import --lesson 19
//	jrp --lang ja sentence-import --lesson 19 --apply
func runSentenceImport(fs *flag.FlagSet, lang string) {
	lesson := fs.Int("lesson", 0, "课号，如 19")
	apply := fs.Bool("apply", false, "真的写回句库（默认只干跑预览）")
	docName := fs.String("doc", "", "自定义汇编文档名（默认按课号自动找第N单元的两份）")
	_ = fs.Parse(cmdArgs)

	if *lesson <= 0 && *docName == "" {
		fmt.Fprintf(os.Stderr, "Error: 需要 --lesson N（或 --doc 指定单份文档）\n")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil || bank == nil {
		// 还没建库：按空库处理，第一次导入顺便把库建出来。
		bank = &SentenceBank{Version: 1}
	}

	// 下载过的文档缓存起来，两种文档不会重复下载。
	cache := map[string]string{}
	fetch := func(name string) (string, bool) {
		if c, ok := cache[name]; ok {
			return c, true
		}
		data, err := storage.DownloadKnowledge(ctx, name)
		if err != nil {
			return "", false
		}
		cache[name] = string(data)
		return cache[name], true
	}

	unit := unitOfLesson(*lesson)
	docs := []struct {
		Name string
		Kind lessonDocKind
	}{}
	if *docName != "" {
		kind := lessonDocKinds[0]
		if strings.Contains(*docName, "应用课文") {
			kind = lessonDocKinds[1]
		}
		docs = append(docs, struct {
			Name string
			Kind lessonDocKind
		}{*docName, kind})
	} else {
		u, basic, applied, found := resolveLessonDocs(fetch, *lesson)
		if !found {
			outputResult(map[string]interface{}{
				"success":   true,
				"command":   "sentence-import",
				"language":  lang,
				"lesson":    *lesson,
				"new_count": 0,
				"note":      "没找到这一课的汇编文档（试过第1-" + strconv.Itoa(maxUnit) + "单元），先 save-lesson 把课文存进 COS",
			})
			return
		}
		unit = u
		docs = append(docs,
			struct {
				Name string
				Kind lessonDocKind
			}{basic, lessonDocKinds[0]},
			struct {
				Name string
				Kind lessonDocKind
			}{applied, lessonDocKinds[1]},
		)
	}

	all := make([]BankSentence, 0)
	for _, s := range bank.Sentences {
		all = append(all, s)
	}

	type docReport struct {
		Name        string   `json:"name"`
		Source      string   `json:"source"`
		RowsInDoc   int      `json:"rows_in_doc"`
		NewCount    int      `json:"new_count"`
		UpdateCount int      `json:"update_count"`
		Skipped     int      `json:"skipped"`
		Orphans     []string `json:"orphans,omitempty"`
		DownloadErr string   `json:"download_error,omitempty"`
	}
	var reports []docReport
	var added []BankSentence
	var updated []bankEntryUpdate
	missingDoc := 0

	for _, d := range docs {
		content, ok := fetch(d.Name)
		if !ok {
			missingDoc++
			reports = append(reports, docReport{
				Name: d.Name, Source: d.Kind.Source, DownloadErr: "下载失败",
			})
			continue
		}
		rows := extractLessonRows(content, *lesson)
		plan := buildImportPlan(rows, *lesson, d.Kind, d.Name, all)
		for _, u := range plan.Updated {
			all[u.Index] = u.Entry
		}
		all = append(all, plan.Added...)
		added = append(added, plan.Added...)
		updated = append(updated, plan.Updated...)
		reports = append(reports, docReport{
			Name: d.Name, Source: d.Kind.Source,
			RowsInDoc: len(rows), NewCount: len(plan.Added),
			UpdateCount: len(plan.Updated), Skipped: plan.Skipped,
			Orphans: plan.Orphans,
		})
	}

	out := map[string]interface{}{
		"success":      true,
		"command":      "sentence-import",
		"language":     lang,
		"lesson":       *lesson,
		"unit":         unit,
		"dry_run":      !*apply,
		"bank_before":  len(bank.Sentences),
		"new_count":    len(added),
		"update_count": len(updated),
		"bank_after":   len(bank.Sentences) + len(added),
		"docs":         reports,
	}
	if len(added) > 0 {
		out["added"] = added
	}
	if len(updated) > 0 {
		out["updated"] = updated
	}

	if *apply {
		if len(added) == 0 && len(updated) == 0 {
			out["note"] = "句库里已是最新，未改动"
		} else {
			bank.Sentences = all
			bank.Updated = time.Now().Format("2006-01-02")
			if bank.Version <= 0 {
				bank.Version = 1
			}
			if err := storage.UploadSentenceBank(ctx, bank); err != nil {
				fmt.Fprintf(os.Stderr, "Error: 写回句库失败：%v\n", err)
				os.Exit(1)
			}
			out["uploaded"] = len(bank.Sentences)
			out["next_step"] = "跑 jrp --lang ja sentence-lint 复核是否都是课本原文"
		}
	} else {
		switch {
		case missingDoc == len(docs):
			out["next_step"] = "两份汇编文档都没找到：先 save-lesson 把课文存进 COS"
		case len(updated) > 0:
			out["next_step"] = "上面 updated 是「文档改过、句库要跟着改」的条目，看一眼 old→new 再加 --apply"
		default:
			out["next_step"] = "确认无误后加 --apply 写回句库"
		}
	}
	outputResult(out)
}
