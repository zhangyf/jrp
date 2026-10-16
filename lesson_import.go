package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- 汇编文档 → 句库 ----------
//
// 「课本原文汇编_第N单元（基本课文/应用课文）」一直是老师录课文的入口，但句库
// sentence_bank 只能靠 `sentence-bank --file` 手工上传，代码里没有任何自动环节。
// 2026-10-16 第19课就是这么漏的：知识点、汇编、单词三样都齐，句库里一句都没有，
// 造句永远练不到那一课。
//
// 这里把「从汇编文档抽句子」做成纯函数，CLI 与（将来的）自动流程共用同一套。
// 抽取只认课本原文表，不生成、不改写句子 —— sentence-lint 的防线依然成立。

// lessonsPerUnit 标日初级名义上一单元 4 课，老师的汇编文档并不严格按这个分：
// 第1单元 1-4、第2单元只有 5-6、第3单元塞了 7-12、第4单元 13-16、第5单元 17-20。
// 所以 unitOfLesson 只能当"先猜这个"的起点，真正定位要走 resolveLessonDocs。
const lessonsPerUnit = 4

// maxUnit 找汇编文档时最多试到第几单元。
const maxUnit = 12

// unitOfLesson 按"一单元 4 课"猜第N课在第几单元。课号非法（≤0）返回 0。
func unitOfLesson(lesson int) int {
	if lesson <= 0 {
		return 0
	}
	return (lesson + lessonsPerUnit - 1) / lessonsPerUnit
}

// docFetcher 按文档名取内容，取不到返回 false。CLI 走 COS，单测喂内存。
type docFetcher func(name string) (string, bool)

// resolveLessonDocs 找第N课真正的汇编文档（基本课文 + 应用课文各一份）。
//
// 不能靠算术推单元 —— 第2单元只有 5-6 课、第3单元塞了 7-12 课，按 4 课一单元
// 去算会把第7课指到第2单元（那里根本没有第7课）。这里按内容找：谁的文档里
// 有「## 第N课」段落，就是它。
//
// 先试 unitOfLesson 的猜测值（大多数情况一次命中），再顺序兜底。
// 找到但段落是"待补"也算找到 —— 调用方看 sentences == 0 就知道是空壳子。
func resolveLessonDocs(fetch docFetcher, lesson int) (unit int, basic, applied string, ok bool) {
	guess := unitOfLesson(lesson)
	order := make([]int, 0, maxUnit+1)
	if guess > 0 {
		order = append(order, guess)
	}
	for u := 1; u <= maxUnit; u++ {
		if u != guess {
			order = append(order, u)
		}
	}
	for _, u := range order {
		name := lessonDocName(u, lessonDocKinds[0])
		content, found := fetch(name)
		if !found || !hasLessonSection(content, lesson) {
			continue
		}
		return u, name, lessonDocName(u, lessonDocKinds[1]), true
	}
	return 0, "", "", false
}

// lessonDocKind 一份汇编文档对应句库里的哪种来源。
type lessonDocKind struct {
	Source string // 存进 BankSentence.Source
	Suffix string // 文档名后缀
	IDTag  string // ID 中段：B = 基本课文文档，A = 应用课文文档
}

// lessonDocKinds 顺序固定：先基本课文文档（4 句课文 + 应用对话），再应用课文文档。
var lessonDocKinds = []lessonDocKind{
	{Source: "基本课文", Suffix: "基本课文", IDTag: "B"},
	{Source: "应用课文", Suffix: "应用课文", IDTag: "A"},
}

func lessonDocName(unit int, kind lessonDocKind) string {
	return fmt.Sprintf("课本原文汇编_第%d单元（%s）.md", unit, kind.Suffix)
}

func lessonLabelOf(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("第%d课", n)
}

type importedRow struct {
	JP string
	CN string
}

var (
	h2LessonRe     = regexp.MustCompile(`^##\s*第(\d+)课`)
	vocabHeadingRe = regexp.MustCompile(`生词|单词|词汇|末词`)
	straySlashRe   = regexp.MustCompile(`[\s　]*/[\s　]*`)
	multiSpaceRe   = regexp.MustCompile(`[\s　]+`)
	bankIDRe       = regexp.MustCompile(`^L(\d+)([AB])(\d+)$`)
	placeholderRe  = regexp.MustCompile(`待补|TODO|（待`)
)

// splitCells 拆 markdown 表格的一行，去掉首尾空管并 trim 每格。
func splitCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if strings.Trim(c, " -:") != "" {
			return false
		}
	}
	return true
}

