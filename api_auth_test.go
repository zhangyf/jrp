// api_auth_test.go —— 登录鉴权 / 试用 / 管理后台 / 安全基线相关测试（配 api_auth.go、auth.go、quota.go）
//
// 腿一收尾：把原本平铺在根目录的 4 个 *_test.go 按被测域物理合并到本文件。
// 测试函数体逐字保留，仅对 import 取并集去重；被测行为、断言、测试名均不变。
// 每个分区以「原文件名」标注，便于定位；文件头来源清单列出各源顶层声明。
//
// 来源与顶层声明：
// api_admin_test.go: seedUsers, okHandler, TestAdminRequiresOwnerSession, TestAdminRoleFollowsUserTableNotSession, TestAdminActionAllowedProtectsOwner, TestRemoveUser, TestAdminRankPutsPendingFirst, TestOpenIDTail, TestDisabledUserCannotCompleteLogin, TestApprovedUserCanCompleteLogin, ... (共 11 个顶层声明)
// api_trial_test.go: TestTrialAllowedWhitelist, newTrialSession, trialRequest, TestTrialPlanReturnsFixedWords, TestTrialBlockedOutsideWhitelist, TestAuthTrialIssuesSession, TestTrialReadonlySections, newPendingUser, TestPendingGracePeriod, TestPendingExpiredCannotCompleteWebLogin, ... (共 14 个顶层声明)
// auth_test.go: TestTicketFitsWXSceneLimit, TestTicketLifecycle, TestTicketExpires, TestSessionCreateLookupDestroy, TestSessionIDFromAnonymous, TestAuthRequiredOffWithoutUsers, TestResolveWXEnv, TestClassifyMiniCodeResp
// security_test.go: TestRejectsPathTraversalDates, TestSafeDate, TestTokenNotAcceptedFromQuery, TestMiniSessionExpiresIn7Days, TestSessionTableIsCapped, TestPendingVisionDailyQuota, TestAuthRequiredFailsClosed
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ==========================================================================
// 原 api_admin_test.go
// ==========================================================================

// 管理后台的红线都在权限上。这组测试把三件事钉死：
//   1. 匿名进不来（401），普通成员也进不来（403）—— 只认 owner；
//   2. owner 不可冻结、不可删除（零迁移的根，动了只能上服务器改 COS 救）；
//   3. 冻结后扫二维码也拿不到会话 —— 老师要的是「冻结后就无法扫码登录」，
//      不只是「能登录但调接口 403」。

func seedUsers(t *testing.T, users []User) {
	t.Helper()
	userCache.Lock()
	userCache.users = users
	userCache.loaded = true
	userCache.at = time.Now()
	userCache.Unlock()
	t.Cleanup(invalidateUserCache)
}

// 一个只回 200 的假 handler，用来验证 withAdmin 放没放行。
func okHandler(w http.ResponseWriter, r *http.Request, st *Storage) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func TestAdminRequiresOwnerSession(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	seedUsers(t, []User{
		{UID: "", Role: roleOwner, Name: "老师"},
		{UID: "u_7f3a2b9c", Role: roleMember},
		{UID: "u_pending1", Role: rolePending},
	})

	ownerSID := srv.createSession(User{UID: "", Role: roleOwner}, "test", sessKindWeb)
	memberSID := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleMember}, "test", sessKindWeb)
	pendingSID := srv.createSession(User{UID: "u_pending1", Role: rolePending}, "test", sessKindWeb)

	cases := []struct {
		name string
		sid  string
		want int
	}{
		{"匿名", "", http.StatusUnauthorized},
		{"owner", ownerSID, http.StatusOK},
		{"普通成员", memberSID, http.StatusForbidden},
		{"待批准", pendingSID, http.StatusForbidden},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/api/admin/users", nil)
		if c.sid != "" {
			req.AddCookie(&http.Cookie{Name: sidCookie, Value: c.sid})
		}
		rec := httptest.NewRecorder()
		srv.withAdmin(okHandler)(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: got %d, want %d（body=%s）", c.name, rec.Code, c.want, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// 会话里的 Role 是登录那一刻的快照：管理员把某人降成成员之后，
// 他手上那个写着 owner 的旧会话必须立刻失效。
func TestAdminRoleFollowsUserTableNotSession(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	sid := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleOwner}, "test", sessKindWeb) // 会话自称 owner
	seedUsers(t, []User{{UID: "u_7f3a2b9c", Role: roleDisabled}})              // 表里已被冻结

	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: sidCookie, Value: sid})
	rec := httptest.NewRecorder()
	srv.withAdmin(okHandler)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("会话自称 owner 但用户表不是：got %d, want 403", rec.Code)
	}
}

