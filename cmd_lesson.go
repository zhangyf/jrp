// cmd_lesson.go —— 课文与批量导入导出 / 统计报表类命令
//
// 腿一 R1：把原本平铺在根目录的 7 个 cmd_*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；命令行为、输出、存档字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找命令实现先看下面的来源清单。
//
// 来源与顶层声明：
// cmd_lesson_import.go         importDoc, importReport, resolveImportDocs, collectImport, cosFetcher, autoImportLessons, runSentenceImport
// cmd_lesson_status.go         runLessonStatus, lessonDocStatus, bankStatus, lessonStatusReport, lessonStatusSource, newLessonStatusSource, fetchDoc, hasKnowledgeDoc, maxKnownLesson, docStatus, report
// cmd_save_lesson.go           runSaveLesson, lastSlash
// cmd_import.go                runImport
// cmd_export_hard.go           runExportHard
// cmd_lexicon.go               runDumpLexicon
// cmd_stats.go                 StatsResult, ComputeStats, runStats
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/lessonimport"
	"github.com/zhangyf/jrp/internal/lexicon"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
	"github.com/zhangyf/jrp/internal/stats"
)

// ==========================================================================
// 原 cmd_lesson_import.go
// ==========================================================================

// ---------- 抽句子的共用部分（CLI 与 save-lesson 自动入库共用） ----------

