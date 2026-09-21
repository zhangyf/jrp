package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 造句练习：句库 + 轮换。
//
// 目标是把「每天喊 AI 挑 20 句」变成一次性建库、日常由 Go 出题。AI 只在建库和
// 每学一课追加时介入（低频），其余时间 /api/plan?mode=sentences 自己就能出句。
//
// 轮换规则沿用 SKILL.md 第 298-333 行：
//   - 错句（status=open 且 next_due<=今天）强制进入当天，不受 7 天去重限制
//   - 同一句 7 天内不重复出（按归一化比对）
//   - 按课轮转，跨课摊平
//   - 变形句（type=transformed）不超过总数 1/5
//
// sentence_history.json / sentence_wrong.json 是 AI 手工流程在用的既有文件，
// 这里直接接管、schema 不变，既有数据零迁移。

// ---------- 句库 ----------

// SentenceBank 课本原文句库。answer 必须是课本原文，不可以是 AI 编的句子 ——
// sentence-lint 是这条约束的唯一防线。
type SentenceBank struct {
	Version   int            `json:"version"`
	Updated   string         `json:"updated"`
	Sentences []BankSentence `json:"sentences"`
}

// BankSentence 句库里的一条句子。
//
// Type:
//   - original    课本原文，照抄
//   - transformed 同骨架换词的变形句，Prototype 指回原句 id
type BankSentence struct {
	ID        string `json:"id"`
	Lesson    string `json:"lesson"`
	Source    string `json:"source"`     // 基本课文 / 应用课文
	SourceDoc string `json:"source_doc"` // 出处文档名，供 sentence-lint 校验原文
	Type      string `json:"type"`
	Prototype string `json:"prototype"`
	Answer    string `json:"answer"`
	Chinese   string `json:"chinese"`
}

// ---------- 轮换状态 ----------

// SentenceHistory 按日期记录每天出过的 answer 原文。
type SentenceHistory map[string][]string

// SentenceWrong 错句间隔重复状态。
type SentenceWrong struct {
	Sentences []WrongSentence `json:"sentences"`
}

// WrongSentence 一条错句记录。
type WrongSentence struct {
	Answer     string `json:"answer"`
	Chinese    string `json:"chinese"`
	Status     string `json:"status"` // open | archived
	NextDue    string `json:"next_due"`
	LastSeen   string `json:"last_seen"`
	LastWrong  string `json:"last_wrong"`
	WrongCount int    `json:"wrong_count"`
}

// SentenceResult 一道造句题的作答结果。
type SentenceResult struct {
	Number  int    `json:"number"`
	Correct bool   `json:"correct"`
	Answer  string `json:"answer"`  // 原句 answer，用于定位 wrong 条目
	Chinese string `json:"chinese"` // 中文提示，新错句入表时带上
}

// ---------- COS key ----------

func (s *Storage) sentenceBankKey() string    { return s.cosPrefix() + "/plans/sentence_bank.json" }
func (s *Storage) sentenceHistoryKey() string { return s.cosPrefix() + "/plans/sentence_history.json" }
func (s *Storage) sentenceWrongKey() string   { return s.cosPrefix() + "/plans/sentence_wrong.json" }

// ---------- 归一化 ----------

// 注意：Go 的 \s 只认 ASCII 空白，全角空格 U+3000 必须单独列 —— SKILL.md 的
// norm 规则是 Python 的 re.sub，Python 的 \s 是 Unicode-aware 的，会吃掉全角空格。
var sentenceNormRe = regexp.MustCompile(`[\s　。．、,./／!?:;]`)

// foldWidth 把全角 ASCII 形式（U+FF01～U+FF5E：全角数字、全角字母、
// 全角标点）折成对应半角。全角空格 U+3000 不在此区间，由正则单独删。
func foldWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		b.WriteRune(r)
	}
	return b.String()
}

// normSentence 造句归一化：先折全角为半角，再删掉所有空白（含全角空格）、
// 句号（。．）、读点（、）、逗号（,.）、斜杠（/／）、感叹号问号分号。
//
// 与 SKILL.md 第 321 行的 norm 规则一致，额外多剥斜杠：历史错句里有
// 「甲／乙」这类成对句（两个半句合成一题），全角半角混用很常见，
// 判分时应当视作同一句。
//
// 宽度折叠是 2026-09-21 加的：老师用日语 IME 打「２万円」（全角２），
// 原句是半角「2」，肉眼是同一个句子，不该判错。
//
// 历史上有同一句被存成带句号和不带句号两个版本的情况，所以任何涉及"这句出过没有"
// 的判断都必须先归一化，不能按原字符串比。
func normSentence(s string) string {
	return sentenceNormRe.ReplaceAllString(foldWidth(s), "")
}