// tableColumns 在表头行里定位「日文」「中文」两列。返回 -1 表示这不是句子表。
//
// 关键区分：生词表的表头是 `| 日文 | 中文 |`（只有 2 列），对话表至少 3 列
// （`| 序号 | 日文 | 中文 |`、`| 说话人 | 日文 | 中文 |`、
// `| 对话 | 说话人 | 日文 | 中文 |`）。用列数把生词表挡在外面。
func tableColumns(cells []string) (int, int) {
	if len(cells) < 3 {
		return -1, -1
	}
	jp, cn := -1, -1
	for i, c := range cells {
		switch c {
		case "日文":
			jp = i
		case "中文":
			cn = i
		}
	}
	return jp, cn
}

// cleanImportedJP 修掉汇编里手误的斜杠与多余空白，句库里存干净原文。
// 「明日は 残業しなくても/ いいですよ。」→「明日は 残業しなくても いいですよ。」
func cleanImportedJP(s string) string {
	s = strings.TrimSpace(s)
	s = straySlashRe.ReplaceAllString(s, " ")
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// isVocabCell 生词行：带全角括号又不带句号，如「初めて（第一次）」。
// 真正的句子不会「有括号没句号」。
func isVocabCell(jp string) bool {
	return strings.Contains(jp, "（") && !strings.Contains(jp, "。")
}

// extractLessonRows 从一份汇编文档里抽第 lesson 课的所有句子行，按文档顺序返回。
//
// 只收：本节课 `## 第N课` 段落内、非生词小节、列数≥3 且表头同时有「日文」「中文」
// 的表格数据行。找不到该课（或该课写着"待补"）返回空。
func extractLessonRows(md string, lesson int) []importedRow {
	var rows []importedRow
	inLesson := false
	inVocab := false
	jpCol, cnCol := -1, -1

	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))

		switch {
		case strings.HasPrefix(line, "## "):
			inLesson = false
			inVocab = false
			jpCol, cnCol = -1, -1
			if m := h2LessonRe.FindStringSubmatch(line); m != nil {
				if n, err := strconv.Atoi(m[1]); err == nil && n == lesson {
					inLesson = true
				}
			}
			continue
		case strings.HasPrefix(line, "### "):
			inVocab = vocabHeadingRe.MatchString(line)
			jpCol, cnCol = -1, -1
			continue
		}

		if !inLesson || inVocab || !strings.HasPrefix(line, "|") {
			continue
		}

		cells := splitCells(line)
		if isSeparatorRow(cells) {
			continue
		}
		if jp, cn := tableColumns(cells); jp >= 0 && cn >= 0 {
			jpCol, cnCol = jp, cn
			continue
		}
		if jpCol < 0 || cnCol < 0 || jpCol >= len(cells) || cnCol >= len(cells) {
			continue
		}

		jp := cleanImportedJP(cells[jpCol])
		if jp == "" || jp == "日文" || placeholderRe.MatchString(jp) || isVocabCell(jp) {
			continue
		}
		rows = append(rows, importedRow{JP: jp, CN: strings.TrimSpace(cells[cnCol])})
	}
	return rows
}

