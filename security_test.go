package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