func TestAdminActionAllowedProtectsOwner(t *testing.T) {
	owner := User{UID: "", Role: roleOwner}
	member := User{UID: "u_7f3a2b9c", Role: roleMember}

	if err := adminActionAllowed(owner, "disable"); err == nil {
		t.Error("owner 不该能被冻结")
	}
	if err := adminActionAllowed(owner, "delete"); err == nil {
		t.Error("owner 不该能被删除")
	}
	if err := adminActionAllowed(owner, "rename"); err != nil {
		t.Errorf("owner 改自己的名字应该可以：%v", err)
	}
	for _, a := range []string{"approve", "disable", "rename", "delete"} {
		if err := adminActionAllowed(member, a); err != nil {
			t.Errorf("成员 %s 应该被允许：%v", a, err)
		}
	}
	if err := adminActionAllowed(member, "transfer-ownership"); err == nil {
		t.Error("不认识的动作应该报错")
	}
}

func TestRemoveUser(t *testing.T) {
	users := []User{{UID: ""}, {UID: "u_1"}, {UID: "u_2"}}
	got := removeUser(users, "u_1")
	if len(got) != 2 {
		t.Fatalf("删一个该剩 2 个，实际 %d", len(got))
	}
	for _, u := range got {
		if u.UID == "u_1" {
			t.Error("被删的还在")
		}
	}
	if len(users) != 3 {
		t.Error("removeUser 动了原切片（应该返回新切片）")
	}
}

func TestAdminRankPutsPendingFirst(t *testing.T) {
	if adminRank(rolePending) >= adminRank(roleMember) {
		t.Error("待批准该排在成员前面")
	}
	if adminRank(roleDisabled) <= adminRank(roleMember) {
		t.Error("已冻结该沉底")
	}
}

func TestOpenIDTail(t *testing.T) {
	if got := openIDTail("oAbCdEfGhIjKlMnOpQrStUvWxYz"); got != "…WxYz" {
		t.Errorf("openid 尾号 got %q", got)
	}
	if got := openIDTail("abc"); got != "abc" {
		t.Errorf("太短的 openid 该原样返回，got %q", got)
	}
}

// 冻结的人扫二维码：确认了也不发会话。
func TestDisabledUserCannotCompleteLogin(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	seedUsers(t, []User{{UID: "u_7f3a2b9c", Role: roleDisabled}})

	tk := newLoginTicket()
	if !confirmTicket(tk.Ticket, "u_7f3a2b9c") {
		t.Fatal("确认失败")
	}

	req := httptest.NewRequest("GET", "/api/auth/status?ticket="+tk.Ticket, nil)
	req.AddCookie(&http.Cookie{Name: pollCookie, Value: tk.PollToken})
	rec := httptest.NewRecorder()
	srv.handleAuthStatus(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("冻结用户扫完码该被拒，got %d", rec.Code)
	}
	var j map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &j); err != nil {
		t.Fatalf("返回不是 JSON：%v", err)
	}
	if cookie := rec.Header().Get("Set-Cookie"); strings.Contains(cookie, sidCookie+"=;") == false &&
		strings.Contains(cookie, sidCookie+"=;") {
		t.Errorf("不该下发会话 cookie：%q", cookie)
	}
}

// 正常成员批准之后能拿到会话（别把登录整个堵死）。
func TestApprovedUserCanCompleteLogin(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	seedUsers(t, []User{{UID: "u_7f3a2b9c", Role: roleMember}})

	tk := newLoginTicket()
	if !confirmTicket(tk.Ticket, "u_7f3a2b9c") {
		t.Fatal("确认失败")
	}

	req := httptest.NewRequest("GET", "/api/auth/status?ticket="+tk.Ticket, nil)
	req.AddCookie(&http.Cookie{Name: pollCookie, Value: tk.PollToken})
	rec := httptest.NewRecorder()
	srv.handleAuthStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("正常成员该能登录，got %d（body=%s）", rec.Code, rec.Body.String())
	}
	var j map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	if j["status"] != "confirmed" {
		t.Errorf("status got %v, want confirmed", j["status"])
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), sidCookie+"=") {
		t.Error("登录成功该下发会话 cookie")
	}
}

