package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

	ownerSID := srv.createSession(User{UID: "", Role: roleOwner}, "test")
	memberSID := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleMember}, "test")
	pendingSID := srv.createSession(User{UID: "u_pending1", Role: rolePending}, "test")

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
	sid := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleOwner}, "test") // 会话自称 owner
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
	victim := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleMember}, "test")
	other := srv.createSession(User{UID: "", Role: roleOwner}, "test")

	srv.destroySessionsForUID("u_7f3a2b9c")

	if _, ok := srv.lookupSession(victim); ok {
		t.Error("被冻结的人的会话该被清掉")
	}
	if _, ok := srv.lookupSession(other); !ok {
		t.Error("别人的会话不该被牵连")
	}
}
