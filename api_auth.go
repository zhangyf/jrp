// api_auth.go —— 登录鉴权 / 试用 / 管理后台接口
//
// 腿一 R2：把原本平铺在根目录的 3 个 api_*.go / storage*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；HTTP 响应、存档、Excel 字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找 handler 先看下面的来源清单。
//
// 来源与顶层声明：
// api_auth.go: handleAuthTicket, handleAuthQR, handleMiniQR, cachedMiniCode, handleAuthStatus, handleAuthWXLogin, handleAuthConfirm, handleAuthMe, handleAuthLogout, authRequired
// api_trial.go: trialAllowed, ptrFloat, trialPlanWords, trialSnapshots, writeTrialStats, trialLexiconWords, writeTrialLexicon, trialConjugation, writeTrialConjugation, handleAuthTrial, ... (共 11 个顶层声明)
// api_admin.go: withAdmin, adminUserRow, handleAdminUsers, adminRank, openIDTail, handleAdminUser, adminActionAllowed, removeUser
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/lexicon"
)

// ==========================================================================
// 原 api_auth.go
// ==========================================================================

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

// ==========================================================================
// 原 api_trial.go
// ==========================================================================

// 体验版（试用账号）—— 2026-10-28 老师要求。
//
// 需求原话：进入小程序后有一个登录页，可以微信登录，也可以选试用账号；
// 试用账号只看「每日复习 / 造句 / 钉子户」三个版块，各 5 词 / 3 句 / 5 词，
// **题是固定的，不轮换**。
//
// 三条设计原则，动这文件之前先读：
//
//  1. **试用账号是服务端一个真实身份，不是前端假数据**。
//     判分必须仍然只有 `/api/grade` 一份实现 —— 在小程序里再抄一套判分规则，
//     两份迟早漂移（这个亏在 web/小程序之间吃过一次）。所以 trial 走正常会话，
//     只是被关进白名单。
//
//  2. **白名单是安全底线，不是可选项**。
//     trial 的凭证是公开的（任何人点一下就有），不设白名单等于把老师的词库、
//     统计、录入接口全挂出去。所以 trial 只能碰读题、判分、回看（空）、草稿（空）
//     这几条，其余一律 403。
//
//  3. **一次都不碰 COS**。
//     `/api/plan` 正常路径会「没有今天档案就初始化一份」——trial 要是走那条路，
//     就会在 `users/trial/` 下凭空造出一份档案，之后每次拉计划都往里写。
//     所以 handler 一进来就认 `st.uid == trialUID`：
//     出题的直接返回内置题面，回看/草稿直接回空。写接口（record/save-draft）
//     压根不在白名单里。
//
// 数据隔离是白送的：uid 非空的本来就读 `users/<uid>/`，试用数据就算写出来
// 也进不了老师的 prefix。但第 3 条还是要守 —— 不写才算干净。

const (
	trialUID  = "trial"
	trialRole = "trial" // /api/auth/me 对外报的身份（pending 也报这个，见 handleAuthMe）
)

// trialAllowed trial 身份允许访问的路由。
//
// 加路由进这个白名单之前，先问一句：这个接口会不会带出老师的个人数据？
// 会 → 不许加。判分（/api/grade）是唯一无状态的例外：它只读请求里的
// word 和 answer，不读档案，所以放行没有泄露可言。
// 回看（/api/review）和草稿（/api/draft）放进来是因为练习页一进页面就会调它们
// （钉子户先查快照、拉完题回填草稿），不放行会 403 挂住整页；它们在 handler
// 里对 trial 直接回空，读的也是 trial 自己的 prefix，不牵扯老师的数据。
// 判句（/api/judge-sentence）同 /api/grade：只读请求里的句子，prompt 模板在
// sharedBase（configs/）下，不碰任何人的个人数据 —— 但它会花钱调模型。
// 统计（/api/stats）、词汇总表（/api/lexicon）、活用（/api/conjugation）三条
// 也放行：它们对 trial 走 `writeTrial*` 分支返回**内置假数据**，一不读档案
// 二不碰 COS，看到的都是编好的演示内容，泄露不了任何东西。
// ⛔ 写接口（/api/record /api/words）永远不许进白名单。
func trialAllowed(path string) bool {
	p := strings.TrimSuffix(path, "/")
	switch p {
	case "/api/plan", "/api/hard", "/api/grade", "/api/auth/me",
		"/api/review", "/api/draft", "/api/judge-sentence",
		"/api/stats", "/api/lexicon", "/api/conjugation":
		return true
	}
	return false
}

