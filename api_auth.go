package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
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

// 常驻小程序码：扫了直接进小程序首页，不带任何登录语义。
//
// scene 是 getwxacodeunlimit 的必填项（≤32 字符），这里塞个固定串当标识，
// 小程序首页不读它，等于占位。
const (
	miniEntryPage = "pages/index/index"
	miniScene     = "home"
)

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
	img, ct, err := miniCode(r.Context(), ticket, loginPage, wxEnv)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", ct) // 微信给的是什么格式就用什么格式（现在是 JPEG）
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(img)
}

// handleMiniQR 返回「进小程序」的常驻小程序码图片。
//
//	GET /api/mini-qr
//
// 跟登录码（/api/auth/qr）的区别，别混用：
//   - 登录码：scene 是一次性 ticket，5 分钟过期，扫了走确认页下发会话
//   - 这个码：固定不变、永久有效，扫了直接进小程序首页
//
// 所以它能常驻在页面左侧当「手机上接着练」的快捷入口，不用每几分钟重画一次。
// 微信的码本身永久有效，进程内缓存一份 —— 别每次刷新页面都去麻烦微信接口。
// 换了 JRP_WX_ENV（trial→release）缓存自动失效重画。
func (s *server) handleMiniQR(w http.ResponseWriter, r *http.Request, st *Storage) {
	if !wxEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "未配置微信凭证（JRP_WX_APPID / JRP_WX_APPSECRET）",
		})
		return
	}
	img, ct, err := cachedMiniCode(r.Context(), wxEnv)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", ct) // 微信给什么格式就用什么格式（现在是 JPEG）
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(img)
}

// cachedMiniCode 进程内缓存小程序码。并发下可能重复生成一次，无所谓（幂等）。
var miniCodeCache = struct {
	sync.Mutex
	env string
	img []byte
	ct  string
}{}

func cachedMiniCode(ctx context.Context, env string) ([]byte, string, error) {
	miniCodeCache.Lock()
	if len(miniCodeCache.img) > 0 && miniCodeCache.env == env {
		img, ct := miniCodeCache.img, miniCodeCache.ct
		miniCodeCache.Unlock()
		return img, ct, nil
	}
	miniCodeCache.Unlock()

	img, ct, err := miniCode(ctx, miniScene, miniEntryPage, env)
	if err != nil {
		return nil, "", err
	}

	miniCodeCache.Lock()
	miniCodeCache.env, miniCodeCache.img, miniCodeCache.ct = env, img, ct
	miniCodeCache.Unlock()
	return img, ct, nil
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
	// 冻结的人扫了也不给会话（跟 wxlogin 一个口径）。
	if u.Role == roleDisabled {
		dropTicket(ticket)
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false, "error": "账号已被冻结，找管理员开通", "role": u.Role,
		})
		return
	}
	// pending 过了 7 天体验期：跟 wxlogin 一个口径，这里也不发会话。
	// 小程序端挡得住不代表网页端能漏 —— 会话可能还活着（30 天），
	// 光靠 withUser 那道 403 就变成「能登录但用不了」，老师要的是登录这一步就挡住。
	if u.Role == rolePending && pendingExpired(u) {
		dropTicket(ticket)
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false,
			"error":   "管理员还没审核通过，7 天体验期已结束，不可继续使用",
			"role":    u.Role, "pending": true, "expired": true,
		})
		return
	}
	sid := s.createSession(u, r.UserAgent(), sessKindWeb)
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
	// ⚠️ 冻结是在这里生效的：不发会话，等于扫了也进不来。
	// 光靠 withUser 里那道 403 不够 —— 那只是「能登录但用不了」，
	// 老师要的是「冻结后就无法扫码登录」。
	if u.Role == roleDisabled {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false, "error": "账号已被冻结，找管理员开通", "role": u.Role,
		})
		return
	}
	// pending 过了 7 天体验期：直接不让登 —— 老师要的是「下次他再登录继续拦截」。
	// 判断读的是库里那条记录的 created（upsertUser 只更新 last_login），
	// 所以删掉记录才会重新计时，光重新扫码没用。
	if u.Role == rolePending && pendingExpired(u) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"success": false,
			"error":   "管理员还没审核通过，7 天体验期已结束，不可继续使用",
			"role":    u.Role, "pending": true, "expired": true,
		})
		return
	}
	sid := s.createSession(u, r.UserAgent(), sessKindMini)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   sid,
		"uid":     u.UID,
		"role":    u.Role,
		"pending": u.Role == rolePending,
		// 还剩几天给前端摆在首页，别让人到期了才知道
		"days_left": pendingDaysLeft(u),
		"name":      u.Name,
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
//
// ⚠️ 走 withUser（见 serve.go 的注册）：会话里的 role 是登录时的快照，
// 批准/冻结之后由 withUser 改正，这里才能报出权威角色。
func (s *server) handleAuthMe(w http.ResponseWriter, r *http.Request, st *Storage) {
	sess, ok := s.sessionUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false, "error": "unauthorized", "login": "/login.html",
		})
		return
	}
	pending := sess.Role == rolePending
	daysLeft := 0
	if pending {
		if u, ok := findUserByUID(cachedUsers(r.Context(), st), sess.UID); ok {
			daysLeft = pendingDaysLeft(u)
		} else {
			daysLeft = pendingGraceDays // 查不到就按满格给，别误伤
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"uid":       sess.UID,
		"role":      sess.Role,
		"pending":   pending,
		"days_left": daysLeft,
		"name":      sess.Name,
		"is_owner":  sess.UID == "",
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
// 没配小程序凭证、或一个用户都还没有 → 不开（等价今天的行为，靠 nginx Basic Auth 挡）。
// 一旦有人绑定成功（阶段 3）→ 打开，且匿名请求一律 401，绝不退回 owner。
//
// ⚠️ 2026-10-28 安全评审改成 **fail-closed**：
// 原来「读不到用户表」和「用户表真的是空的」是同一种表现（空切片），
// 于是 COS 抖一下 → len==0 → authRequired()==false → 匿名用户 uid="" → **就是 owner**。
// 现在只要一次都没读成功过，就当「不知道有没有人」→ 一律要求登录。
// 代价：COS 读失败的那一小会儿，网页上的匿名请求会 401（已登录的人不受影响，
// withUser 里 `logged` 为真就放行）。60 秒内自愈。
func (s *server) authRequired() bool {
	if !wxEnabled() {
		return false
	}
	if usersUnknown() {
		cachedUsers(s.ctx(), s.storage) // 再试一次，别因为一次抖动就长期挡着
		if usersUnknown() {
			return true
		}
	}
	return len(cachedUsers(s.ctx(), s.storage)) > 0
}
