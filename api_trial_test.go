package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 试用凭证是公开的，白名单就是唯一的门。加路由进来必须过这个测试。
func TestTrialAllowedWhitelist(t *testing.T) {
	allow := []string{"/api/plan", "/api/hard", "/api/grade", "/api/auth/me",
		"/api/review", "/api/draft", "/api/judge-sentence"}
	for _, p := range allow {
		if !trialAllowed(p) {
			t.Errorf("%s 应该放行", p)
		}
	}
	// 这几个都会带出老师的个人数据，绝不能开
	deny := []string{
		"/api/lexicon", "/api/stats", "/api/words", "/api/record",
		"/api/word-def", "/api/vision",
		"/api/conjugation", "/api/offline", "/api/admin/users",
	}
	for _, p := range deny {
		if trialAllowed(p) {
			t.Errorf("%s 不该对体验版开放", p)
		}
	}
}

// 直接往会话表塞一份试用会话，绕开 createSession 的异步落盘（测试里没有真 COS）。
func newTrialSession(srv *server) string {
	sid := "trial-test-sid"
	sessions.Lock()
	sessions.m[sid] = &session{
		UID: trialUID, Role: "trial", Name: "体验用户",
		Created: time.Now(), LastSeen: time.Now(),
	}
	sessions.Unlock()
	return sid
}

func trialRequest(srv *server, sid, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w := httptest.NewRecorder()
	return w
}

// 体验版拉计划：给固定题面 + read_only，且不碰 COS（storage 是空的也照样过，
// 说明它没去下载档案）。
func TestTrialPlanReturnsFixedWords(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}
	sid := newTrialSession(srv)

	req := httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w := httptest.NewRecorder()
	srv.withUser(srv.handlePlan)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if out["trial"] != true {
		t.Error("应带 trial:true，前端靠它切模式")
	}
	if out["read_only"] != true {
		t.Error("体验版必须 read_only，前端据此关掉提交")
	}
	words, _ := out["words"].([]interface{})
	if len(words) != 5 {
		t.Errorf("每日复习应 5 词，实际 %d", len(words))
	}

	// 钉子户：另一套固定题
	req = httptest.NewRequest(http.MethodGet, "/api/hard", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w = httptest.NewRecorder()
	srv.withUser(srv.handleHard)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("hard HTTP %d: %s", w.Code, w.Body.String())
	}
	out = map[string]interface{}{}
	if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got, _ := out["words"].([]interface{}); len(got) != 5 {
		t.Errorf("钉子户应 5 词，实际 %d", len(got))
	}

	// 造句：3 句，且必须是课本原文（不能是自造的）
	req = httptest.NewRequest(http.MethodGet, "/api/plan?mode=sentences", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w = httptest.NewRecorder()
	srv.withUser(srv.handlePlan)(w, req)
	out = map[string]interface{}{}
	if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got, _ := out["sentences"].([]interface{}); len(got) != 3 {
		t.Errorf("造句应 3 句，实际 %d", len(got))
	}
}

// 白名单外的路由一律 403 —— 这是体验版的安全底线。
func TestTrialBlockedOutsideWhitelist(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}
	sid := newTrialSession(srv)

	reached := false
	guard := func(w http.ResponseWriter, r *http.Request, st *Storage) { reached = true }

	req := httptest.NewRequest(http.MethodGet, "/api/lexicon", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w := httptest.NewRecorder()
	srv.withUser(guard)(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("体验版拉词库应 403，实际 %d", w.Code)
	}
	if reached {
		t.Error("403 了就不许再进 handler")
	}
}

// 领试用身份：匿名可领，不写用户表（写进去会牵动 authRequired 和后台用户列表）。
func TestAuthTrialIssuesSession(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/trial", nil)
	w := httptest.NewRecorder()
	srv.handleAuthTrial(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if out["uid"] != trialUID || out["role"] != "trial" {
		t.Errorf("身份不对：uid=%v role=%v", out["uid"], out["role"])
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("没发 token")
	}
	sessions.Lock()
	_, ok := sessions.m[token]
	sessions.Unlock()
	if !ok {
		t.Error("token 应该真的建了会话")
	}
}

// 固定题是不变的 —— 老师明确要求「不需要轮换」。这条盯着别被改成动态取词。
func TestTrialQuestionsAreStable(t *testing.T) {
	a := trialPlanWords("words")
	b := trialPlanWords("words")
	if len(a) != len(b) {
		t.Fatal("两次取的题数不一样")
	}
	for i := range a {
		if a[i].Word != b[i].Word || a[i].Definition != b[i].Definition {
			t.Errorf("第 %d 题两次不一样：%q vs %q", i+1, a[i].Word, b[i].Word)
		}
	}
	// 改副本不能污染全局
	a[0].Word = "改坏了"
	if trialReviewWords[0].Word == "改坏了" {
		t.Error("trialPlanWords 必须返回副本，否则调用方能改到全局题面")
	}
	// 每日复习和钉子户不能是同一批
	if len(trialReviewWords) > 0 && len(trialHardWords) > 0 &&
		trialReviewWords[0].Word == trialHardWords[0].Word {
		t.Error("每日复习和钉子户用了同一批词")
	}
}