// ---------- 固定题面 ----------

// 标日初级前几课的通用词。挑它们的理由：跟老师的实际进度无关，
// 对外演示合适，也不会泄露「学到第几课」。
var trialReviewWords = []PlanWord{
	{Number: 1, Word: "みず(水)", Definition: "水", Group: "体验版", Status: "🔄待测试"},
	{Number: 2, Word: "ほん(本)", Definition: "书", Group: "体验版", Status: "🔄待测试"},
	{Number: 3, Word: "がくせい(学生)", Definition: "学生", Group: "体验版", Status: "🔄待测试"},
	{Number: 4, Word: "たべもの(食べ物)", Definition: "食物", Group: "体验版", Status: "🔄待测试"},
	{Number: 5, Word: "でんわ(電話)", Definition: "电话", Group: "体验版", Status: "🔄待测试"},
}

// 钉子户挑第 9 课那组味觉/触感形容词 —— 长得像、意思容易混，
// 正好演示「钉子户」这个版块是干什么的。
var trialHardWords = []PlanWord{
	{Number: 1, Word: "しょっぱい(塩辛い)", Definition: "咸", Group: "体验版", Status: "☠️钉子户", ErrorCount: 4, ReviewCount: 8, Accuracy: ptrFloat(0.50)},
	{Number: 2, Word: "にがい(苦い)", Definition: "苦", Group: "体验版", Status: "☠️钉子户", ErrorCount: 5, ReviewCount: 9, Accuracy: ptrFloat(0.44)},
	{Number: 3, Word: "からい(辛い)", Definition: "辣", Group: "体验版", Status: "☠️钉子户", ErrorCount: 4, ReviewCount: 7, Accuracy: ptrFloat(0.43)},
	{Number: 4, Word: "あまい(甘い)", Definition: "甜", Group: "体验版", Status: "☠️钉子户", ErrorCount: 3, ReviewCount: 8, Accuracy: ptrFloat(0.62)},
	{Number: 5, Word: "つめたい(冷たい)", Definition: "凉（触感）", Group: "体验版", Status: "☠️钉子户", ErrorCount: 3, ReviewCount: 6, Accuracy: ptrFloat(0.50)},
}

// 课本原文，第 1-3 课。⚠️ 别自造句子 —— 造句必须是课本原文是全局铁律。
var trialSentences = []PlanSentence{
	{Number: 1, Chinese: "小李是中国人。", Answer: "李さんは 中国人です。"},
	{Number: 2, Chinese: "这是书。", Answer: "これは ほんです。"},
	{Number: 3, Chinese: "洗手间在哪里？", Answer: "トイレは どこですか。"},
}

func ptrFloat(v float64) *float64 { return &v }

// trialPlanWords 按模式取固定题面（返回副本，别让调用方改到全局那一份）。
func trialPlanWords(mode string) []PlanWord {
	src := trialReviewWords
	if mode == "hard" {
		src = trialHardWords
	}
	out := make([]PlanWord, len(src))
	copy(out, src)
	return out
}

// ---------- 只读演示数据（统计 / 词汇总表 / 活用） ----------
//
// 这三个版块对体验版开放，但要的是「看起来像那么回事」，不是真数据 ——
// 所以整段写死在这里，不读档案、不碰 COS。改真数据口径时**不用**同步这里，
// 它们是两回事（一个是老师的学习记录，一个是给陌生人看的样板间）。

