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
	h2LessonRe      = regexp.MustCompile(`^##\s*第(\d+)课`)
	vocabHeadingRe  = regexp.MustCompile(`生词|单词|词汇|末词`)
	straySlashRe    = regexp.MustCompile(`[\s　]*/[\s　]*`)
	multiSpaceRe    = regexp.MustCompile(`[\s　]+`)
	bankIDRe        = regexp.MustCompile(`^L(\d+)([AB])(\d+)$`)
	placeholderRe   = regexp.MustCompile(`待补|TODO|（待`)
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

// buildImportedEntries 把抽出来的行变成句库条目：
// 文档内去重 + 与已有句库去重（按 normSentence 比，避免句号/空格差异重复入库），
// 然后连号编 ID。返回新条目与被跳过的条数。
func buildImportedEntries(rows []importedRow, lesson int, kind lessonDocKind, doc string, existing []BankSentence) ([]BankSentence, int) {
	have := make(map[string]bool, len(existing))
	for _, s := range existing {
		if n := normSentence(s.Answer); n != "" {
			have[n] = true
		}
	}

	seq := maxBankSeq(existing, lesson, kind.IDTag)
	seen := make(map[string]bool, len(rows))
	out := make([]BankSentence, 0, len(rows))
	skipped := 0
	for _, r := range rows {
		n := normSentence(r.JP)
		if n == "" || seen[n] || have[n] {
			skipped++
			continue
		}
		seen[n] = true
		seq++
		out = append(out, BankSentence{
			ID:        fmt.Sprintf("L%d%s%03d", lesson, kind.IDTag, seq),
			Lesson:    lessonLabelOf(lesson),
			Source:    kind.Source,
			SourceDoc: doc,
			Type:      "original",
			Answer:    r.JP,
			Chinese:   r.CN,
		})
	}
	return out, skipped
}