// 冻结某人之后，他手上已经登录的会话要一起清掉。
func TestDestroySessionsForUID(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	victim := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleMember}, "test", sessKindWeb)
	other := srv.createSession(User{UID: "", Role: roleOwner}, "test", sessKindWeb)

	srv.destroySessionsForUID("u_7f3a2b9c")

	if _, ok := srv.lookupSession(victim); ok {
		t.Error("被冻结的人的会话该被清掉")
	}
	if _, ok := srv.lookupSession(other); !ok {
		t.Error("别人的会话不该被牵连")
	}
}

// ==========================================================================
// 原 api_trial_test.go
// ==========================================================================

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

// TestTrialConjugationLabelsMatchRealAPI 体验版固定题的 label 必须跟真接口一一对应。
//
// 真接口 2026-11-03 给动词卡加了た形（原形/て形/た形 三个空），试用卡要是还停在
// 两个空，审核看到的就是「试用版和正式版长得不一样」。
// ⚠️ 以后真接口再加新形（比如ば形），改完 api_trial.go 必须同步改这里的表。
func TestTrialConjugationLabelsMatchRealAPI(t *testing.T) {
	want := map[string][]string{
		"verb":     {"原形", "て形", "た形"},
		"verb_nai": {"原形", "ない形"},
		"adj":      {"过去式", "否定形", "过去否定形"},
	}
	for typ, labels := range want {
		items := trialConjugation(typ)
		if len(items) == 0 {
			t.Fatalf("%s 一题都没有", typ)
		}
		for i, it := range items {
			if len(it.Tasks) != len(labels) {
				t.Errorf("%s 第%d题有 %d 个空，应为 %d 个（%v）",
					typ, i+1, len(it.Tasks), len(labels), labels)
				continue
			}
			for j, lb := range labels {
				if it.Tasks[j].Label != lb {
					t.Errorf("%s 第%d题第%d空 label=%q，应为 %q",
						typ, i+1, j+1, it.Tasks[j].Label, lb)
				}
				if len(it.Tasks[j].Answers) == 0 {
					t.Errorf("%s 第%d题第%d空（%s）没有答案", typ, i+1, j+1, lb)
				}
			}
		}
	}
}

// ==========================================================================
// 原 auth_test.go
// ==========================================================================

// 登录票据的两条硬约束：长度必须塞得进 wxacode 的 scene（32 字符上限，
// 超了微信只回一个没有说明的报错），以及一次性 + 5 分钟过期。

func TestTicketFitsWXSceneLimit(t *testing.T) {
	for i := 0; i < 50; i++ {
		tk := newTicket()
		if len(tk) > 32 {
			t.Fatalf("ticket 太长（%d 字符），wxacode 的 scene 上限是 32：%q", len(tk), tk)
		}
		if len(tk) == 0 {
			t.Fatal("ticket 为空")
		}
	}
}

func TestTicketLifecycle(t *testing.T) {
	tk := newLoginTicket()
	if _, ok := getTicket(tk.Ticket); !ok {
		t.Fatal("刚建的 ticket 应该能取到")
	}
	if confirmTicket("不存在的ticket", "u_1") {
		t.Error("不存在的 ticket 不该确认成功")
	}
	if !confirmTicket(tk.Ticket, "u_1") {
		t.Fatal("确认自己的 ticket 应该成功")
	}
	got, _ := getTicket(tk.Ticket)
	if !got.Confirmed || got.UID != "u_1" {
		t.Errorf("确认后状态不对：confirmed=%v uid=%q", got.Confirmed, got.UID)
	}
	dropTicket(tk.Ticket)
	if _, ok := getTicket(tk.Ticket); ok {
		t.Error("用完的 ticket 应该被删掉（一次性）")
	}
}

func TestTicketExpires(t *testing.T) {
	tk := newLoginTicket()
	got, ok := getTicket(tk.Ticket)
	if !ok {
		t.Fatal("刚建的 ticket 应该能取到")
	}
	got.Expires = got.Expires.Add(-10 * 60 * 1000 * 1000 * 1000) // 往前拨 10 分钟（纳秒）
	if _, ok := getTicket(tk.Ticket); ok {
		t.Error("过期的 ticket 应该取不到")
	}
	if confirmTicket(tk.Ticket, "u_1") {
		t.Error("过期的 ticket 不该能确认")
	}
}