// trialSnapshots 造一条递增的学习曲线：总词量每天 +4，四档按比例切。
// 前端按 snapshots 画走势、算 KPI 增量，所以**必须按日期升序**返回。
func trialSnapshots(days int) []map[string]interface{} {
	if days < 1 {
		days = 30
	}
	if days > 90 {
		days = 90
	}
	base := time.Now().AddDate(0, 0, -(days - 1))
	out := make([]map[string]interface{}, 0, days)
	errs := 210
	for i := 0; i < days; i++ {
		total := 430 + i*4
		mastered := total * 52 / 100
		basic := total * 24 / 100
		needs := total * 9 / 100
		errs += 2
		if i%7 == 0 {
			errs += 5 // 偶尔错多一点，曲线不至于像尺子画的
		}
		out = append(out, map[string]interface{}{
			"date":         base.AddDate(0, 0, i).Format("2006-01-02"),
			"total":        total,
			"mastered":     mastered,
			"basic":        basic,
			"needs_consol": needs,
			"untested":     total - mastered - basic - needs,
			"errors":       errs,
		})
	}
	return out
}

// writeTrialStats GET /api/stats 的体验版分支。结构跟真接口一致，前端不用分支。
func writeTrialStats(w http.ResponseWriter, days int) {
	snaps := trialSnapshots(days)
	last := snaps[len(snaps)-1]
	firm := last["total"].(int)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"language":  "ja",
		"days":      days,
		"trial":     true,
		"read_only": true,
		"snapshots": snaps,
		"changes":   map[string]interface{}{},
		"jlpt": map[string]interface{}{
			// ⚠️ vocab / lesson 在 n5 / n4 里是**门槛值**；当前值是顶层的这两个。
			"vocab_firm": firm, "lesson": 16,
			"n5": map[string]interface{}{"vocab": 800, "lesson": 22},
			"n4": map[string]interface{}{"vocab": 1500, "lesson": 44},
		},
		"detail": map[string]interface{}{
			// 档位名必须跟前端 DIST_ORDER 对上，少一个整块不显示
			"accuracy_distribution": map[string]int{
				"0-30%": 12, "30-60%": 26, "60-80%": 58, "80-90%": 94, "90-100%": 161,
			},
			"top_reviewed": []map[string]interface{}{
				{"word": "しょっぱい(塩辛い)", "reviews": 8, "errors": 4, "accuracy": 0.50},
				{"word": "にがい(苦い)", "reviews": 9, "errors": 5, "accuracy": 0.44},
				{"word": "からい(辛い)", "reviews": 7, "errors": 4, "accuracy": 0.43},
				{"word": "たべもの(食べ物)", "reviews": 6, "errors": 2, "accuracy": 0.67},
				{"word": "でんわ(電話)", "reviews": 5, "errors": 1, "accuracy": 0.80},
			},
			"hard_words": map[string]interface{}{"total": 9, "severe": 3, "moderate": 4, "mild": 2},
		},
	})
}

// trialLexiconWords 词汇总表的 10 行：5 个基础词 + 5 个钉子户，跟练习里是同一批。
// 词形拆分复用 lexicon.SplitLexiconWord，别在这儿另写一套括号规则。
func trialLexiconWords() []LexiconItem {
	type row struct {
		word, pos, sub, status string
		reviews, errors        int
	}
	rows := []row{
		{"みず(水)", "名词", "", "🟢已掌握", 6, 0},
		{"ほん(本)", "名词", "", "🟢已掌握", 5, 1},
		{"がくせい(学生)", "名词", "", "🟡基本掌握", 4, 1},
		{"たべもの(食べ物)", "名词", "", "🟡基本掌握", 6, 2},
		{"でんわ(電話)", "名词", "", "🔵未测试", 0, 0},
		{"しょっぱい(塩辛い)", "形容词", "い形", "☠️钉子户", 8, 4},
		{"にがい(苦い)", "形容词", "い形", "☠️钉子户", 9, 5},
		{"からい(辛い)", "形容词", "い形", "☠️钉子户", 7, 4},
		{"あまい(甘い)", "形容词", "い形", "🔴待巩固", 8, 3},
		{"つめたい(冷たい)", "形容词", "い形", "🔴待巩固", 6, 3},
	}
	def := map[string]string{}
	for _, w := range trialReviewWords {
		def[w.Word] = w.Definition
	}
	for _, w := range trialHardWords {
		def[w.Word] = w.Definition
	}

	items := make([]LexiconItem, 0, len(rows))
	for i, r := range rows {
		kana, kanji, note := lexicon.SplitLexiconWord(r.word)
		items = append(items, LexiconItem{
			Number: i + 1, Word: r.word, Kana: kana, Kanji: kanji, Note: note,
			Def: def[r.word], Pos: r.pos, Sub: r.sub, Status: r.status,
			Group: "体验版", Reviews: r.reviews, Errors: r.errors,
			Last: time.Now().AddDate(0, 0, -r.reviews).Format("2006-01-02"),
		})
	}
	return items
}