// hasLessonSection 文档里有没有这一课的段落（用于 lesson-status 判断"课文录了没"）。
func hasLessonSection(md string, lesson int) bool {
	want := fmt.Sprintf("第%d课", lesson)
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") && strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// lessonSectionsIn 列出文档里所有「## 第N课」段落的课号（升序去重）。
// save-lesson 用它提示"这份文档里这几课可以进句库了"。
func lessonSectionsIn(md string) []int {
	seen := map[int]bool{}
	var out []int
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		m := h2LessonRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// maxBankSeq 已有条目里 L<n><tag>xxx 的最大序号，新条目从它后面接着编。
func maxBankSeq(existing []BankSentence, lesson int, tag string) int {
	prefix := fmt.Sprintf("L%d%s", lesson, tag)
	max := 0
	for _, s := range existing {
		if !strings.HasPrefix(s.ID, prefix) {
			continue
		}
		m := bankIDRe.FindStringSubmatch(s.ID)
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[3]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// bankEntryUpdate 一次「原地覆盖」：文档里第 N 条被改了，句库里第 N 条跟着改，
// ID 不变（ID 就是它在这一课这个来源里的位置）。
type bankEntryUpdate struct {
	Index  int          `json:"-"` // 在 bank.Sentences 里的下标
	ID     string       `json:"id"`
	Before string       `json:"before"`
	After  string       `json:"after"`
	Entry  BankSentence `json:"entry"`
}

// importPlan 一课的导入方案。三种结局互斥：新增 / 覆盖 / 跳过。
type importPlan struct {
	Added   []BankSentence    `json:"added"`
	Updated []bankEntryUpdate `json:"updated"`
	Skipped int               `json:"skipped"`
	// Orphans 文档里已经没有、句库里还留着的条目 ID。只报告不自动删 ——
	// 当天 plan 可能正在用，删了会让老师正在练的句子凭空消失。
	Orphans []string `json:"orphans,omitempty"`
}

// buildImportPlan 把抽出来的行变成导入方案。三种情况：
//
//  1. 句库里已有完全一样的一句（按 normSentence 比，句号/空格/斜杠差异都算同一句）→ 跳过
//  2. 同一课同一来源的**第 N 条**已存在但内容不同 → **原地覆盖**，ID 不变
//     （文档改了原文，句库跟着改，不留旧的那一版）
//  3. 都没有 → 追加新条目，ID 接着已有最大号编
//
// 位置（第几条）按句库里「同课 + 同来源」的现有顺序算，不靠 ID 数字 ——
// 老课的 ID 有两种写法（L1-B001 / L18B001）且序号是跨课接的，不能当位置用。
func buildImportPlan(rows []importedRow, lesson int, kind lessonDocKind, doc string, existing []BankSentence) importPlan {
	label := lessonLabelOf(lesson)

	byNorm := make(map[string]int, len(existing))
	for i, s := range existing {
		if n := normSentence(s.Answer); n != "" {
			if _, dup := byNorm[n]; !dup {
				byNorm[n] = i
			}
		}
	}
	group := []int{}
	for i, s := range existing {
		if s.Lesson == label && s.Source == kind.Source {
			group = append(group, i)
		}
	}
	used := map[int]bool{}

	plan := importPlan{}
	seq := maxBankSeq(existing, lesson, kind.IDTag)
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		n := normSentence(r.JP)
		if n == "" || seen[n] {
			plan.Skipped++
			continue
		}
		seen[n] = true

		if idx, ok := byNorm[n]; ok {
			used[idx] = true
			plan.Skipped++
			continue
		}
		if idx := findRevisedEntry(existing, group, used, n); idx >= 0 {
			old := existing[idx]
			used[idx] = true
			entry := newBankSentence(lesson, kind, doc, 0, r)
			entry.ID = old.ID
			entry.Type = old.Type
			entry.Prototype = old.Prototype
			plan.Updated = append(plan.Updated, bankEntryUpdate{
				Index: idx, ID: old.ID, Before: old.Answer, After: r.JP, Entry: entry,
			})
			continue
		}
		seq++
		plan.Added = append(plan.Added, newBankSentence(lesson, kind, doc, seq, r))
	}

	for _, i := range group {
		if !used[i] {
			plan.Orphans = append(plan.Orphans, existing[i].ID)
		}
	}
	return plan
}

// reviseThreshold 判定「同一句被改写了」的相似度门槛（见 sentenceSimilarity）。
// 0.35 是拿真例子量出来的：儿子→女儿 这种改一个词约 0.41，
// 同课里两句不相关的句子普遍低于 0.25。
const reviseThreshold = 0.35

// findRevisedEntry 在同一课同一来源里找「就是这句、但内容被改过」的旧条目。
//
// 不能用位置（第几条）来配对：文档中间插入一句，后面全部错位，会把不相干的
// 旧句子盖掉。改用文本相似度，且要求该旧条目没被别的行认领过。
func findRevisedEntry(existing []BankSentence, group []int, used map[int]bool, norm string) int {
	best, bestScore := -1, 0.0
	for _, i := range group {
		if used[i] {
			continue
		}
		// 变形句是回炉句库的数据，不拿课本原文去盖它。
		if t := existing[i].Type; t != "" && t != "original" {
			continue
		}
		if s := sentenceSimilarity(normSentence(existing[i].Answer), norm); s > bestScore {
			best, bestScore = i, s
		}
	}
	if bestScore >= reviseThreshold {
		return best
	}
	return -1
}

// sentenceSimilarity 两个归一化句子的字符 2-gram Jaccard 相似度（0~1）。
// 判「改词级别」的改写够用：换掉一两个词仍然很高，换整句立刻掉下来。
func sentenceSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ar, br := []rune(a), []rune(b)
	if len(ar) < 2 || len(br) < 2 {
		return 0
	}
	setA := make(map[string]bool, len(ar))
	for i := 0; i+1 < len(ar); i++ {
		setA[string(ar[i:i+2])] = true
	}
	inter, union := 0, len(setA)
	seenB := make(map[string]bool, len(br))
	for i := 0; i+1 < len(br); i++ {
		g := string(br[i : i+2])
		if seenB[g] {
			continue
		}
		seenB[g] = true
		if setA[g] {
			inter++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func newBankSentence(lesson int, kind lessonDocKind, doc string, seq int, r importedRow) BankSentence {
	return BankSentence{
		ID:        fmt.Sprintf("L%d%s%03d", lesson, kind.IDTag, seq),
		Lesson:    lessonLabelOf(lesson),
		Source:    kind.Source,
		SourceDoc: doc,
		Type:      "original",
		Answer:    r.JP,
		Chinese:   r.CN,
	}
}