// ---------- 读取（对既有文件做容错解析） ----------

// loadSentenceHistory 解析 sentence_history.json。
// 顶层可能是 {"2026-09-03": [...]}，也可能被包在 {"dates": ...} / {"history": ...} 里。
func loadSentenceHistory(data []byte) (SentenceHistory, error) {
	var m SentenceHistory
	if err := json.Unmarshal(data, &m); err == nil {
		return m, nil
	}
	for _, wrapper := range []string{"dates", "history", "records"} {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		if inner, ok := raw[wrapper]; ok {
			var h SentenceHistory
			if err := json.Unmarshal(inner, &h); err == nil {
				return h, nil
			}
		}
	}
	return SentenceHistory{}, fmt.Errorf("无法解析 sentence_history.json（前 %d 字节）：%s",
		min(len(data), 200), string(data[:min(len(data), 200)]))
}

// loadSentenceWrong 解析 sentence_wrong.json。顶层可能是数组，也可能是 {"sentences": [...]}。
func loadSentenceWrong(data []byte) (*SentenceWrong, error) {
	var arr []WrongSentence
	if err := json.Unmarshal(data, &arr); err == nil {
		return &SentenceWrong{Sentences: arr}, nil
	}
	var obj SentenceWrong
	if err := json.Unmarshal(data, &obj); err == nil && len(obj.Sentences) > 0 {
		return &obj, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		for _, k := range []string{"sentences", "items", "wrong", "wrong_sentences"} {
			if inner, ok := raw[k]; ok {
				var s []WrongSentence
				if err := json.Unmarshal(inner, &s); err == nil {
					return &SentenceWrong{Sentences: s}, nil
				}
			}
		}
	}
	return &SentenceWrong{}, fmt.Errorf("无法解析 sentence_wrong.json（前 %d 字节）：%s",
		min(len(data), 200), string(data[:min(len(data), 200)]))
}

// ---------- Storage 层读写 ----------

func (s *Storage) DownloadSentenceBank(ctx context.Context) (*SentenceBank, error) {
	data, err := s.store.GetAll(ctx, s.sentenceBankKey())
	if err != nil {
		return nil, err
	}
	var b SentenceBank
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("解析句库失败：%w", err)
	}
	return &b, nil
}

func (s *Storage) UploadSentenceBank(ctx context.Context, b *SentenceBank) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sentenceBankKey(), []byte(toJSON(b)))
}

func (s *Storage) DownloadSentenceHistory(ctx context.Context) (SentenceHistory, error) {
	data, err := s.store.GetAll(ctx, s.sentenceHistoryKey())
	if err != nil {
		return SentenceHistory{}, nil // 还没建过，视为空
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return SentenceHistory{}, nil
	}
	h, err := loadSentenceHistory(data)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (s *Storage) DownloadSentenceWrong(ctx context.Context) (*SentenceWrong, error) {
	data, err := s.store.GetAll(ctx, s.sentenceWrongKey())
	if err != nil {
		return &SentenceWrong{}, nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &SentenceWrong{}, nil
	}
	return loadSentenceWrong(data)
}

func (s *Storage) UploadSentenceHistory(ctx context.Context, h SentenceHistory) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sentenceHistoryKey(), []byte(toJSON(h)))
}

func (s *Storage) UploadSentenceWrong(ctx context.Context, w *SentenceWrong) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sentenceWrongKey(), []byte(toJSON(w)))
}

// ---------- 出题 ----------

// sentenceCand 一个候选句，带"上次出过的时间"用于「最久没出过」排序。
type sentenceCand struct {
	s    BankSentence
	last time.Time // 零值 = 从没出过
}