func writeTrialLexicon(w http.ResponseWriter) {
	items := trialLexiconWords()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "trial": true, "read_only": true,
		"count": len(items), "items": items,
		"pos_summary": map[string]int{"名词": 5, "形容词": 5},
		"groups":      []string{"体验版"},
	})
}

// trialConjugation 活用练习：每个类型固定 2 题，不管前端传 count 多少。
// label 必须跟真接口一致（原形/て形/た形、原形/ない形、过去式/否定形/过去否定形），
// 否则前端那一栏的题目名会跟对不上。
// ⚠️ 真接口加了新形（2026-11-03 加た形），这里必须同步补一列，
// 否则体验版那张卡比正常卡少一个空。
func trialConjugation(typ string) []ConjItem {
	two := func(forms ...string) []string { return forms }
	switch typ {
	case "verb_nai":
		return []ConjItem{
			{Word: "たべる(食べる)", Definition: "吃", Pos: "动词", Sub: "二类",
				Tasks: []ConjTask{
					{Label: "原形", Answers: two("たべる", "食べる")},
					{Label: "ない形", Answers: two("たべない", "食べない")},
				}},
			{Word: "かう(買う)", Definition: "买", Pos: "动词", Sub: "一类",
				Tasks: []ConjTask{
					{Label: "原形", Answers: two("かう", "買う")},
					// ⚠️ う结尾变「わ」不变「あ」—— 活用表里特意标过的坑
					{Label: "ない形", Answers: two("かわない", "買わない")},
				}},
		}
	case "adj":
		return []ConjItem{
			{Word: "たかい(高い)", Definition: "高（贵）", Pos: "形容词", Sub: "い形",
				Tasks: []ConjTask{
					{Label: "过去式", Answers: two("たかかった", "高かった")},
					{Label: "否定形", Answers: two("たかくない", "高くない")},
					{Label: "过去否定形", Answers: two("たかくなかった", "高くなかった")},
				}},
			{Word: "やすい(安い)", Definition: "便宜", Pos: "形容词", Sub: "い形",
				Tasks: []ConjTask{
					{Label: "过去式", Answers: two("やすかった", "安かった")},
					{Label: "否定形", Answers: two("やすくない", "安くない")},
					{Label: "过去否定形", Answers: two("やすくなかった", "安くなかった")},
				}},
		}
	default: // verb（て形 + た形）
		return []ConjItem{
			{Word: "たべる(食べる)", Definition: "吃", Pos: "动词", Sub: "二类",
				Tasks: []ConjTask{
					{Label: "原形", Answers: two("たべる", "食べる")},
					{Label: "て形", Answers: two("たべて", "食べて")},
					{Label: "た形", Answers: two("たべた", "食べた")},
				}},
			{Word: "かう(買う)", Definition: "买", Pos: "动词", Sub: "一类",
				Tasks: []ConjTask{
					{Label: "原形", Answers: two("かう", "買う")},
					// う・つ・る 是促音便（て形 って / た形 った，同一个音便）
					{Label: "て形", Answers: two("かって", "買って")},
					{Label: "た形", Answers: two("かった", "買った")},
				}},
		}
	}
}

func writeTrialConjugation(w http.ResponseWriter, typ string) {
	items := trialConjugation(typ)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "trial": true, "read_only": true,
		"type": typ, "count": len(items), "pool": len(items), "items": items,
	})
}

