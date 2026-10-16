package main

import (
	"encoding/json"
	"net/http"
)

// 登录接口。全部挂在 /api/auth/ 下，除了 status 的双凭证校验，都不需要会话。
//
// 整体流程（小程序码确认登录）：
//
//	PC 打开 /login.html
//	  → POST /api/auth/ticket        拿 ticket，种 jrp_login cookie
//	  → GET  /api/auth/qr?ticket=…   拿小程序码图片
//	手机微信扫 → 小程序
//	  → wx.login() → POST /api/auth/wxlogin   拿 token（Bearer）
//	  → 点确认   → POST /api/auth/confirm     把 ticket 绑到自己的账号
//	PC 轮询 GET /api/auth/status?ticket=…
//	  → confirmed → 下发 jrp_sid cookie → 进首页
//
// 因为扫码的就是已经登录的那个小程序账号，web 和小程序的身份天然相同，
// 不需要 unionid（那玩意儿要企业主体的开放平台才拿得到）。

const loginPage = "pages/login/login"

func (s *server) handleAuthTicket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 POST"})
		return
	}
	if !wxEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "登录未配置：服务端缺少 JRP_WX_APPID / JRP_WX_APPSECRET",
		})
		return
	}
	sweepTickets()
	t := newLoginTicket()
	setPollCookie(w, t.PollToken)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"ticket":     t.Ticket,
		"expires_in": int(ticketTTL.Seconds()),
	})
}

// handleAuthQR 返回登录用的小程序码图片。
func (s *server) handleAuthQR(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if _, ok := getTicket(ticket); !ok {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "ticket 无效或已过期，请刷新页面"})
		return
	}
	png, err := miniCode(r.Context(), ticket, loginPage, wxEnv)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// handleAuthStatus PC 端轮询。
//
// ⚠️ 必须 ticket + poll_token 双匹配：小程序码是公开 URL，谁都能扫，
// 只凭 ticket 的话旁人能抢先轮询把会话拿走（他登的是他自己的号，受害者白扫一次，
// 但会话被截胡）。poll_token 只种在发起登录的那个浏览器里。
func (s *server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	t, ok := getTicket(ticket)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "status": "expired"})
		return
	}
	if t.PollToken == "" || t.PollToken != pollTokenFrom(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"success": false, "error": "不是本次登录的浏览器"})
		return
	}
	if !t.Confirmed {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "status": "pending"})
		return
	}

	users := cachedUsers(r.Context(), s.storage)
	u, found := findUserByUID(users, t.UID)
	if !found {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "用户不存在了"})
		return
	}
	sid := s.createSession(u, r.UserAgent())
	setSessionCookie(w, sid)
	dropTicket(ticket)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "status": "confirmed", "uid": u.UID, "role": u.Role, "name": u.Name,
	})
}

// handleAuthWXLogin 小程序端登录：wx.login() 拿到 code → 换 openid → 查/建用户。
// 返回 token，小程序存本地，之后所有请求带 Authorization: Bearer <token>。
func (s *server) handleAuthWXLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 POST"})
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "请求体不是 JSON"})
		return
	}
	openid, err := code2Session(r.Context(), in.Code)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	u, err := upsertUser(r.Context(), s.storage, openid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	sid := s.createSession(u, r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   sid,
		"uid":     u.UID,
		"role":    u.Role,
		"name":    u.Name,
	})
}

// handleAuthConfirm 小程序里点「确认登录」：把 ticket 绑到自己身上。
// 必须带 Bearer（小程序自己的会话），匿名不能确认。
func (s *server) handleAuthConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 POST"})
		return
	}
	sess, ok := s.sessionUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"success": false, "error": "小程序未登录"})
		return
	}
	var in struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "请求体不是 JSON"})
		return
	}
	if !confirmTicket(in.Ticket, sess.UID) {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "ticket 无效或已过期，请刷新网页上的二维码"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "uid": sess.UID})
}

// handleAuthMe 当前登录者。没登录返回 401，前端据此跳登录页。
func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false, "error": "unauthorized", "login": "/login.html",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"uid":      sess.UID,
		"role":     sess.Role,
		"name":     sess.Name,
		"is_owner": sess.UID == "",
	})
}

func (s *server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	sid := sessionIDFrom(r)
	if sid != "" {
		s.destroySession(sid) // 服务端必须删，光清 cookie 没用
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// authRequired 报告是否强制登录。
//
// 没配小程序凭证、或一个用户都还没有 → 不开（等价于今天的行为，靠 nginx Basic Auth 挡）。
// 一旦有人绑定成功（阶段 3）→ 打开，且匿名请求一律 401，绝不退回 owner。
func (s *server) authRequired() bool {
	if !wxEnabled() {
		return false
	}
	return len(cachedUsers(s.ctx(), s.storage)) > 0
}
