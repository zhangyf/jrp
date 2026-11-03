package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 知识点 HTTP 测试保留目录排序、匿名/体验版权限、全文访问和错误响应断言。
// markdown 纯测试迁至 internal/knowledge；这里保留相同样本供内存存储夹具使用。

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

	// owner：uid 恒为空（零迁移约定）。2026-11-03 的真事故 —— 这里曾经把
	// uid=="" 当成匿名挡掉，结果小程序里「每日复习已登录、知识点还是未登录」。
	// 同一个会话走 withUser 的页面全是登录态，只有这条自己认会话的路由被挡。
	sid := "owner-sid-" + time.Now().Format("150405.000000000")
	sessions.Lock()
	sessions.m[sid] = &session{UID: "", Role: "owner", Created: time.Now(), LastSeen: time.Now()}
	sessions.Unlock()
	locked, visible, total = do(http.Header{"Authorization": []string{"Bearer " + sid}})
	if locked || visible != total {
		t.Fatalf("owner（uid 为空）应给全文：locked=%v visible=%d total=%d", locked, visible, total)
	}

	// 体验版：uid 是 trial，凭证是公开的，一律受限
	tsid := "trial-sid-" + time.Now().Format("150405.000000000")
	sessions.Lock()
	sessions.m[tsid] = &session{UID: trialUID, Role: "trial", Created: time.Now(), LastSeen: time.Now()}
	sessions.Unlock()
	locked, visible, total = do(http.Header{"Authorization": []string{"Bearer " + tsid}})
	if !locked || visible >= total {
		t.Fatalf("体验版应受限：locked=%v visible=%d total=%d", locked, visible, total)
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