// ---------- 登录 ----------

// handleAuthTrial 领一个试用身份。不需要微信、不需要批准，点一下就有。
//
//	POST /api/auth/trial → { success, token, uid:"trial", role:"trial" }
//
// ⚠️ **不写用户表**：写进去就会成为「第一个用户」之外的真实账号，
// 会牵动 `authRequired()`（用户表非空 = 强制登录）和后台的用户列表。
// 试用身份只存在于会话表里，服务端重启后自然消失，没有残留。
func (s *server) handleAuthTrial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "用 POST",
		})
		return
	}
	u := User{UID: trialUID, Role: trialRole, Name: "体验用户"}
	sid := s.createSession(u, r.UserAgent(), sessKindMini)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   sid,
		"uid":     trialUID,
		"role":    trialRole,
		"name":    u.Name,
	})
}

// writeTrialPlan 试用账号的计划。结构跟正常 /api/plan 一致，前端不用分支。
func writeTrialPlan(w http.ResponseWriter, mode string) {
	now := time.Now()
	base := map[string]interface{}{
		"success":    true,
		"date":       now.Format("2006-01-02"),
		"trial":      true,
		"read_only":  true, // 前端据此关掉提交按钮
		"write_note": "体验版不保存进度",
	}
	if mode == "sentences" {
		base["sentences"] = trialSentences
		base["sentence_count"] = len(trialSentences)
		base["due_count"] = 0
	} else {
		base["words"] = trialPlanWords(mode)
		base["due_count"] = len(trialPlanWords(mode))
		base["hard_count"] = 0
	}
	writeJSON(w, http.StatusOK, base)
}

// ==========================================================================
// 原 api_admin.go
// ==========================================================================

// 管理后台接口（/api/admin/*）。
//
// 权限只认两样：owner 的登录会话，或 --auth-token。
//
// ⚠️ 不认「过了 nginx Basic Auth 就算管理员」：Basic Auth 是 nginx 那道门，
// Go 自己看不见，等阶段 3 把 auth_basic 摘掉就没人拦了。owner 会话是 Go
// 能自己校验的东西，摘不摘口令都一样安全，也不用再管一套管理员密码。
//
// 管理操作一律用 owner 的 Storage：用户表在语言根之外的 _auth/ 下，
// 跟 uid 无关，不存在「谁的档案」这回事。

func (s *server) withAdmin(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, logged := s.sessionUser(r)
		tokenOK := s.token != "" &&
			subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1

		if !tokenOK {
			if !logged {
				writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
					"success": false, "error": "unauthorized", "login": "/login.html?next=/admin.html",
				})
				return
			}
			// 角色以用户表为准：会话里的 Role 是登录那一刻的快照，
			// 管理员自己被改了角色也要立刻生效。
			u, ok := findUserByUID(cachedUsers(r.Context(), s.storage), sess.UID)
			if !ok || u.Role != roleOwner {
				writeJSON(w, http.StatusForbidden, map[string]interface{}{
					"success": false, "error": "只有站点所有者能进管理后台",
				})
				return
			}
		}
		next(w, r, s.storage.ForUser(""))
	}
}

// adminUserRow 给前端的一行。openid 只给尾号 —— 那是微信身份，没必要整段摆到页面上。
type adminUserRow struct {
	UID        string `json:"uid"`
	Role       string `json:"role"`
	Name       string `json:"name"`
	OpenIDTail string `json:"openid_tail"`
	Created    string `json:"created"`
	LastLogin  string `json:"last_login"`
	IsOwner    bool   `json:"is_owner"`
}

// handleAdminUsers 用户列表。待审核的排最前，被冻结的沉底。
func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 GET"})
		return
	}
	users, err := st.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	rows := make([]adminUserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, adminUserRow{
			UID:        u.UID,
			Role:       u.Role,
			Name:       u.Name,
			OpenIDTail: openIDTail(u.OpenID),
			Created:    u.Created,
			LastLogin:  u.LastLogin,
			IsOwner:    u.UID == "",
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return adminRank(rows[i].Role) < adminRank(rows[j].Role)
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"count":   len(rows),
		"users":   rows,
		"roles": map[string]string{
			roleOwner:    "站点所有者",
			roleMember:   "成员",
			rolePending:  "待批准",
			roleDisabled: "已冻结",
		},
	})
}

