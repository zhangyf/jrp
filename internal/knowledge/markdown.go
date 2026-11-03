package knowledge

import (
	"regexp"
	"strconv"
	"strings"
)

// Span 一个行内片段。bold/code 让前端决定样式，避免前端再去解析 markdown 标记。
type Span struct {
	Text string `json:"t"`
	Bold bool   `json:"b,omitempty"`
	Code bool   `json:"c,omitempty"`
}

// Item 列表的一项。
type Item struct {
	Spans []Span `json:"spans"`
}

// Block 知识点文档的一个块。
//
// type 只有六种：h1 / h2 / h3 / p / quote / list / table。
// 文本型（h1-h3、p、quote）用 spans，list 用 items，table 用 header + rows。
// 表格单元格只留纯文本（去掉 ** 标记）—— 表格里加粗没有意义，还会让前端多套一层。
type Block struct {
	Type    string     `json:"type"`
	Spans   []Span     `json:"spans,omitempty"`
	Items   []Item     `json:"items,omitempty"`
	Ordered bool       `json:"ordered,omitempty"`
	Header  []string   `json:"header,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
}

// Title 取标题：优先用文档里的一级标题（「# 标准日本语初级上册 第21课 知识点」），
// 没有就退回「第N课」。
func Title(blocks []Block, num int) string {
	for _, b := range blocks {
		if b.Type == "h1" && len(b.Spans) > 0 {
			return spanText(b.Spans)
		}
	}
	return "第" + strconv.Itoa(num) + "课"
}

// VisibleBlockCount 未登录能看到几个块。
//
// 规则：按「文本量」累计到三成为止 —— 按块数算的话，第 1 课这种只有几段的小文档
// 会被整篇放完，第 20 课又能看到大半，体感不一致。
// 至少给 3 块（否则一眼全是模糊，像坏了），且**至少留一块被挡**（否则等于全放开）。
func VisibleBlockCount(blocks []Block) int {
	if len(blocks) <= 1 {
		return len(blocks)
	}
	total := 0
	for _, b := range blocks {
		total += blockWeight(b)
	}
	if total == 0 {
		return len(blocks) - 1
	}
	budget := total * 3 / 10
	used, n := 0, 0
	for _, b := range blocks {
		if used >= budget && n >= 3 {
			break
		}
		used += blockWeight(b)
		n++
	}
	if n < 3 {
		n = 3
	}
	// 块数也要封顶：只看文本量的话，标题块「便宜」而表格「贵」，
	// 实测第 21 课会放到 44 块里的 18 块 —— 看着就不像「一部分」了。
	if cap := len(blocks) * 3 / 10; n > cap && cap >= 3 {
		n = cap
	}
	if n > len(blocks)-1 {
		n = len(blocks) - 1
	}
	return n
}

// blockWeight 块的"分量"，用字符数近似（表格按格子算，不然大表格会被当成一句）。
func blockWeight(b Block) int {
	switch b.Type {
	case "table":
		n := 0
		for _, r := range b.Rows {
			for _, c := range r {
				n += len([]rune(c))
			}
		}
		return n
	case "list":
		n := 0
		for _, it := range b.Items {
			n += len([]rune(spanText(it.Spans)))
		}
		return n
	}
	return len([]rune(spanText(b.Spans)))
}

func spanText(ss []Span) string {
	var sb strings.Builder
	for _, s := range ss {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

// ---------- markdown 解析 ----------
//
// 只支持知识点文档实际用到的那点语法：# / ## / ### 标题、> 引用、- 无序列表、
// 1. 有序列表、| 表格、段落，行内 **粗体** 和 `代码`。
// 这是给自家 AI 生成的文档用的，不是通用 markdown 解析器 —— 遇到不认识的行
// 一律当普通段落，绝不能整段丢掉（丢内容比样式难看严重得多）。

var (
	mdH        = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdQuote    = regexp.MustCompile(`^>\s?(.*)$`)
	mdUL       = regexp.MustCompile(`^[-*]\s+(.*)$`)
	mdOL       = regexp.MustCompile(`^\d+[.、)]\s+(.*)$`)
	mdTableSep = regexp.MustCompile(`^\|?[\s:\-\|]+\|[\s:\-\|]*$`)
)

func ParseMarkdown(src string) []Block {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []Block
	i := 0
	for i < len(lines) {
		line := strings.TrimRight(lines[i], " \t")

		switch {
		case strings.TrimSpace(line) == "":
			i++
			continue

		// 表格：连续的 | 开头行，第二行是 |---| 分隔行则跳过。
		case strings.HasPrefix(strings.TrimSpace(line), "|"):
			var raw []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				raw = append(raw, strings.TrimSpace(lines[i]))
				i++
			}
			if b, ok := parseTable(raw); ok {
				out = append(out, b)
			}
			continue

		// 引用块：连续 > 行合成一块。
		case mdQuote.MatchString(line):
			var body []string
			for i < len(lines) {
				m := mdQuote.FindStringSubmatch(strings.TrimRight(lines[i], " \t"))
				if m == nil {
					break
				}
				if t := strings.TrimSpace(m[1]); t != "" {
					body = append(body, t)
				}
				i++
			}
			out = append(out, Block{Type: "quote", Spans: parseInline(strings.Join(body, " "))})
			continue

		case mdH.MatchString(line):
			m := mdH.FindStringSubmatch(line)
			lv := len(m[1])
			if lv > 3 {
				lv = 3
			}
			out = append(out, Block{Type: "h" + strconv.Itoa(lv), Spans: parseInline(strings.TrimSpace(m[2]))})
			i++
			continue

		// 列表：连续同类型行合成一块。
		case mdUL.MatchString(line) || mdOL.MatchString(line):
			ordered := mdOL.MatchString(line) && !mdUL.MatchString(line)
			var items []Item
			for i < len(lines) {
				l := strings.TrimRight(lines[i], " \t")
				var m []string
				if ordered {
					m = mdOL.FindStringSubmatch(l)
				} else {
					m = mdUL.FindStringSubmatch(l)
				}
				if m == nil {
					break
				}
				items = append(items, Item{Spans: parseInline(strings.TrimSpace(m[1]))})
				i++
			}
			out = append(out, Block{Type: "list", Ordered: ordered, Items: items})
			continue

		// 普通段落：连续非空、非以上开头的行合成一段。
		default:
			var body []string
			for i < len(lines) {
				l := strings.TrimRight(lines[i], " \t")
				t := strings.TrimSpace(l)
				if t == "" || mdH.MatchString(l) || mdQuote.MatchString(l) ||
					mdUL.MatchString(l) || mdOL.MatchString(l) ||
					strings.HasPrefix(t, "|") {
					break
				}
				body = append(body, t)
				i++
			}
			if len(body) > 0 {
				out = append(out, Block{Type: "p", Spans: parseInline(strings.Join(body, " "))})
			}
		}
	}
	return out
}

// parseTable 把连续的表格行变成块。第一行为表头，第二行若是分隔行则丢掉。
func parseTable(raw []string) (Block, bool) {
	var rows [][]string
	for _, r := range raw {
		if mdTableSep.MatchString(r) {
			continue
		}
		r = strings.Trim(r, "|")
		cells := strings.Split(r, "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return Block{}, false
	}
	b := Block{Type: "table", Header: rows[0]}
	if len(rows) > 1 {
		b.Rows = rows[1:]
	}
	return b, true
}

// parseInline 解析行内 **粗体** 和 `代码`。
//
// 用两两配对的最左匹配：** 必须成对出现才生效，落单的 ** 原样留下
// （文档里常有「*」当着重号的情况，别把它吃成一个粗体开头导致后面全乱）。
func parseInline(s string) []Span {
	var out []Span
	for len(s) > 0 {
		ib := strings.Index(s, "**")
		ic := strings.Index(s, "`")
		if ib < 0 && ic < 0 {
			out = append(out, Span{Text: s})
			break
		}
		// 先处理靠前的那个标记
		if ic >= 0 && (ib < 0 || ic < ib) {
			if end := strings.Index(s[ic+1:], "`"); end >= 0 {
				if ic > 0 {
					out = append(out, Span{Text: s[:ic]})
				}
				out = append(out, Span{Text: s[ic+1 : ic+1+end], Code: true})
				s = s[ic+1+end+1:]
				continue
			}
			// 落单的反引号：当普通字符
			out = append(out, Span{Text: s[:ic+1]})
			s = s[ic+1:]
			continue
		}
		if end := strings.Index(s[ib+2:], "**"); end >= 0 {
			if ib > 0 {
				out = append(out, Span{Text: s[:ib]})
			}
			out = append(out, Span{Text: s[ib+2 : ib+2+end], Bold: true})
			s = s[ib+2+end+2:]
			continue
		}
		out = append(out, Span{Text: s[:ib+2]})
		s = s[ib+2:]
	}
	// 合并相邻同属性片段，省得前端多渲染一堆 text
	var merged []Span
	for _, sp := range out {
		if sp.Text == "" {
			continue
		}
		if n := len(merged); n > 0 && merged[n-1].Bold == sp.Bold && merged[n-1].Code == sp.Code {
			merged[n-1].Text += sp.Text
			continue
		}
		merged = append(merged, sp)
	}
	return merged
}
