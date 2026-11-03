package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 知识点板块的测试盯着三件事：
//  1. markdown 拆块别丢内容（丢内容比样式难看严重得多）；
//  2. 未登录必须**至少留一块被挡** —— 全放开等于没做这个限制；
//  3. 匿名/体验版能读（不然板块白屏），真登录才给全文。

const sampleKnowledgeMD = `# 标准日本语初级上册 第21课 知识点

> **本课核心：动词「た形」。**

## た形速查表

| 类别 | ます形 | た形 |
|---|---|---|
| 一类 | 書きます | 書いた |
| 二类 | 食べます | 食べた |

**口诀：て→た、で→だ。**

### 四大句型

- 动た形 ＋ ことが あります
- 动た形 ＋ 後で

1. 前项是名词时：名词＋の＋後で
2. 前项接た形

段落里的 ` + "`コード`" + ` 要单独成段，别被当成粗体。

落单的星号 * 不该吃掉后面的文字。
`

func TestParseKnowledgeMarkdownBlocks(t *testing.T) {
	bs := parseKnowledgeMarkdown(sampleKnowledgeMD)
	var kinds []string
	for _, b := range bs {
		kinds = append(kinds, b.Type)
	}
	got := strings.Join(kinds, ",")
	want := "h1,quote,h2,table,p,h3,list,list,p,p"
	if got != want {
		t.Fatalf("块序列不对：\n got=%s\nwant=%s", got, want)
	}

	// 表格：表头三列，两行数据，分隔行被丢掉
	var tab KBlock
	for _, b := range bs {
		if b.Type == "table" {
			tab = b
			break
		}
	}
	if len(tab.Header) != 3 || len(tab.Rows) != 2 {
		t.Fatalf("表格解析错：header=%v rows=%v", tab.Header, tab.Rows)
	}
	if tab.Rows[0][2] != "書いた" {
		t.Fatalf("表格内容错：%v", tab.Rows[0])
	}

	// 列表：无序在前、有序在后，ordered 标记正确
	var lists []KBlock
	for _, b := range bs {
		if b.Type == "list" {
			lists = append(lists, b)
		}
	}
	if len(lists) != 2 || lists[0].Ordered || !lists[1].Ordered {
		t.Fatalf("列表顺序/类型错：%+v", lists)
	}
	if len(lists[0].Items) != 2 || len(lists[1].Items) != 2 {
		t.Fatalf("列表项数错：%d / %d", len(lists[0].Items), len(lists[1].Items))
	}

	// h1 里的粗体要拆成 span
	h1 := bs[0]
	if len(h1.Spans) == 0 || spanText(h1.Spans) != "标准日本语初级上册 第21课 知识点" {
		t.Fatalf("h1 解析错：%+v", h1.Spans)
	}
}

func TestParseInlineKeepsBoldCodeAndStrayMarks(t *testing.T) {
	ss := parseInline("口诀是「**て→た**」，見た 後で 很重要")
	if len(ss) != 3 || !ss[1].Bold || ss[1].Text != "て→た" {
		t.Fatalf("粗体没拆出来：%+v", ss)
	}
	// 反引号里的代码
	ss = parseInline("用 `後で` 不是 `に`")
	var codes int
	for _, s := range ss {
		if s.Code {
			codes++
		}
	}
	if codes != 2 {
		t.Fatalf("代码段没拆出来：%+v", ss)
	}
	// 落单的 ** 不能吃掉后面的文字
	ss = parseInline("这里有个 ** 孤零零的星号")
	for _, s := range ss {
		if s.Bold {
			t.Fatalf("落单的 ** 被当成粗体开头了：%+v", ss)
		}
	}
	if spanText(ss) != "这里有个 ** 孤零零的星号" {
		t.Fatalf("落单标记把文字吃掉了：%+v", ss)
	}
}

