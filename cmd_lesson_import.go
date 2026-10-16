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

// ---------- 抽句子的共用部分（CLI 与 save-lesson 自动入库共用） ----------

// importDoc 一份要抽句子的汇编文档。
type importDoc struct {
	Name string
	Kind lessonDocKind
}

type importReport struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	RowsInDoc   int    `json:"rows_in_doc"`
	NewCount    int    `json:"new_count"`
	Skipped     int    `json:"skipped"`
	DownloadErr string `json:"download_error,omitempty"`
}

// resolveImportDocs 找第N课的两份汇编文档（基本课文 + 应用课文）。
// --doc 指定单份时只抽那一份，kind 按文档名里的「应用课文」判断。
func resolveImportDocs(fetch docFetcher, lesson int, onlyDoc string) (unit int, docs []importDoc, found bool) {
	if onlyDoc != "" {
		kind := lessonDocKinds[0]
		if strings.Contains(onlyDoc, "应用课文") {
			kind = lessonDocKinds[1]
		}
		return unitOfLesson(lesson), []importDoc{{Name: onlyDoc, Kind: kind}}, true
	}
	u, basic, applied, ok := resolveLessonDocs(fetch, lesson)
	if !ok {
		return unitOfLesson(lesson), nil, false
	}
	return u, []importDoc{
		{Name: basic, Kind: lessonDocKinds[0]},
		{Name: applied, Kind: lessonDocKinds[1]},
	}, true
}

// collectImport 从给定的文档里抽第 lesson 课的句子，只留句库里没有的。
// all 传进来时是句库现有全部条目，返回时已把新条目追加在后面（供连续导多课时查重）。
func collectImport(fetch docFetcher, all []BankSentence, lesson int, docs []importDoc) (newAll, added []BankSentence, reports []importReport) {
	for _, d := range docs {
		content, ok := fetch(d.Name)
		if !ok {
			reports = append(reports, importReport{
				Name: d.Name, Source: d.Kind.Source, DownloadErr: "下载失败",
			})
			continue
		}
		rows := extractLessonRows(content, lesson)
		plan := buildImportPlan(rows, lesson, d.Kind, d.Name, all)
		all = append(all, plan.Added...)
		added = append(added, plan.Added...)
		reports = append(reports, importReport{
			Name: d.Name, Source: d.Kind.Source,
			RowsInDoc: len(rows), NewCount: len(plan.Added), Skipped: plan.Skipped,
		})
	}
	return all, added, reports
}

// cosFetcher 按文档名从 COS 取内容，取过的缓存起来（导多课不会重复下载）。
// seed 可预置刚上传、还没落盘的内容（save-lesson 自动入库用）。
func cosFetcher(ctx context.Context, storage *Storage, seed map[string]string) docFetcher {
	cache := map[string]string{}
	for k, v := range seed {
		cache[k] = v
	}
	return func(name string) (string, bool) {
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
}

// autoImportLessons 把若干课一次性导进句库并写回 COS。save-lesson 用它，
// 所以「存完课文 → 句子自动进句库」是一条命令的事，不用再单独敲 sentence-import。
func autoImportLessons(ctx context.Context, storage *Storage, lessons []int, seed map[string]string) (added []BankSentence, perLesson []map[string]interface{}, err error) {
	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil || bank == nil {
		bank = &SentenceBank{Version: 1}
	}
	fetch := cosFetcher(ctx, storage, seed)

	all := make([]BankSentence, 0, len(bank.Sentences))
	all = append(all, bank.Sentences...)
	for _, ln := range lessons {
		_, docs, found := resolveImportDocs(fetch, ln, "")
		if !found {
			continue
		}
		var part []BankSentence
		var reports []importReport
		all, part, reports = collectImport(fetch, all, ln, docs)
		added = append(added, part...)
		perLesson = append(perLesson, map[string]interface{}{
			"lesson":    ln,
			"new_count": len(part),
			"docs":      reports,
		})
	}
	if len(added) == 0 {
		return nil, perLesson, nil
	}
	bank.Sentences = all
	bank.Updated = time.Now().Format("2006-01-02")
	if bank.Version <= 0 {
		bank.Version = 1
	}
	if err := storage.UploadSentenceBank(ctx, bank); err != nil {
		return added, perLesson, err
	}
	return added, perLesson, nil
}

// runSentenceImport 从「课本原文汇编」文档里自动抽某一课的课文句子，补进句库。
//
// 默认只干跑预览，--apply 才真的写回 COS。抽出来的句子一律是课本原文
// （生词表、"待补"占位一律跳过），写完可以跑 sentence-lint 复核。
// 平时不用手动敲 —— save-lesson 存汇编文档时会自动跑一遍。
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

	fetch := cosFetcher(ctx, storage, nil)
	unit, docs, found := resolveImportDocs(fetch, *lesson, *docName)
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

	all := make([]BankSentence, 0, len(bank.Sentences))
	all = append(all, bank.Sentences...)
	var added []BankSentence
	var reports []importReport
	all, added, reports = collectImport(fetch, all, *lesson, docs)

	out := map[string]interface{}{
		"success":     true,
		"command":     "sentence-import",
		"language":    lang,
		"lesson":      *lesson,
		"unit":        unit,
		"dry_run":     !*apply,
		"bank_before": len(bank.Sentences),
		"new_count":   len(added),
		"bank_after":  len(bank.Sentences) + len(added),
		"docs":        reports,
	}
	if len(added) > 0 {
		out["added"] = added
	}

	if *apply {
		if len(added) == 0 {
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
		out["next_step"] = "确认无误后加 --apply 写回句库"
	}
	outputResult(out)
}
