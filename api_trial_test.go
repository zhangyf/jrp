package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 试用凭证是公开的，白名单就是唯一的门。加路由进来必须过这个测试。
func TestTrialAllowedWhitelist(t *testing.T) {
	allow := []string{"/api/plan", "/api/hard", "/api/grade", "/api/auth/me",
		"/api/review", "/api/draft", "/api/judge-sentence",
		"/api/stats", "/api/lexicon", "/api/conjugation"}
	for _, p := range allow {
		if !trialAllowed(p) {
			t.Errorf("%s 应该放行", p)
		}
	}
	// ⚠️ stats / lexicon / conjugation 已经移出 deny —— 它们对 trial 走
	// writeTrial* 分支返回内置假数据（见 TestTrialReadonlySections）。
	// 下面这几个仍然一律挡：要么写数据，要么带出老师的个人信息。
	deny := []string{
		"/api/words", "/api/record", "/api/word-def", "/api/vision",
		"/api/offline", "/api/admin/users", "/api/knowledge",
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

	// 写接口永远该挡：拿 /api/record 当代表
	req := httptest.NewRequest(http.MethodPost, "/api/record", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w := httptest.NewRecorder()
	srv.withUser(guard)(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("体验版回写成绩应 403，实际 %d", w.Code)
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

// 统计 / 词汇总表 / 活用：体验版拿的是内置假数据，结构得跟真接口一致，
// 否则前端那几页会整块空白（前端不认 trial，全靠结构对齐）。
func TestTrialReadonlySections(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}
	sid := newTrialSession(srv)

	// 统计：snapshots 必须按日期升序（前端取首尾算增量），天数跟请求一致
	req := httptest.NewRequest(http.MethodGet, "/api/stats?days=7", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w := httptest.NewRecorder()
	srv.withUser(srv.handleStats)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("stats HTTP %d: %s", w.Code, w.Body.String())
	}
	var st map[string]interface{}
	if err := jsonUnmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	snaps, _ := st["snapshots"].([]interface{})
	if len(snaps) != 7 {
		t.Fatalf("要 7 个存档点，实际 %d", len(snaps))
	}
	first, _ := snaps[0].(map[string]interface{})
	last, _ := snaps[len(snaps)-1].(map[string]interface{})
	if d1, _ := first["date"].(string); d1 == "" {
		t.Error("存档点要有日期")
	}
	if a, _ := first["date"].(string); a >= func() string { b, _ := last["date"].(string); return b }() {
		t.Errorf("snapshots 必须升序：%v → %v", first["date"], last["date"])
	}
	if tot, _ := last["total"].(float64); tot == 0 {
		t.Error("最后一个存档点要有总词量，否则 KPI 全 0")
	}
	// 前端 DIST_ORDER 五档，少一档整块不显示
	out := map[string]interface{}{}

	req = httptest.NewRequest(http.MethodGet, "/api/lexicon", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w = httptest.NewRecorder()
	srv.withUser(srv.handleLexicon)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("lexicon HTTP %d: %s", w.Code, w.Body.String())
	}
	if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	items, _ := out["items"].([]interface{})
	if len(items) != 10 {
		t.Fatalf("词汇总表应 10 行，实际 %d", len(items))
	}
	row0, _ := items[0].(map[string]interface{})
	if row0["kana"] == nil || row0["def"] == nil {
		t.Error("行的 kana / def 不能为空，页面上就靠这两列")
	}

	// 活用：三种类型各 2 题，label 要跟真接口一致
	for _, typ := range []string{"verb", "verb_nai", "adj"} {
		out = map[string]interface{}{}
		req = httptest.NewRequest(http.MethodGet, "/api/conjugation?type="+typ, nil)
		req.Header.Set("Authorization", "Bearer "+sid)
		w = httptest.NewRecorder()
		srv.withUser(srv.handleConjugation)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("conjugation(%s) HTTP %d: %s", typ, w.Code, w.Body.String())
		}
		if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析失败：%v", err)
		}
		got, _ := out["items"].([]interface{})
		if len(got) != 2 {
			t.Errorf("%s 应 2 题，实际 %d", typ, len(got))
		}
		it0, _ := got[0].(map[string]interface{})
		tasks, _ := it0["tasks"].([]interface{})
		if len(tasks) == 0 {
			t.Errorf("%s 第一题没有 tasks", typ)
		}
		tk0, _ := tasks[0].(map[string]interface{})
		if tk0["label"] == nil || tk0["answers"] == nil {
			t.Errorf("%s 的 task 缺 label/answers", typ)
		}
	}
}

// newPendingUser 造一个 pending 用户 + 会话。daysAgo 控制他是哪天注册的。
func newPendingUser(t *testing.T, uid, sid string, daysAgo int) {
	t.Helper()
	created := time.Now().AddDate(0, 0, -daysAgo).Format("2006-01-02 15:04:05")
	seedUsers(t, []User{{UID: uid, Role: rolePending, Name: "等批准的人", Created: created}})
	sessions.Lock()
	sessions.m[sid] = &session{
		UID: uid, Role: rolePending, Name: "等批准的人",
		Created: time.Now(), LastSeen: time.Now(),
	}
	sessions.Unlock()
}

// pending 的 7 个自然日体验期：期内**完整权限**（uid 是自己的，不是 trial），
// 到期一律 403，且下次登录照样挡。
func TestPendingGracePeriod(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}

	// --- 刚注册：放行，身份是他自己（不是体验版） ---
	newPendingUser(t, "u_fresh", "fresh-sid", 0)
	defer seedUsers(t, nil)

	gotUID := ""
	guard := func(w http.ResponseWriter, r *http.Request, st *Storage) {
		gotUID = st.uid
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
	}
	req := httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer fresh-sid")
	w := httptest.NewRecorder()
	srv.withUser(guard)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("体验期内应放行，实际 %d: %s", w.Code, w.Body.String())
	}
	if gotUID != "u_fresh" {
		t.Errorf("体验期内应是他自己的 uid，实际 %q（走 trial 就错了）", gotUID)
	}

	// auth/me 要报 pending + 剩余天数，前端才好在首页摆一句
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer fresh-sid")
	w = httptest.NewRecorder()
	srv.withUser(srv.handleAuthMe)(w, req)
	var out map[string]interface{}
	if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if out["pending"] != true || out["role"] != rolePending {
		t.Errorf("应报 pending，实际 role=%v pending=%v", out["role"], out["pending"])
	}
	if d, _ := out["days_left"].(float64); d != pendingGraceDays {
		t.Errorf("刚注册应还剩 %d 天，实际 %v", pendingGraceDays, d)
	}

	// --- 第 6 天：还在期内 ---
	newPendingUser(t, "u_d6", "d6-sid", 6)
	req = httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer d6-sid")
	w = httptest.NewRecorder()
	srv.withUser(guard)(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("第 6 天还在体验期内，实际 %d", w.Code)
	}

	// --- 第 8 天：过期，一律 403 ---
	newPendingUser(t, "u_d8", "d8-sid", 8)
	req = httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer d8-sid")
	w = httptest.NewRecorder()
	srv.withUser(guard)(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("过了 7 天应 403，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "审核") {
		t.Errorf("403 要写清是没审核，实际：%s", w.Body.String())
	}

	// --- 边界：正好第 7 天算过期 ---
	newPendingUser(t, "u_d7", "d7-sid", 7)
	req = httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer d7-sid")
	w = httptest.NewRecorder()
	srv.withUser(guard)(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("第 7 天应已过期（7 个自然日 = 到第 7 天结束），实际 %d", w.Code)
	}
}