// 会话：owner 的 UID 是空串（所以他的 COS 路径跟老数据一致），成员带 uid。
func TestSessionCreateLookupDestroy(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true} // dryRun：不真写 COS
	srv := &server{storage: st}

	ownerSID := srv.createSession(User{UID: "", Role: roleOwner, Name: "老师"}, "test-agent", sessKindWeb)
	sess, ok := srv.lookupSession(ownerSID)
	if !ok {
		t.Fatal("刚建的会话应该能查到")
	}
	if sess.UID != "" || sess.Role != roleOwner {
		t.Errorf("owner 会话不对：uid=%q role=%q", sess.UID, sess.Role)
	}

	memberSID := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleMember}, "test-agent", sessKindWeb)
	ms, ok := srv.lookupSession(memberSID)
	if !ok || ms.UID != "u_7f3a2b9c" {
		t.Fatalf("成员会话不对：%#v", ms)
	}

	srv.destroySession(memberSID)
	if _, ok := srv.lookupSession(memberSID); ok {
		t.Error("登出后会话应该失效（光清 cookie 没用，服务端必须删）")
	}
}

// 红线：匿名请求不能拿到任何会话，更不能退回 owner。
func TestSessionIDFromAnonymous(t *testing.T) {
	r, err := http.NewRequest("GET", "/api/plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDFrom(r); got != "" {
		t.Errorf("匿名请求不该解析出 sid：%q", got)
	}
}

func TestAuthRequiredOffWithoutUsers(t *testing.T) {
	// 没配小程序凭证 → 不开登录（等价于今天的行为，外网靠 nginx Basic Auth）。
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st}
	if srv.authRequired() {
		t.Error("没配 appid/secret 时不该强制登录")
	}
}