// BuildSentencePlan 挑出当天的造句题。纯函数，不碰 COS，方便单测。
//
// count: 普通模式 20，纯句子模式 50。
func BuildSentencePlan(bank *SentenceBank, hist SentenceHistory, wrong *SentenceWrong,
	today time.Time, count int) []PlanSentence {

	if bank == nil || count <= 0 {
		return nil
	}

	var picked []BankSentence
	used := make(map[string]bool) // norm -> 已选

	add := func(s BankSentence) bool {
		n := normSentence(s.Answer)
		if n == "" || used[n] {
			return false
		}
		used[n] = true
		picked = append(picked, s)
		return true
	}

	// 1. 错句强制插入：status=open 且 next_due <= 今天，不受 7 天去重限制。
	var forced []BankSentence
	if wrong != nil {
		for _, w := range wrong.Sentences {
			if w.Status != "open" {
				continue
			}
			due, err := time.Parse("2006-01-02", w.NextDue)
			if err != nil {
				// next_due 缺失或格式不对 → 当作今天到期，宁可多练不要漏
				due = today
			}
			if !today.Before(due) {
				forced = append(forced, BankSentence{
					ID: "wrong-" + w.Answer, Lesson: "", Type: "original",
					Answer: w.Answer, Chinese: w.Chinese,
				})
			}
		}
	}
	for _, s := range forced {
		add(s)
	}

	// 2. 近 7 天归一化禁用集 + 每句"上次出过时间"。
	banned := make(map[string]bool)
	lastSeen := make(map[string]time.Time)
	cutoff := today.AddDate(0, 0, -7)
	for dateStr, answers := range hist {
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		for _, a := range answers {
			n := normSentence(a)
			if n == "" {
				continue
			}
			if !d.Before(cutoff) {
				banned[n] = true
			}
			if cur, ok := lastSeen[n]; !ok || d.After(cur) {
				lastSeen[n] = d
			}
		}
	}

	// 3. 变形句配额（20 句 → 4 句，50 句 → 10 句）。
	quota := count / 5
	var transformed, originals []sentenceCand
	for _, s := range bank.Sentences {
		if s.Answer == "" {
			continue
		}
		c := sentenceCand{s: s, last: lastSeen[normSentence(s.Answer)]}
		if s.Type == "transformed" {
			transformed = append(transformed, c)
		} else {
			originals = append(originals, c)
		}
	}
	sortCands(transformed)
	tn := 0
	for _, c := range transformed {
		if tn >= quota {
			break
		}
		if banned[normSentence(c.s.Answer)] {
			continue
		}
		if add(c.s) {
			tn++
		}
	}

	// 4. 原文按课轮转，跨课摊平。先严格避开近 7 天，句池不够再放宽。
	sortCands(originals)
	byLesson := make(map[string][]sentenceCand)
	var lessons []string
	for _, c := range originals {
		ls := c.s.Lesson
		if ls == "" {
			ls = "未分类"
		}
		if _, ok := byLesson[ls]; !ok {
			lessons = append(lessons, ls)
		}
		byLesson[ls] = append(byLesson[ls], c)
	}
	sortLessons(lessons)

	fill := func(strict bool) {
		cursor := make(map[string]int, len(lessons))
		for len(picked) < count {
			progressed := false
			for _, ls := range lessons {
				lst := byLesson[ls]
				for cursor[ls] < len(lst) {
					c := lst[cursor[ls]]
					cursor[ls]++
					n := normSentence(c.s.Answer)
					if used[n] {
						continue
					}
					if strict && banned[n] {
						continue
					}
					if add(c.s) {
						progressed = true
						break
					}
				}
				if len(picked) >= count {
					return
				}
			}
			if !progressed {
				return // 句池耗尽
			}
		}
	}
	fill(true)
	if len(picked) < count {
		fill(false) // 放宽：允许复用近 7 天出过的，按「最久没出过」优先
	}

	out := make([]PlanSentence, 0, len(picked))
	for i, s := range picked {
		out = append(out, PlanSentence{Number: i + 1, Chinese: s.Chinese, Answer: s.Answer})
	}
	return out
}

// sortCands 按「从没出过优先，其次最久没出过，最后按 id 稳定」。
func sortCands(cs []sentenceCand) {
	sort.SliceStable(cs, func(i, j int) bool {
		zi, zj := cs[i].last.IsZero(), cs[j].last.IsZero()
		if zi != zj {
			return zi
		}
		if !zi && !cs[i].last.Equal(cs[j].last) {
			return cs[i].last.Before(cs[j].last)
		}
		return cs[i].s.ID < cs[j].s.ID
	})
}

var lessonNumRe = regexp.MustCompile(`\d+`)