// importDoc 一份要抽句子的汇编文档。
type importDoc struct {
	Name string
	Kind lessonimport.LessonDocKind
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
func resolveImportDocs(fetch lessonimport.DocFetcher, lesson int, onlyDoc string) (unit int, docs []importDoc, found bool) {
	if onlyDoc != "" {
		kind := lessonimport.LessonDocKinds[0]
		if strings.Contains(onlyDoc, "应用课文") {
			kind = lessonimport.LessonDocKinds[1]
		}
		return lessonimport.UnitOfLesson(lesson), []importDoc{{Name: onlyDoc, Kind: kind}}, true
	}
	u, basic, applied, ok := lessonimport.ResolveLessonDocs(fetch, lesson)
	if !ok {
		return lessonimport.UnitOfLesson(lesson), nil, false
	}
	return u, []importDoc{
		{Name: basic, Kind: lessonimport.LessonDocKinds[0]},
		{Name: applied, Kind: lessonimport.LessonDocKinds[1]},
	}, true
}

// collectImport 从给定的文档里抽第 lesson 课的句子，只留句库里没有的。
// all 传进来时是句库现有全部条目，返回时已把新条目追加在后面（供连续导多课时查重）。
func collectImport(fetch lessonimport.DocFetcher, all []sentence.BankSentence, lesson int, docs []importDoc) (newAll, added []sentence.BankSentence, reports []importReport) {
	for _, d := range docs {
		content, ok := fetch(d.Name)
		if !ok {
			reports = append(reports, importReport{
				Name: d.Name, Source: d.Kind.Source, DownloadErr: "下载失败",
			})
			continue
		}
		rows := lessonimport.ExtractLessonRows(content, lesson)
		plan := lessonimport.BuildImportPlan(rows, lesson, d.Kind, d.Name, all)
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
func cosFetcher(ctx context.Context, storage *Storage, seed map[string]string) lessonimport.DocFetcher {
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
func autoImportLessons(ctx context.Context, storage *Storage, lessons []int, seed map[string]string) (added []sentence.BankSentence, perLesson []map[string]interface{}, err error) {
	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil || bank == nil {
		bank = &sentence.SentenceBank{Version: 1}
	}
	fetch := cosFetcher(ctx, storage, seed)

	all := make([]sentence.BankSentence, 0, len(bank.Sentences))
	all = append(all, bank.Sentences...)
	for _, ln := range lessons {
		_, docs, found := resolveImportDocs(fetch, ln, "")
		if !found {
			continue
		}
		var part []sentence.BankSentence
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
		bank = &sentence.SentenceBank{Version: 1}
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
			"note":      "没找到这一课的汇编文档（试过第1-" + strconv.Itoa(lessonimport.MaxUnit) + "单元），先 save-lesson 把课文存进 COS",
		})
		return
	}

	all := make([]sentence.BankSentence, 0, len(bank.Sentences))
	all = append(all, bank.Sentences...)
	var added []sentence.BankSentence
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

// ==========================================================================
// 原 cmd_lesson_status.go
// ==========================================================================

// runLessonStatus 一次查清某一课的「四件套」齐不齐：
//
//  1. 知识点文档（第N课知识点）
//  2. 课本原文汇编 · 基本课文（有第N课段落、能抽出几句）
//  3. 课本原文汇编 · 应用课文
//  4. 单词（档案里归在第N课分组下的词）
//  5. 句库（第N课有几句）
//
// 缺哪样就列在 missing 里并给出下一步该跑什么。
// 第19课（2026-10-16）就是前四样齐了、句库漏了，造句一直练不到那一课。
func runLessonStatus(fs *flag.FlagSet, lang string) {
	lesson := fs.Int("lesson", 0, "课号，如 19")
	all := fs.Bool("all", false, "扫描全部课号，列出哪些课不齐")
	_ = fs.Parse(cmdArgs)

	if *lesson <= 0 && !*all {
		fmt.Fprintf(os.Stderr, "Error: 需要 --lesson N 或 --all\n")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	src, err := newLessonStatusSource(ctx, storage, lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *all {
		upper := src.maxKnownLesson() + 1
		if upper < 1 {
			upper = 1
		}
		reports := make([]lessonStatusReport, 0, upper)
		notReady := []int{}
		for n := 1; n <= upper; n++ {
			r := src.report(n)
			reports = append(reports, r)
			if !r.Ready {
				notReady = append(notReady, n)
			}
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "lesson-status",
			"language":  lang,
			"scanned":   upper,
			"not_ready": notReady,
			"lessons":   reports,
		})
		return
	}

	r := src.report(*lesson)
	out := map[string]interface{}{
		"success":  true,
		"command":  "lesson-status",
		"language": lang,
		"lesson":   r.Lesson,
		"unit":     r.Unit,
		"ready":    r.Ready,
		"missing":  r.Missing,
		"checks":   r,
	}
	if r.NextStep != "" {
		out["next_step"] = r.NextStep
	}
	outputResult(out)
}

type lessonDocStatus struct {
	Name      string `json:"name"`
	Exists    bool   `json:"exists"`
	Section   bool   `json:"has_lesson_section"`
	Sentences int    `json:"sentences_in_doc"`
}

type bankStatus struct {
	Count     int            `json:"count"`
	BySource  map[string]int `json:"by_source,omitempty"`
	SampleIDs []string       `json:"sample_ids,omitempty"`
}

type lessonStatusReport struct {
	Lesson       int             `json:"lesson"`
	Unit         int             `json:"unit"`
	Ready        bool            `json:"ready"`
	Missing      []string        `json:"missing"`
	KnowledgeDoc string          `json:"knowledge_doc"`
	BasicDoc     lessonDocStatus `json:"basic_doc"`
	AppliedDoc   lessonDocStatus `json:"applied_doc"`
	Words        int             `json:"words_in_archive"`
	Bank         bankStatus      `json:"bank"`
	NextStep     string          `json:"next_step,omitempty"`
}

// lessonStatusSource 把 COS 上的东西按需下载并缓存，--all 扫多课时每份只读一次。
type lessonStatusSource struct {
	storage   *Storage
	ctx       context.Context
	lang      string
	knowledge []string          // COS 上所有知识点/汇编文档名
	docs      map[string]string // 文档名 → 内容（下载失败不进 map）
	bank      *sentence.SentenceBank
	words     map[int]int // 课号 → 档案里的词数
}

func newLessonStatusSource(ctx context.Context, storage *Storage, lang string) (*lessonStatusSource, error) {
	s := &lessonStatusSource{
		storage: storage,
		ctx:     ctx,
		lang:    lang,
		docs:    map[string]string{},
		words:   map[int]int{},
	}

	objs, err := storage.ListKnowledge(ctx)
	if err != nil {
		return nil, fmt.Errorf("列知识点文档失败：%w", err)
	}
	for _, o := range objs {
		s.knowledge = append(s.knowledge, filepath.Base(o.Key))
	}

	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil || bank == nil {
		bank = &sentence.SentenceBank{}
	}
	s.bank = bank

	// 档案读不到不该拖垮其它几项，词数按 0 处理即可。
	if data, _, err := storage.DownloadLatestArchive(ctx); err == nil {
		if arc, err := archive.ParseArchive(string(data), lang); err == nil {
			for _, w := range review.AllWords(arc.Groups) {
				if n := sentencereview.GroupLesson(w.Group); n > 0 {
					s.words[n]++
				}
			}
		}
	}
	return s, nil
}

// fetchDoc 按需下载并缓存汇编文档。
func (s *lessonStatusSource) fetchDoc(name string) (string, bool) {
	if c, ok := s.docs[name]; ok {
		return c, true
	}
	data, err := s.storage.DownloadKnowledge(s.ctx, name)
	if err != nil {
		return "", false
	}
	s.docs[name] = string(data)
	return s.docs[name], true
}

func (s *lessonStatusSource) hasKnowledgeDoc(lesson int, must string) string {
	want := fmt.Sprintf("第%d课", lesson)
	for _, n := range s.knowledge {
		if strings.Contains(n, want) && strings.Contains(n, must) {
			return n
		}
	}
	return ""
}

// lessonRe 从字符串里抠「第N课」的课号（知识点文档名、分组名通用）。
// 统计用例迁 internal/stats 后自带了私有副本，这里留给 lesson-status 用。
var lessonRe = regexp.MustCompile(`第(\d+)课`)

// maxKnownLesson 取知识点文档名 / 句库 / 档案里出现过的最大课号。
func (s *lessonStatusSource) maxKnownLesson() int {
	max := 0
	for _, n := range s.knowledge {
		if m := lessonRe.FindStringSubmatch(n); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil && v > max {
				max = v
			}
		}
	}
	for _, sen := range s.bank.Sentences {
		if v := sentencereview.GroupLesson(sen.Lesson); v > max {
			max = v
		}
	}
	for n := range s.words {
		if n > max {
			max = n
		}
	}
	return max
}

func (s *lessonStatusSource) docStatus(name string, lesson int) lessonDocStatus {
	st := lessonDocStatus{Name: name}
	if c, ok := s.fetchDoc(name); ok {
		st.Exists = true
		st.Section = lessonimport.HasLessonSection(c, lesson)
		st.Sentences = len(lessonimport.ExtractLessonRows(c, lesson))
	}
	return st
}

func (s *lessonStatusSource) report(lesson int) lessonStatusReport {
	r := lessonStatusReport{Lesson: lesson}
	label := lessonimport.LessonLabelOf(lesson)

	r.KnowledgeDoc = s.hasKnowledgeDoc(lesson, "知识点")

	// 单元与课的对应关系不固定（第3单元塞了7-12课），按内容定位而不是按算术。
	unit, basic, applied, found := lessonimport.ResolveLessonDocs(s.fetchDoc, lesson)
	r.Unit = unit
	if r.Unit <= 0 {
		r.Unit = lessonimport.UnitOfLesson(lesson) // 没定位到就显示按 4 课一单元猜的，至少不是 0
	}
	if found {
		r.BasicDoc = s.docStatus(basic, lesson)
		r.AppliedDoc = s.docStatus(applied, lesson)
	} else {
		r.BasicDoc = lessonDocStatus{Name: lessonimport.LessonDocName(r.Unit, lessonimport.LessonDocKinds[0])}
		r.AppliedDoc = lessonDocStatus{Name: lessonimport.LessonDocName(r.Unit, lessonimport.LessonDocKinds[1])}
	}
	r.Words = s.words[lesson]

	bySource := map[string]int{}
	var ids []string
	for _, sen := range s.bank.Sentences {
		if sen.Lesson != label {
			continue
		}
		src := sen.Source
		if src == "" {
			src = "未标"
		}
		bySource[src]++
		if len(ids) < 5 {
			ids = append(ids, sen.ID)
		}
	}
	total := 0
	for _, v := range bySource {
		total += v
	}
	sort.Strings(ids)
	r.Bank = bankStatus{Count: total, BySource: bySource, SampleIDs: ids}

	if r.KnowledgeDoc == "" {
		r.Missing = append(r.Missing, "知识点文档")
	}
	if !r.BasicDoc.Section {
		r.Missing = append(r.Missing, "汇编·基本课文")
	} else if r.BasicDoc.Sentences == 0 {
		r.Missing = append(r.Missing, "汇编·基本课文是待补")
	}
	if !r.AppliedDoc.Section {
		r.Missing = append(r.Missing, "汇编·应用课文")
	} else if r.AppliedDoc.Sentences == 0 {
		r.Missing = append(r.Missing, "汇编·应用课文是待补")
	}
	if r.Words == 0 {
		r.Missing = append(r.Missing, "单词未入库")
	}
	if r.Bank.Count == 0 {
		r.Missing = append(r.Missing, "句库未收录")
	}
	r.Ready = len(r.Missing) == 0

	switch {
	case r.Ready:
		r.NextStep = ""
	case r.BasicDoc.Section && r.Bank.Count == 0:
		r.NextStep = fmt.Sprintf("jrp --lang ja sentence-import --lesson %d --apply", lesson)
	case !r.BasicDoc.Section:
		r.NextStep = fmt.Sprintf("先补课文：把第%d课的原文整理成汇编文档再 save-lesson", lesson)
	default:
		r.NextStep = "照上面 missing 补齐全套"
	}
	return r
}

// ==========================================================================
// 原 cmd_save_lesson.go
// ==========================================================================

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
	if lessons := lessonimport.LessonSectionsIn(string(data)); len(lessons) > 0 {
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
	return strings.LastIndex(s, "/")
}

// ==========================================================================
// 原 cmd_import.go
// ==========================================================================

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
	arc, err := archive.ParseArchive(content, lang)
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

	totalWords := review.CountAllWords(arc.Groups)
	outputResult(map[string]interface{}{
		"success":    true,
		"command":    "import",
		"filename":   filename,
		"history":    *history,
		"word_count": totalWords,
		"groups":     len(arc.Groups),
	})
}

// ==========================================================================
// 原 cmd_export_hard.go
// ==========================================================================

// 钉子户分区常量。值与分区顺序的唯一来源在 internal/offline
// （渲染侧同包持有 hardCategories），根包只留别名给 runExportHard
// 的分区计数 switch 用，避免两份字面量各写一遍。
const (
	HardSevere   = offline.HardSevere
	HardModerate = offline.HardModerate
	HardMild     = offline.HardMild
)

// BuildHardPlan / hardSeverity 已迁 internal/plan（P2），
// CLI export-hard 与 HTTP /api/hard、/api/export 共用同一份实现。

func runExportHard(fs *flag.FlagSet, lang string) {
	outputPath := fs.String("output", "", "Output Excel file path (default: outputs/hard_words_<date>_vA.B.xlsx)")
	minAcc := fs.Float64("min-accuracy", review.DefaultHardMinAccuracy, "Accuracy threshold (0-1); words below this are hard words")
	minRev := fs.Int("min-reviews", review.DefaultHardMinReviews, "Minimum review count required to qualify as a hard word")
	dateFlag := fs.String("date", "", "Target date used in the output filename (YYYY-MM-DD). Defaults to today.")
	fs.Parse(cmdArgs)

	if *minAcc <= 0 || *minAcc >= 1 {
		fmt.Fprintf(os.Stderr, "Error: --min-accuracy must be between 0 and 1 (exclusive), got %v\n", *minAcc)
		os.Exit(1)
	}
	if *minRev < 1 {
		fmt.Fprintf(os.Stderr, "Error: --min-reviews must be >= 1, got %d\n", *minRev)
		os.Exit(1)
	}

	// Determine target date
	var targetDate time.Time
	var err error
	if *dateFlag != "" {
		targetDate, err = time.Parse("2006-01-02", *dateFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing --date (expected YYYY-MM-DD): %v\n", err)
			os.Exit(1)
		}
	} else {
		targetDate = time.Now()
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Download latest archive
	data, archiveFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading latest archive: %v\n", err)
		os.Exit(1)
	}

	// Parse version from the archive filename for output naming.
	//
	// Unlike gen-plan, this command does NOT initialize a new-day archive: it is
	// a read-only query, and merely looking at the hard-word list should not bump
	// the archive version. As a result the version here may still refer to the
	// previous day's archive, so archive_filename is reported for traceability.
	arcMajor, arcMinor := 1, 0
	if _, maj, min, perr := archive.ParseFilename(archiveFilename); perr == nil {
		arcMajor, arcMinor = maj, min
	}

	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	dateStr := targetDate.Format("2006-01-02")
	plan := plan.BuildHardPlan(arc, lang, targetDate, *minAcc, *minRev)

	if len(plan.Words) == 0 {
		outputResult(map[string]interface{}{
			"success":          true,
			"command":          "export-hard",
			"date":             dateStr,
			"language":         lang,
			"min_accuracy":     *minAcc,
			"min_reviews":      *minRev,
			"hard_count":       0,
			"archive_filename": archiveFilename,
			"message":          "No hard words found under the given thresholds",
		})
		return
	}

	// Count severity buckets
	severe, moderate, mild := 0, 0, 0
	for _, w := range plan.Words {
		switch w.Status {
		case HardSevere:
			severe++
		case HardModerate:
			moderate++
		case HardMild:
			mild++
		}
	}

	if *outputPath == "" {
		*outputPath = fmt.Sprintf("outputs/hard_words_%s_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
	}
	if dir := filepath.Dir(*outputPath); dir != "" {
		os.MkdirAll(dir, 0755)
	}

	if err := offline.GenerateHardExcel(plan, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating Excel: %v\n", err)
		os.Exit(1)
	}

	// Upload the plan JSON so that `record --hard` can resolve word numbers.
	if err := storage.UploadHardPlan(ctx, plan); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload hard plan: %v\n", err)
	}

	// Upload Excel to COS (backup)
	if err := storage.UploadHardExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
	}

	outputResult(map[string]interface{}{
		"success":          true,
		"command":          "export-hard",
		"date":             dateStr,
		"language":         lang,
		"min_accuracy":     *minAcc,
		"min_reviews":      *minRev,
		"hard_count":       len(plan.Words),
		"severe_count":     severe,
		"moderate_count":   moderate,
		"mild_count":       mild,
		"excel_path":       *outputPath,
		"plan_key":         fmt.Sprintf("plans/hard_%s.json", dateStr),
		"archive_filename": archiveFilename,
		"version":          fmt.Sprintf("v%d.%d", arcMajor, arcMinor),
		"plan_words":       plan.Words,
	})
}

// ==========================================================================
// 原 cmd_lexicon.go
// ==========================================================================

// runDumpLexicon 导出词汇总表（跟 GET /api/lexicon 同一份数据、同一个拼装函数），
// 只是从命令行直接拿，不用过鉴权 —— 在服务器上核对词库时省一步登录。
// ⛔ 只读：不写档案、不写 word_meta。
func runDumpLexicon(fs *flag.FlagSet, lang string) {
	outPath := fs.String("output", "", "输出文件路径（默认打印到 stdout）")
	fs.Parse(cmdArgs)

	st, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	arc, err := st.LoadArchiveForRead(ctx, lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading archive: %v\n", err)
		os.Exit(1)
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}
	if meta.Items == nil {
		meta.Items = map[string]WordPOS{}
	}

	items, posSummary, groups := lexicon.BuildLexicon(arc, meta)

	payload := map[string]interface{}{
		"success":      true,
		"count":        len(items),
		"items":        items,
		"pos_summary":  posSummary,
		"groups":       groups,
		"meta_note":    meta.Note,
		"meta_updated": meta.Updated,
		"dry_run":      st.dryRun,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", *outPath, err)
			os.Exit(1)
		}
		defer f.Close()
		enc = json.NewEncoder(f)
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding: %v\n", err)
		os.Exit(1)
	}
}

