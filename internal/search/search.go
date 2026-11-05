// Package search 存放全局搜索（/api/search）的纯匹配逻辑。
//
// 之所以单独成包：搜索要同时做「词汇字面匹配」和「知识点语义检索结果清洗」，
// 这些规则跟 COS / net/http / *Storage 无关，抽成纯函数可脱离外部依赖单测。
// handler 只负责取数、编排两路、拼 HTTP 响应。
//
// 归一化口径与判分 sentence.Norm **故意不同**：判分要删标点（把「甲、乙」和
// 「甲乙」视作同一句），而搜索要查中文释义/带括号词形，删标点会过激 ——
// 「みず(水)」和「みず水」不该因为括号被抹掉就乱命中。所以这里只做
// ① 全角→半角 ② 去空白 ③ ASCII 小写，标点一律保留。
package search

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Normalize 搜索专用归一化：全角 ASCII→半角、去所有空白（含全角空格 U+3000）、
// ASCII 大写→小写；标点保留。query 和候选字段都用同一份，保证可比。
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0xFF01 && r <= 0xFF5E { // 全角 ASCII 区（含全角括号（）、全角字母数字）
			r -= 0xFEE0
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if unicode.IsSpace(r) { // 半角空白 + 全角空格 U+3000
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Score 返回 query 对一组候选字段的最高匹配分；-1 表示完全不匹配。
//
// 同时用「原串」和「归一化串」比较，覆盖两类输入：
//   - 原串：用户照着词表打「みず(水)」，括号/大小写都对得上；
//   - 归一化串：用户打全角「くろい（黒い）」或带空格，折宽度后与
//     「くろい(黒い)」互相命中，或打罗马字大小写不一致。
//
// 打分档（前缀命中优先，完全相等最优）：让「搜 ている」时
// 「ている」整词排在「〜ています」前面。fields 按调用方给的顺序，
// 但取所有字段里的最高分，不因字段顺序影响排序。
func Score(query string, fields []string) int {
	q := strings.TrimSpace(query)
	if q == "" {
		return -1
	}
	nq := Normalize(q)
	if nq == "" {
		return -1
	}
	ql := strings.ToLower(q)
	best := -1
	for _, f := range fields {
		if f == "" {
			continue
		}
		if s := fieldScore(ql, nq, f); s > best {
			best = s
		}
	}
	return best
}

func fieldScore(qLower, nq, f string) int {
	fl := strings.ToLower(strings.TrimSpace(f))
	nf := Normalize(f)
	switch {
	case fl == qLower:
		return 100
	case nf == nq:
		return 95
	case strings.HasPrefix(fl, qLower):
		return 80
	case strings.HasPrefix(nf, nq):
		return 75
	case strings.Contains(fl, qLower):
		return 55
	case strings.Contains(nf, nq):
		return 45
	}
	return -1
}

// TruncateRunes 按 rune 截断（不是字节，中文/假名不会截出半个字）。
// MetaInsight 的 SearchText 上限是 60 个 UTF-8 字符，结果片段展示也用它。
func TruncateRunes(s string, max int) string {
	r := []rune(s)
	if max > 0 && len(r) > max {
		return string(r[:max])
	}
	return s
}

var (
	// MetaInsight 每个 chunk 都重复带文档标题，例如
	// 「# 标准日本语初级上册 第16课 知识点」。结果面板不该整行重复它。
	repeatedTitleRe = regexp.MustCompile(`(?m)^#{1,6}[^\n]*(?:标准日本语|第\d+课知识点)[^\n]*\n?`)
	// markdown 表格分隔行 |---|---| / :---: / --- 之类，只留内容行。
	tableSepRe     = regexp.MustCompile(`^[\s|:-]+$`)
	imageTagRe     = regexp.MustCompile(`\{Image_\d+\}`)
	multiBlankRe   = regexp.MustCompile(`[ \t]+`)
	lessonInURIRex = regexp.MustCompile(`第(\d+)课知识点`)
)

// CleanDocText 把 MetaInsight 返回的 chunk 清成面板里能读的一行摘要：
// 去重复文档标题、去图片占位符、表格分隔行、把 | # 等 markdown 符号压平，
// 合并空白后按 rune 截断补省略号。不解析 markdown，只做展示层清洗。
func CleanDocText(text string, maxRunes int) string {
	s := imageTagRe.ReplaceAllString(text, "")
	s = repeatedTitleRe.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || tableSepRe.MatchString(t) {
			continue
		}
		t = strings.ReplaceAll(t, "|", " ")
		t = strings.TrimLeft(t, "# ") // 内容里的小标题去掉井号，文字保留
		t = multiBlankRe.ReplaceAllString(t, " ")
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	s = strings.Join(out, " ")
	if maxRunes > 0 {
		r := []rune(s)
		if len(r) > maxRunes {
			return string(r[:maxRunes]) + "…"
		}
	}
	return s
}

// LessonFromURI 从 cos://.../标准日本语初级上册_第16课知识点.md 解出课号。
// 解不出（汇编、速查、其他命名）返回 ok=false —— 这类结果前端只展示、不跳页。
func LessonFromURI(uri string) (int, bool) {
	m := lessonInURIRex.FindStringSubmatch(uri)
	if len(m) != 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// TitleFromURI 结果标题：能解课号就是「第N课」，否则用文件名（去掉 .md）。
// 汇编/速查类文档靠这个告诉用户命中了哪份材料。
func TitleFromURI(uri string) string {
	if n, ok := LessonFromURI(uri); ok {
		return "第" + strconv.Itoa(n) + "课"
	}
	base := path.Base(uri)
	return strings.TrimSuffix(base, ".md")
}

// MatchText 在一段文本里找 query 的字面命中，返回命中行 ±1 行作为摘要。
// 只给**兜底路**用（MetaInsight 不可用时服务端读 knowledge md 做字面匹配），
// 不下发全库、只回命中的那几行。
func MatchText(text, q string) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		if Score(q, []string{ln}) < 0 {
			continue
		}
		lo, hi := i-1, i+2
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		return strings.Join(lines[lo:hi], " "), true
	}
	return "", false
}
