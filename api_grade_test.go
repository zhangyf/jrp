package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGradeAPI：小程序走 /api/grade 判分，口径必须跟 Go 的 GradeAnswer 一致
// （web 端是浏览器里用 web/grade.js 本地判，两边靠同一套规则对齐）。
func TestGradeAPI(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}

	post := func(body string) map[string]interface{} {
		req := httptest.NewRequest(http.MethodPost, "/api/grade", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.withUser(srv.handleGrade)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		var out map[string]interface{}
		if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析返回失败：%v", err)
		}
		return out
	}

	// 任一档：写假名或写汉字都算对
	out := post(`{"mode":"either","items":[
		{"word":"すし(寿司)","answer":"すし"},
		{"word":"すし(寿司)","answer":"寿司"},
		{"word":"すし(寿司)","answer":"すし(寿司)"},
		{"word":"すし(寿司)","answer":"さかな"},
		{"word":"すし(寿司)","answer":""}
	]}`)
	got := out["results"].([]interface{})
	want := []bool{true, true, true, false, false}
	if len(got) != len(want) {
		t.Fatalf("结果条数 %d，要 %d", len(got), len(want))
	}
	for i := range want {
		if got[i].(bool) != want[i] {
			t.Errorf("第 %d 题：got %v want %v", i+1, got[i], want[i])
		}
	}
	if out["mode"] != "either" {
		t.Errorf("mode 回显 %v", out["mode"])
	}

	// 假名档（默认）：只写汉字不算对
	out = post(`{"items":[{"word":"すし(寿司)","answer":"寿司"}]}`)
	if out["results"].([]interface{})[0].(bool) {
		t.Error("假名档下只写汉字不该判对")
	}
	if out["mode"] != "kana" {
		t.Errorf("不传 mode 应回落 kana，实际 %v", out["mode"])
	}

	// 空 items 不能 panic
	out = post(`{"mode":"either","items":[]}`)
	if _, ok := out["results"]; !ok {
		t.Error("空 items 也该返回 results 字段")
	}

	// GET 要被拒
	req := httptest.NewRequest(http.MethodGet, "/api/grade", nil)
	w := httptest.NewRecorder()
	srv.withUser(srv.handleGrade)(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 应 405，实际 %d", w.Code)
	}
}