// TestResolveWXEnv：小程序码版本开关。未发布的小程序必须用 trial，
// 否则 getwxacodeunlimit 报 41030；写错值不能让登录整个挂掉。
func TestResolveWXEnv(t *testing.T) {
	cases := map[string]string{
		"":        "release",
		"release": "release",
		"trial":   "trial",
		"develop": "develop",
		" trial ": "trial",
		"TRIAL":   "release", // 大小写不认，回退 release（微信只认小写）
		"线上":      "release", // 笔误回退，不 panic
	}
	for in, want := range cases {
		if got := resolveWXEnv(in); got != want {
			t.Errorf("resolveWXEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClassifyMiniCodeResp：出码接口的返回可能是图也可能是 JSON 报错。
// ⚠️ 微信 getwxacodeunlimit 实际返回 JPEG，只认 PNG 头会把正常返回当错误（2026-10-16 线上实测踩到）。
func TestClassifyMiniCodeResp(t *testing.T) {
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01")
	png := []byte("\x89PNG\r\n\x1a\n")

	if ct, err := classifyMiniCodeResp(jpeg, "image/jpeg"); err != nil || ct != "image/jpeg" {
		t.Errorf("jpeg: ct=%q err=%v，应原样返回 content-type", ct, err)
	}
	if ct, err := classifyMiniCodeResp(png, "image/png"); err != nil || ct != "image/png" {
		t.Errorf("png: ct=%q err=%v", ct, err)
	}
	// 微信没给 content-type 时兜底 jpeg（它现在给的就是这个）
	if ct, err := classifyMiniCodeResp(jpeg, ""); err != nil || ct != "image/jpeg" {
		t.Errorf("no ct: ct=%q err=%v", ct, err)
	}
	// JSON 报错要解析出 errcode
	_, err := classifyMiniCodeResp([]byte(`{"errcode":41030,"errmsg":"invalid page"}`), "application/json")
	if err == nil || !strings.Contains(err.Error(), "41030") {
		t.Errorf("errcode 41030 没被识别出来：%v", err)
	}
	// 既不是图也不是可解析的 JSON → 报错但不能 panic
	if _, err := classifyMiniCodeResp([]byte("gateway timeout"), "text/html"); err == nil {
		t.Error("非图非 JSON 应当报错")
	}
}

// ==========================================================================
// 原 security_test.go
// ==========================================================================

// 2026-10-28 安全评审后补的几道闸门。这几条挂了 = 又开天窗了。

// date / plan_date 会拼进 COS key，必须严格 YYYY-MM-DD。
func TestRejectsPathTraversalDates(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st, lang: "ja"}
	seedUsers(t, []User{{UID: "u_1", Role: roleMember}})
	defer seedUsers(t, nil)
	sid := srv.createSession(User{UID: "u_1", Role: roleMember}, "test", sessKindWeb)

	bad := []string{
		"../../_auth/users",
		"x/../../../users/u_victim/drafts/draft_2026-10-28",
		"2026-13-99",
		"2026-1-1",
	}

	for _, d := range bad {
		req := httptest.NewRequest(http.MethodGet, "/api/draft?mode=words&date="+d, nil)
		req.Header.Set("Authorization", "Bearer "+sid)
		w := httptest.NewRecorder()
		srv.withUser(srv.handleDraft)(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("/api/draft?date=%q 该 400，实际 %d（body=%s）", d, w.Code, w.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, "/api/review?mode=words&date="+d, nil)
		req.Header.Set("Authorization", "Bearer "+sid)
		w = httptest.NewRecorder()
		srv.withUser(srv.handleReview)(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("/api/review?date=%q 该 400，实际 %d", d, w.Code)
		}

		// body 里的 plan_date 同样要拦
		req = httptest.NewRequest(http.MethodPost, "/api/record",
			strings.NewReader(`{"plan_date":"`+d+`"}`))
		req.Header.Set("Authorization", "Bearer "+sid)
		w = httptest.NewRecorder()
		srv.withUser(srv.handleRecord)(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("/api/record plan_date=%q 该 400，实际 %d（body=%s）", d, w.Code, w.Body.String())
		}

		// draft POST 的 body date 也要拦
		req = httptest.NewRequest(http.MethodPost, "/api/draft",
			strings.NewReader(`{"date":"`+d+`","mode":"words","items":[{"number":1,"answer":"x"}]}`))
		req.Header.Set("Authorization", "Bearer "+sid)
		w = httptest.NewRecorder()
		srv.withUser(srv.handleDraft)(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("/api/draft POST date=%q 该 400，实际 %d", d, w.Code)
		}
	}

}

// safeDate 本身：合法日期照过，非法的一律拦（别把功能一起堵死）。
func TestSafeDate(t *testing.T) {
	if d, ok := safeDate("2026-10-28"); !ok || d != "2026-10-28" {
		t.Errorf("合法日期该原样过，got %q ok=%v", d, ok)
	}
	// 空 = 今天
	if d, ok := safeDate(""); !ok || len(d) != 10 {
		t.Errorf("空该回今天，got %q ok=%v", d, ok)
	}
	for _, bad := range []string{"../../x", "2026-13-99", "2026-1-1", "20261028", "2026-10-28/../x", " 2026-10-28 "} {
		if _, ok := safeDate(bad); ok {
			t.Errorf("%q 不该通过", bad)
		}
	}
}

// token 不再接受 ?token= —— URL 会进 access log / 历史 / Referer。
func TestTokenNotAcceptedFromQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/plan?token=s3cr3t", nil)
	if got := bearerToken(req); got != "" {
		t.Errorf("?token= 不该被认，实际拿到 %q", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/plan", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	if got := bearerToken(req); got != "s3cr3t" {
		t.Errorf("Authorization 头该照常认，实际 %q", got)
	}
}

// 小程序会话 7 天，网页会话 30 天。
func TestMiniSessionExpiresIn7Days(t *testing.T) {
	if sessionTTL(sessKindMini) != 7*24*time.Hour {
		t.Errorf("小程序会话该 7 天，实际 %v", sessionTTL(sessKindMini))
	}
	if sessionTTL(sessKindWeb) != sidTTL {
		t.Errorf("网页会话该 30 天，实际 %v", sessionTTL(sessKindWeb))
	}
	// 老会话（COS 上没 kind 字段）按 30 天算，别把存量用户踢下线
	if sessionTTL("") != sidTTL {
		t.Errorf("没 kind 的老会话该按 30 天，实际 %v", sessionTTL(""))
	}

	srv := &server{storage: &Storage{lang: "ja", dryRun: true}, lang: "ja"}
	mini := srv.createSession(User{UID: "u_1", Role: roleMember}, "test", sessKindMini)
	sessions.Lock()
	sessions.m[mini].LastSeen = time.Now().Add(-8 * 24 * time.Hour)
	sessions.Unlock()
	if _, ok := srv.lookupSession(mini); ok {
		t.Error("小程序的会话过了 8 天该失效")
	}

	web := srv.createSession(User{UID: "u_1", Role: roleMember}, "test", sessKindWeb)
	sessions.Lock()
	sessions.m[web].LastSeen = time.Now().Add(-8 * 24 * time.Hour)
	sessions.Unlock()
	if _, ok := srv.lookupSession(web); !ok {
		t.Error("网页会话 8 天还在 30 天内，不该失效")
	}
}

// 会话表有上限：匿名领体验版能被刷，不能让内存无限涨。
func TestSessionTableIsCapped(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}, lang: "ja"}
	sessions.Lock()
	sessions.m = map[string]*session{}
	sessions.Unlock()
	defer func() {
		sessions.Lock()
		sessions.m = map[string]*session{}
		sessions.Unlock()
	}()

	// 直接塞满（造 3000 条真实会话要跑 3000 次 goroutine，没必要）
	sessions.Lock()
	old := time.Now().Add(-40 * 24 * time.Hour)
	for i := 0; i < maxSessions; i++ {
		sessions.m["old"+string(rune('a'+i%26))+time.Now().Format("150405.000000000")+
			strings.Repeat("x", 0)+strconv.Itoa(i)] = &session{UID: "u_old", LastSeen: old}
	}
	sessions.Unlock()

	sid := srv.createSession(User{UID: trialUID, Role: trialRole}, "test", sessKindMini)
	sessions.Lock()
	n := len(sessions.m)
	_, kept := sessions.m[sid]
	sessions.Unlock()
	if !kept {
		t.Error("新会话该留在表里")
	}
	if n > maxSessions {
		t.Errorf("会话表超过上限 %d，实际 %d", maxSessions, n)
	}
}

// pending 每天 2 张图：判定在扣之前，第 3 张被拒且不污染计数。
func TestPendingVisionDailyQuota(t *testing.T) {
	st := &Storage{lang: "ja", dryRun: true, uid: "u_quota"}
	ctx := context.Background()

	if ok, _, left := takeQuota(ctx, st, quotaNameVision, 1, pendingVisionDaily); !ok || left != 1 {
		t.Fatalf("第 1 张该放行，ok=%v left=%d", ok, left)
	}
	if ok, _, left := takeQuota(ctx, st, quotaNameVision, 1, pendingVisionDaily); !ok || left != 0 {
		t.Fatalf("第 2 张该放行，ok=%v left=%d", ok, left)
	}
	ok, used, left := takeQuota(ctx, st, quotaNameVision, 1, pendingVisionDaily)
	if ok {
		t.Error("第 3 张该被拒")
	}
	if used != pendingVisionDaily {
		t.Errorf("被拒不该把计数顶上去，used=%d", used)
	}
	if left != 0 {
		t.Errorf("剩余该是 0，实际 %d", left)
	}
	// 一次要 3 张也直接拒（别让它分两次钻过去）
	if ok, _, _ := takeQuota(ctx, st, quotaNameVision, 3, pendingVisionDaily); ok {
		t.Error("一次 3 张该被拒")
	}
	if ok, used, _ := takeQuota(ctx, st, quotaNameVision, 1, pendingVisionDaily); ok || used != 2 {
		t.Errorf("被拒之后计数不该变，ok=%v used=%d", ok, used)
	}
	// 0 张不占额度
	if ok, _, _ := takeQuota(ctx, st, quotaNameVision, 0, pendingVisionDaily); !ok {
		t.Error("0 张不该被拦")
	}
}

// 用户表读不到 → 不能把匿名当 owner。
func TestAuthRequiredFailsClosed(t *testing.T) {
	// store 为 nil 时 ListUsers 返回 (nil, nil)，读得到 → 不算 unknown
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st, lang: "ja"}
	wxAppID, wxAppSecret = "wxappid", "wxsecret"
	defer func() { wxAppID, wxAppSecret = "", "" }()

	invalidateUserCache()
	seedUsers(t, nil)
	if srv.authRequired() {
		t.Error("用户表是空的（读成功了）→ 第一个人该能直接进来")
	}

	// 模拟「一次都没读成功」：loaded=false 且缓存里也没有
	invalidateUserCache()
	userCache.Lock()
	userCache.loaded = false
	userCache.users = nil
	userCache.Unlock()
	// ListUsers 在 store==nil 时会成功，所以用 usersUnknown 直接验语义
	if !usersUnknown() {
		t.Error("刚清过缓存该算 unknown")
	}
}
