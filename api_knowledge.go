package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- 知识点板块（小程序「新标日III知识点」，2026-11-03 老师要求） ----------
//
// 需求原话：小程序里新增一个板块，把新标日各课的知识点总结按课时展示出来；
// **非登录状态下仅展示一部分，其余被模糊滤镜挡住**。
//
// 三条设计决定，动这个文件之前先读：
//
//  1. **markdown 在服务端解析，不在小程序里解析**。
//     小程序没有 markdown 渲染能力（rich-text 只吃 HTML 节点，且样式不可控），
//     而且「哪几块模糊」必须按块切 —— 所以这里把 md 拆成结构化块数组发给前端，
//     前端只负责按 type 摆样式。
//
//  2. **这个接口不走 withUser**。
//     匿名和体验版也要能读到（只是读到的内容被截断），而 withUser 在鉴权打开后
//     对匿名直接 401。所以这里自己认会话：认出来就给全量，认不出来就给受限版。
//     ⛔ 只读 sharedBase（教材内容，不属于任何人的个人数据），一行都不碰 uid 目录。
//
//  3. **模糊是展示层软限制，不是加密**。
//     后端照样把全文发下去，只多给一个 visible 数字让前端把后面的块盖住。
//     想拿全文的人抓个包就有 —— 这是有意的取舍：知识点是教材整理而非隐私数据，
//     挡一下是为了引导登录，不是防攻击。真要防就在服务端按块截断（那前端就没有
//     「模糊」的视觉了，跟老师要的效果不符）。

// KSpan 一个行内片段。bold/code 让前端决定样式，避免前端再去解析 markdown 标记。
type KSpan struct {
	Text string `json:"t"`
	Bold bool   `json:"b,omitempty"`
	Code bool   `json:"c,omitempty"`
}

// KItem 列表的一项。
type KItem struct {
	Spans []KSpan `json:"spans"`
}

