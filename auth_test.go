package main

import (
	"net/http"
	"testing"
)

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

	ownerSID := srv.createSession(User{UID: "", Role: roleOwner, Name: "老师"}, "test-agent")
	sess, ok := srv.lookupSession(ownerSID)
	if !ok {
		t.Fatal("刚建的会话应该能查到")
	}
	if sess.UID != "" || sess.Role != roleOwner {
		t.Errorf("owner 会话不对：uid=%q role=%q", sess.UID, sess.Role)
	}

	memberSID := srv.createSession(User{UID: "u_7f3a2b9c", Role: roleMember}, "test-agent")
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