// 网页扫码那条路（/api/auth/status）也得挡：小程序端挡住了不代表网页端能漏。
func TestPendingExpiredCannotCompleteWebLogin(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	defer seedUsers(t, nil)

	login := func(daysAgo int) *httptest.ResponseRecorder {
		uid := "u_web"
		seedUsers(t, []User{
			{UID: uid, Role: rolePending, Created: time.Now().AddDate(0, 0, -daysAgo).Format("2006-01-02 15:04:05")},
		})
		tk := newLoginTicket()
		if !confirmTicket(tk.Ticket, uid) {
			t.Fatal("确认失败")
		}
		req := httptest.NewRequest(http.MethodGet, "/api/auth/status?ticket="+tk.Ticket, nil)
		req.AddCookie(&http.Cookie{Name: pollCookie, Value: tk.PollToken})
		rec := httptest.NewRecorder()
		srv.handleAuthStatus(rec, req)
		return rec
	}

	// 期内：正常发会话
	rec := login(3)
	if rec.Code != http.StatusOK {
		t.Fatalf("体验期内网页该能登录，got %d（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), sidCookie+"=") {
		t.Error("期内该下发会话 cookie")
	}

	// 过期：403 且不下发会话
	rec = login(8)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("过期 pending 扫完码该被拒，got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "审核") {
		t.Errorf("403 要写清是没审核，实际：%s", rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Set-Cookie"), sidCookie+"=") {
		t.Error("过期不该下发会话 cookie")
	}
}

// 天数计算按**自然日**，不是 7×24 小时 —— 晚上注册的别第二天就少一天。
func TestPendingDaysLeftCountsCalendarDays(t *testing.T) {
	now := time.Now()
	mk := func(daysAgo int, hour int) User {
		d := now.AddDate(0, 0, -daysAgo)
		return User{Created: time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, time.Local).
			Format("2006-01-02 15:04:05")}
	}
	if got := pendingDaysLeft(mk(0, 23)); got != 7 {
		t.Errorf("今晚 23 点注册应还剩 7 天，实际 %d", got)
	}
	if got := pendingDaysLeft(mk(3, 1)); got != 4 {
		t.Errorf("3 天前凌晨注册应还剩 4 天，实际 %d", got)
	}
	// 老数据没 created：按满格给，不误伤
	if got := pendingDaysLeft(User{}); got != pendingGraceDays {
		t.Errorf("没 created 应按满格给，实际 %d", got)
	}
	if pendingExpired(User{Created: "不是时间"}) {
		t.Error("时间串解析不了不该判过期")
	}
}

// 冻结的照样挡在外面 —— 这条不能因为上面的放宽被带偏。
func TestDisabledUserStillBlocked(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}

	seedUsers(t, []User{{UID: "u_bad", Role: roleDisabled, Name: "被冻结的"}})
	defer seedUsers(t, nil)

	sid := "disabled-sid"
	sessions.Lock()
	sessions.m[sid] = &session{
		UID: "u_bad", Role: roleDisabled, Name: "被冻结的",
		Created: time.Now(), LastSeen: time.Now(),
	}
	sessions.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer "+sid)
	w := httptest.NewRecorder()
	srv.withUser(srv.handlePlan)(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("冻结账号应 403，实际 %d", w.Code)
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