// adminRank 排序权重：待批准 → 所有者 → 成员 → 已冻结。
func adminRank(role string) int {
	switch role {
	case rolePending:
		return 0
	case roleOwner:
		return 1
	case roleMember:
		return 2
	default:
		return 3
	}
}

func openIDTail(openid string) string {
	if len(openid) <= 4 {
		return openid
	}
	return "…" + openid[len(openid)-4:]
}

// handleAdminUser 改一个账号。
//
//	POST /api/admin/user  {uid, action, name?}
//	action: approve（批准/解冻）| disable（冻结）| rename（改名）| delete（删号）
//
// ⚠️ 冻结和删号都会立刻踢掉他的存量会话；删号只删账号，users/<uid>/ 下的学习数据保留。
func (s *server) handleAdminUser(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 POST"})
		return
	}
	var in struct {
		UID    string `json:"uid"`
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "请求体不是 JSON"})
		return
	}
	in.Action = strings.ToLower(strings.TrimSpace(in.Action))

	usersMu.Lock()
	defer usersMu.Unlock()

	users, err := st.ListUsers(r.Context()) // 写前重读，不用缓存
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "读用户表失败：" + err.Error()})
		return
	}
	u, ok := findUserByUID(users, in.UID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"success": false, "error": "没有这个用户（刷新列表看看）"})
		return
	}

	if err := adminActionAllowed(u, in.Action); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	note := ""
	switch in.Action {
	case "approve":
		u.Role = roleMember
		note = "已批准，现在可以扫码登录使用"
	case "disable":
		u.Role = roleDisabled
		note = "已冻结，他的会话立刻失效，再扫码也进不来"
	case "rename":
		n := strings.TrimSpace(in.Name)
		if n == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "名字不能为空"})
			return
		}
		u.Name = n
		note = "已改名为「" + n + "」"
	case "delete":
		users = removeUser(users, in.UID)
		if err := st.SaveUsers(r.Context(), users); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "写回失败：" + err.Error()})
			return
		}
		invalidateUserCache()
		s.destroySessionsForUID(in.UID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "uid": in.UID, "action": "delete",
			"note": "账号已删除（学习数据保留）；他再用微信扫会重新变成待批准",
		})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "不认识的动作：" + in.Action})
		return
	}

	u.LastLogin = time.Now().Format("2006-01-02 15:04:05")
	for i := range users {
		if users[i].UID == u.UID {
			users[i] = u
		}
	}
	if err := st.SaveUsers(r.Context(), users); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": "写回失败：" + err.Error()})
		return
	}
	invalidateUserCache()
	// 角色一变就让存量会话失效：不然冻结了还能用旧会话继续练。
	if in.Action == "disable" || in.Action == "approve" {
		s.destroySessionsForUID(u.UID)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "uid": u.UID, "action": in.Action, "role": u.Role, "name": u.Name, "note": note,
	})
}

// adminActionAllowed 判断这个动作能不能落在这个账号上。
//
// ⚠️ owner 不可冻结、不可删除：uid=="" 是零迁移的根（他的数据就在语言根下），
// 把他冻了老师自己进不去，删了则没人能再批准任何人 —— 只能上服务器改 COS 救。
func adminActionAllowed(u User, action string) error {
	if u.UID == "" && (action == "disable" || action == "delete") {
		return fmt.Errorf("不能冻结或删除站点所有者")
	}
	switch action {
	case "approve", "disable", "rename", "delete":
		return nil
	default:
		return fmt.Errorf("不认识的动作：%s", action)
	}
}

func removeUser(users []User, uid string) []User {
	out := make([]User, 0, len(users))
	for _, u := range users {
		if u.UID != uid {
			out = append(out, u)
		}
	}
	return out
}