// KBlock 知识点文档的一个块。
//
// type 只有六种：h1 / h2 / h3 / p / quote / list / table。
// 文本型（h1-h3、p、quote）用 spans，list 用 items，table 用 header + rows。
// 表格单元格只留纯文本（去掉 ** 标记）—— 表格里加粗没有意义，还会让前端多套一层。
type KBlock struct {
	Type    string     `json:"type"`
	Spans   []KSpan    `json:"spans,omitempty"`
	Items   []KItem    `json:"items,omitempty"`
	Ordered bool       `json:"ordered,omitempty"`
	Header  []string   `json:"header,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
}

// lessonDocRe 从文件名里认课号：「标准日本语初级上册_第21课知识点.md」→ 21。
//
// 只认「第N课知识点」这一种命名 —— knowledge 目录里还混着
// 「课本原文汇编_第N单元（基本课文）.md」和「造句易错语法速查（第12-13课）.md」，
// 它们不是按课的知识点总结，不能进这个板块。
var lessonDocRe = regexp.MustCompile(`第(\d+)课知识点`)

// handleKnowledge 知识点板块的唯一入口。
//
//	GET /api/knowledge            → 课号列表（按课号数字排序，不是字符串排序）
//	GET /api/knowledge?lesson=21  → 第 21 课全文（分块）
func (s *server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	objs, err := s.storage.ListKnowledge(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "知识点目录读不了：" + err.Error(),
		})
		return
	}

	type lesson struct {
		num  int
		name string
		size int64
	}
	var ls []lesson
	for _, o := range objs {
		m := lessonDocRe.FindStringSubmatch(o.Key)
		if len(m) != 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ls = append(ls, lesson{num: n, name: o.Key[strings.LastIndex(o.Key, "/")+1:], size: o.Size})
	}
	// 课号必须按数字排：字符串排序会把第 10 课排在第 1 课前面。
	sort.Slice(ls, func(i, j int) bool { return ls[i].num < ls[j].num })

	if len(ls) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "lessons": []interface{}{},
		})
		return
	}

	full := s.knowledgeFullAccess(r, ctx)
	want := strings.TrimSpace(r.URL.Query().Get("lesson"))

	// 没传 lesson：只回列表（体验版也要能看到有哪些课，才有「登录看全部」的动力）。
	if want == "" {
		out := make([]map[string]interface{}, 0, len(ls))
		for _, l := range ls {
			out = append(out, map[string]interface{}{
				"lesson": l.num,
				"title":  "第" + strconv.Itoa(l.num) + "课",
				"size":   l.size,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"locked":  !full,
			"lessons": out,
		})
		return
	}

	num, err := strconv.Atoi(want)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "lesson 应该是课号数字",
		})
		return
	}
	var hit *lesson
	for i := range ls {
		if ls[i].num == num {
			hit = &ls[i]
			break
		}
	}
	if hit == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false, "error": "没有第 " + want + " 课的知识点",
		})
		return
	}

	data, err := s.storage.DownloadKnowledge(ctx, hit.name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "知识点读不了：" + err.Error(),
		})
		return
	}
	blocks := parseKnowledgeMarkdown(string(data))

	visible := len(blocks)
	if !full {
		visible = visibleBlockCount(blocks)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"lesson":  num,
		"title":   knowledgeTitle(blocks, num),
		"locked":  !full,
		"visible": visible,
		"blocks":  blocks,
	})
}

// knowledgeFullAccess 这次请求能不能看全文。
//
// 三种情况给全量：正常登录（含 7 天体验期内的 pending）、被冻结/过期 pending 不算、
// 以及 CLI 的共享 token。体验版（trial）和匿名一律受限。
func (s *server) knowledgeFullAccess(r *http.Request, ctx context.Context) bool {
	if s.token != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1 {
		return true
	}
	sess, logged := s.sessionUser(r)
	if !logged {
		return false
	}
	if sess.UID == "" || sess.UID == trialUID {
		return false // 体验版凭证是公开的，等于匿名
	}
	u, ok := findUserByUID(cachedUsers(ctx, s.storage), sess.UID)
	if !ok {
		return true // 用户表里查不到（老会话）就按会话角色给
	}
	if u.Role == roleDisabled {
		return false
	}
	if u.Role == rolePending && pendingExpired(u) {
		return false
	}
	return true
}

// knowledgeTitle 取标题：优先用文档里的一级标题（「# 标准日本语初级上册 第21课 知识点」），
// 没有就退回「第N课」。
func knowledgeTitle(blocks []KBlock, num int) string {
	for _, b := range blocks {
		if b.Type == "h1" && len(b.Spans) > 0 {
			return spanText(b.Spans)
		}
	}
	return "第" + strconv.Itoa(num) + "课"
}

// visibleBlockCount 未登录能看到几个块。
//
// 规则：按「文本量」累计到三成为止 —— 按块数算的话，第 1 课这种只有几段的小文档
// 会被整篇放完，第 20 课又能看到大半，体感不一致。
// 至少给 3 块（否则一眼全是模糊，像坏了），且**至少留一块被挡**（否则等于全放开）。
func visibleBlockCount(blocks []KBlock) int {
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
func blockWeight(b KBlock) int {
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

func spanText(ss []KSpan) string {
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

func parseKnowledgeMarkdown(src string) []KBlock {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []KBlock
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
			out = append(out, KBlock{Type: "quote", Spans: parseInline(strings.Join(body, " "))})
			continue

		case mdH.MatchString(line):
			m := mdH.FindStringSubmatch(line)
			lv := len(m[1])
			if lv > 3 {
				lv = 3
			}
			out = append(out, KBlock{Type: "h" + strconv.Itoa(lv), Spans: parseInline(strings.TrimSpace(m[2]))})
			i++
			continue

		// 列表：连续同类型行合成一块。
		case mdUL.MatchString(line) || mdOL.MatchString(line):
			ordered := mdOL.MatchString(line) && !mdUL.MatchString(line)
			var items []KItem
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
				items = append(items, KItem{Spans: parseInline(strings.TrimSpace(m[1]))})
				i++
			}
			out = append(out, KBlock{Type: "list", Ordered: ordered, Items: items})
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
				out = append(out, KBlock{Type: "p", Spans: parseInline(strings.Join(body, " "))})
			}
		}
	}
	return out
}

// parseTable 把连续的表格行变成块。第一行为表头，第二行若是分隔行则丢掉。
func parseTable(raw []string) (KBlock, bool) {
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
		return KBlock{}, false
	}
	b := KBlock{Type: "table", Header: rows[0]}
	if len(rows) > 1 {
		b.Rows = rows[1:]
	}
	return b, true
}

// parseInline 解析行内 **粗体** 和 `代码`。
//
// 用两两配对的最左匹配：** 必须成对出现才生效，落单的 ** 原样留下
// （文档里常有「*」当着重号的情况，别把它吃成一个粗体开头导致后面全乱）。
func parseInline(s string) []KSpan {
	var out []KSpan
	for len(s) > 0 {
		ib := strings.Index(s, "**")
		ic := strings.Index(s, "`")
		if ib < 0 && ic < 0 {
			out = append(out, KSpan{Text: s})
			break
		}
		// 先处理靠前的那个标记
		if ic >= 0 && (ib < 0 || ic < ib) {
			if end := strings.Index(s[ic+1:], "`"); end >= 0 {
				if ic > 0 {
					out = append(out, KSpan{Text: s[:ic]})
				}
				out = append(out, KSpan{Text: s[ic+1 : ic+1+end], Code: true})
				s = s[ic+1+end+1:]
				continue
			}
			// 落单的反引号：当普通字符
			out = append(out, KSpan{Text: s[:ic+1]})
			s = s[ic+1:]
			continue
		}
		if end := strings.Index(s[ib+2:], "**"); end >= 0 {
			if ib > 0 {
				out = append(out, KSpan{Text: s[:ib]})
			}
			out = append(out, KSpan{Text: s[ib+2 : ib+2+end], Bold: true})
			s = s[ib+2+end+2:]
			continue
		}
		out = append(out, KSpan{Text: s[:ib+2]})
		s = s[ib+2:]
	}
	// 合并相邻同属性片段，省得前端多渲染一堆 text
	var merged []KSpan
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