// BuildLexicon 已迁 internal/lexicon（P2），CLI dump-lexicon 与
// GET /api/lexicon 共用同一份实现。

// ==========================================================================
// 原 cmd_stats.go
// ==========================================================================

// StatsResult 统计结果类型已迁 internal/stats，根包留别名 ——
// runStats 与 api_stats.go 继续用这个名字，JSON 序列化形态不变。
type StatsResult = stats.StatsResult

// 编译期断言：*Storage 满足统计用例的最小依赖面（ListArchiveRefs + ReadArchiveBlob）。
var _ stats.StatsArchiveReader = (*Storage)(nil)

// ComputeStats 统计最近 days 天。CLI 与 HTTP handler 共用，两边都只读。
// 用例已迁 internal/stats；这里只注入 now 时钟后转发。
func ComputeStats(ctx context.Context, storage *Storage, lang string, days int) (*StatsResult, error) {
	return stats.ComputeStats(ctx, storage, lang, days, time.Now())
}

func runStats(fs *flag.FlagSet, lang string) {
	days := fs.Int("days", 7, "Number of days to look back")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	res, err := ComputeStats(ctx, storage, lang, *days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error computing stats: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "stats",
		"language":  lang,
		"days":      *days,
		"snapshots": res.Snapshots,
		"changes":   res.Changes,
		"detail":    res.Detail,
		// 与 /api/stats 对齐（api_stats.go 里也带 jlpt）。非日语为 nil，序列化成 null。
		"jlpt": res.Jlpt,
	})
}