// sortLessons 第2课 < 第10课（按数字，不按字符串）。
func sortLessons(ls []string) {
	num := func(s string) int {
		m := lessonNumRe.FindString(s)
		if m == "" {
			return 1 << 30
		}
		n, err := strconv.Atoi(m)
		if err != nil {
			return 1 << 30
		}
		return n
	}
	sort.SliceStable(ls, func(i, j int) bool {
		ni, nj := num(ls[i]), num(ls[j])
		if ni != nj {
			return ni < nj
		}
		return ls[i] < ls[j]
	})
}

// ---------- 落库 ----------

// ApplySentenceState 把造句结果写进 sentence_wrong.json 与 sentence_history.json。
//
// 规则（SKILL.md 第 327-332 行）：
//   - 写对 → status=archived
//   - 写错 → wrong_count+1、last_wrong=当天、next_due=当天+3/7/14（count=1/2/≥3）
//   - 本文件里没有的错句 → 追加，count=1、next_due=当天+3
//
// results 里带 Answer 的条目都会被记进 history（按 planDate），用于 7 天去重。
func ApplySentenceState(wrong *SentenceWrong, hist SentenceHistory,
	results []SentenceResult, planDate string) (*SentenceWrong, SentenceHistory) {

	if wrong == nil {
		wrong = &SentenceWrong{}
	}
	if hist == nil {
		hist = SentenceHistory{}
	}

	idx := make(map[string]int) // norm -> index in wrong.Sentences
	for i, w := range wrong.Sentences {
		idx[normSentence(w.Answer)] = i
	}

	var asked []string
	for _, r := range results {
		if r.Answer == "" {
			continue
		}
		asked = append(asked, r.Answer)
		n := normSentence(r.Answer)

		if r.Correct {
			if i, ok := idx[n]; ok {
				wrong.Sentences[i].Status = "archived"
				wrong.Sentences[i].LastSeen = planDate
			}
			continue
		}

		if i, ok := idx[n]; ok {
			w := &wrong.Sentences[i]
			w.Status = "open"
			w.LastSeen = planDate
			w.LastWrong = planDate
			w.WrongCount++
			if w.Chinese == "" {
				w.Chinese = chineseFromResults(results, r.Answer)
			}
			w.NextDue = nextSentenceDue(planDate, w.WrongCount)
			continue
		}

		// 新错句
		wrong.Sentences = append(wrong.Sentences, WrongSentence{
			Answer:     r.Answer,
			Chinese:    chineseFromResults(results, r.Answer),
			Status:     "open",
			LastSeen:   planDate,
			LastWrong:  planDate,
			WrongCount: 1,
			NextDue:    nextSentenceDue(planDate, 1),
		})
		idx[n] = len(wrong.Sentences) - 1
	}

	// history：追加当天出过的句子，清理 30 天前。
	if len(asked) > 0 {
		hist[planDate] = mergeUnique(hist[planDate], asked)
	}
	pruneSentenceHistory(hist, planDate, 30)

	return wrong, hist
}

// nextSentenceDue 错句重出间隔：count=1 → 3 天，count=2 → 7 天，count>=3 → 14 天。
func nextSentenceDue(planDate string, count int) string {
	d, err := time.Parse("2006-01-02", planDate)
	if err != nil {
		d = time.Now()
	}
	var days int
	switch {
	case count <= 1:
		days = 3
	case count == 2:
		days = 7
	default:
		days = 14
	}
	return d.AddDate(0, 0, days).Format("2006-01-02")
}

// pruneSentenceHistory 清理 days 天以前的记录。
func pruneSentenceHistory(h SentenceHistory, today string, days int) {
	base, err := time.Parse("2006-01-02", today)
	if err != nil {
		return
	}
	cutoff := base.AddDate(0, 0, -days)
	for k := range h {
		d, err := time.Parse("2006-01-02", k)
		if err != nil {
			delete(h, k) // 日期格式不对的旧数据，直接清掉
			continue
		}
		if d.Before(cutoff) {
			delete(h, k)
		}
	}
}

func mergeUnique(dst, src []string) []string {
	seen := make(map[string]bool, len(dst))
	for _, s := range dst {
		seen[normSentence(s)] = true
	}
	for _, s := range src {
		n := normSentence(s)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		dst = append(dst, s)
	}
	return dst
}

// chineseFromResults 从结果里取同 answer 的中文提示（用于新错句条目）。
func chineseFromResults(results []SentenceResult, answer string) string {
	n := normSentence(answer)
	for _, r := range results {
		if normSentence(r.Answer) == n && r.Chinese != "" {
			return r.Chinese
		}
	}
	return ""
}
