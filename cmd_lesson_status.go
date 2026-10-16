package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

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
	bank      *SentenceBank
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
		bank = &SentenceBank{}
	}
	s.bank = bank

	// 档案读不到不该拖垮其它几项，词数按 0 处理即可。
	if data, _, err := storage.DownloadLatestArchive(ctx); err == nil {
		if arc, err := ParseArchive(string(data), lang); err == nil {
			for _, w := range AllWords(arc.Groups) {
				if n := groupLesson(w.Group); n > 0 {
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
		if v := groupLesson(sen.Lesson); v > max {
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
		st.Section = hasLessonSection(c, lesson)
		st.Sentences = len(extractLessonRows(c, lesson))
	}
	return st
}

func (s *lessonStatusSource) report(lesson int) lessonStatusReport {
	r := lessonStatusReport{Lesson: lesson}
	label := lessonLabelOf(lesson)

	r.KnowledgeDoc = s.hasKnowledgeDoc(lesson, "知识点")

	// 单元与课的对应关系不固定（第3单元塞了7-12课），按内容定位而不是按算术。
	unit, basic, applied, found := resolveLessonDocs(s.fetchDoc, lesson)
	r.Unit = unit
	if r.Unit <= 0 {
		r.Unit = unitOfLesson(lesson) // 没定位到就显示按 4 课一单元猜的，至少不是 0
	}
	if found {
		r.BasicDoc = s.docStatus(basic, lesson)
		r.AppliedDoc = s.docStatus(applied, lesson)
	} else {
		r.BasicDoc = lessonDocStatus{Name: lessonDocName(r.Unit, lessonDocKinds[0])}
		r.AppliedDoc = lessonDocStatus{Name: lessonDocName(r.Unit, lessonDocKinds[1])}
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