func TestVisibleBlockCountAlwaysHidesSomething(t *testing.T) {
	bs := parseKnowledgeMarkdown(sampleKnowledgeMD)
	n := visibleBlockCount(bs)
	if n >= len(bs) {
		t.Fatalf("未登录却全放开了：visible=%d total=%d", n, len(bs))
	}
	if n < 3 {
		t.Fatalf("可见块太少（像坏了）：%d", n)
	}
	// 既要「有内容可看」，又不能「看着像全放开」——块数封顶三成
	if n > len(bs)*4/10 {
		t.Fatalf("可见块太多，不像「一部分」：visible=%d total=%d", n, len(bs))
	}
	// 短文档也不能整篇放开
	short := parseKnowledgeMarkdown("第一段。\n\n第二段。\n\n第三段。\n\n第四段。")
	if got := visibleBlockCount(short); got >= len(short) {
		t.Fatalf("短文档被全放开：visible=%d total=%d", got, len(short))
	}
	if visibleBlockCount(nil) != 0 {
		t.Fatalf("空文档应返回 0")
	}
}

func knowledgeTestServer(t *testing.T) *server {
	mem := newMemObjStore()
	ctx := context.Background()
	// 课号故意乱序写入：列表必须按数字排，不能按文件名字符串排
	for _, n := range []string{"21", "2", "10", "1"} {
		name := "标准日本语初级上册_第" + n + "课知识点.md"
		if err := mem.PutObject(ctx, "language-review/ja/knowledge/"+name,
			[]byte(sampleKnowledgeMD)); err != nil {
			t.Fatal(err)
		}
	}
	// 这两类不是「按课的知识点」，不能进列表
	mem.PutObject(ctx, "language-review/ja/knowledge/课本原文汇编_第1单元（基本课文）.md", []byte("# 汇编"))
	mem.PutObject(ctx, "language-review/ja/knowledge/造句易错语法速查（第12-13课）.md", []byte("# 速查"))
	return &server{storage: &Storage{store: mem, lang: "ja"}, lang: "ja", token: "tok"}
}

func TestKnowledgeListSortedByLessonNumber(t *testing.T) {
	srv := knowledgeTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleKnowledge(rec, httptest.NewRequest("GET", "/api/knowledge", nil))
	var out struct {
		Success bool `json:"success"`
		Locked  bool `json:"locked"`
		Lessons []struct {
			Lesson int    `json:"lesson"`
			Title  string `json:"title"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || len(out.Lessons) != 4 {
		t.Fatalf("列表条数不对（应只含 4 篇按课知识点）：%+v", out.Lessons)
	}
	want := []int{1, 2, 10, 21}
	for i, w := range want {
		if out.Lessons[i].Lesson != w {
			t.Fatalf("课号没按数字排序：got=%v want=%v", out.Lessons, want)
		}
	}
	if !out.Locked {
		t.Fatal("匿名请求应当 locked=true")
	}
}

func TestKnowledgeAnonymousIsPartialButSharedTokenIsFull(t *testing.T) {
	srv := knowledgeTestServer(t)

	do := func(header http.Header) (locked bool, visible, total int) {
		req := httptest.NewRequest("GET", "/api/knowledge?lesson=21", nil)
		for k, v := range header {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		srv.handleKnowledge(rec, req)
		var out struct {
			Success bool     `json:"success"`
			Locked  bool     `json:"locked"`
			Visible int      `json:"visible"`
			Blocks  []KBlock `json:"blocks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Success {
			t.Fatalf("请求失败：%s", rec.Body.String())
		}
		return out.Locked, out.Visible, len(out.Blocks)
	}

	locked, visible, total := do(nil)
	if !locked || visible >= total {
		t.Fatalf("匿名应受限：locked=%v visible=%d total=%d", locked, visible, total)
	}
	locked, visible, total = do(http.Header{"Authorization": []string{"Bearer tok"}})
	if locked || visible != total {
		t.Fatalf("共享 token 应给全文：locked=%v visible=%d total=%d", locked, visible, total)
	}
}

func TestKnowledgeUnknownLessonIs404(t *testing.T) {
	srv := knowledgeTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleKnowledge(rec, httptest.NewRequest("GET", "/api/knowledge?lesson=99", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的课号应 404，实际 %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.handleKnowledge(rec, httptest.NewRequest("GET", "/api/knowledge?lesson=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非数字课号应 400，实际 %d", rec.Code)
	}
}
