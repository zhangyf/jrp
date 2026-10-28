package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 会话与登录票据。
//
// 会话以内存表为权威（每次请求不再打 COS，否则每个 API 都多一次 GET，
// 成本和延迟翻倍），同时异步写一份到 COS 兜底 —— 这样部署新版本、重启服务
// 之后老师不用重新扫码。
//
// 小程序端 wx.request 没有 cookie jar（真机不帮存 Set-Cookie），所以同一个
// token 同时接受两种载体：Cookie: jrp_sid=<t> 和 Authorization: Bearer <t>。
// 一张 session 表服务 web 和小程序，天然同一账号，不需要 unionid。

const (
	sidCookie       = "jrp_sid"
	pollCookie      = "jrp_login"
	sidTTL          = 30 * 24 * time.Hour
	miniSidTTL      = 7 * 24 * time.Hour // 小程序：老师 2026-10-28 定的，别再改回 30 天
	ticketTTL       = 5 * time.Minute
	sessionThrottle = 10 * time.Minute
	// maxSessions 内存会话表上限。
	//
	// ⚠️ 2026-10-28 安全评审加的：/api/auth/trial 匿名就能领会话，
	// 没有上限的话脚本刷一遍就几万条常驻内存，进程只涨不落。
	maxSessions      = 3000
	sessionSweepTick = 10 * time.Minute
)

// 会话种类：网页（cookie）和小程序（Bearer）寿命不一样。
const (
	sessKindWeb  = "web"
	sessKindMini = "mini"
)

// sessionTTL 会话寿命。滑动续期：从最后一次使用算起。
// 老会话（没有 kind 字段）按网页算 30 天。
func sessionTTL(kind string) time.Duration {
	if kind == sessKindMini {
		return miniSidTTL
	}
	return sidTTL
}

type session struct {
	UID      string    `json:"uid"`
	Role     string    `json:"role"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"` // web / mini，决定寿命
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"last_seen"`
	UA       string    `json:"ua"`
	// persisted 记录这份会话是否写过 COS，避免每次滑动续期都写一遍。
	persisted bool
	lastWrite time.Time
}

var sessions = struct {
	sync.Mutex
	m map[string]*session
}{m: map[string]*session{}}

// loginTicket 一次待确认的扫码登录。
type loginTicket struct {
	Ticket    string
	PollToken string // 只有发起登录的那个浏览器有（种在 jrp_login cookie 里）
	UID       string // 确认后绑定
	Confirmed bool
	Expires   time.Time
}

var tickets = struct {
	sync.Mutex
	m map[string]*loginTicket
}{m: map[string]*loginTicket{}}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 极端情况退化：时间 + 计数器也不至于撞，只是没那么随机。
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().Format("20060102150405.000")))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// newSessionID 32 字节 → 43 字符。
func newSessionID() string { return randomToken(32) }

// newTicket 16 字节 → 22 字符。⚠️ 必须 ≤32：wxacode 的 scene 字段硬上限就是 32
// 字符，超了微信只返回一个没有说明的报错。
func newTicket() string { return randomToken(16) }

// createSession 建一份会话。kind 决定寿命（web 30 天 / mini 7 天）。
//
// ⚠️ 2026-10-28 安全评审两处改动：
//  1. 体验版（trial）的会话**不落 COS** —— /api/auth/trial 匿名可领，
//     落盘等于让脚本白嫖 COS 写对象（每次一个 PUT，还能堆出几万个垃圾对象）。
//     体验版本来就不保存任何东西，重开小程序再领一个是完全合理的。
//  2. 内存表有上限，超了先扫过期、再按 LastSeen 淘汰最旧的。
func (s *server) createSession(u User, ua, kind string) string {
	sid := newSessionID()
	now := time.Now()
	sess := &session{UID: u.UID, Role: u.Role, Name: u.Name, Kind: kind, Created: now, LastSeen: now, UA: ua}

	sessions.Lock()
	if len(sessions.m) >= maxSessions {
		sweepSessionsLocked(now)
		for len(sessions.m) >= maxSessions {
			if oldest := oldestSessionLocked(); oldest != "" {
				delete(sessions.m, oldest)
			} else {
				break
			}
		}
	}
	sessions.m[sid] = sess
	sessions.Unlock()

	if u.UID != trialUID {
		go s.persistSession(sid, sess)
	}
	return sid
}

// sweepSessionsLocked 清掉过期会话。调用方必须持有 sessions 锁。
func sweepSessionsLocked(now time.Time) {
	for sid, sess := range sessions.m {
		if now.Sub(sess.LastSeen) > sessionTTL(sess.Kind) {
			delete(sessions.m, sid)
		}
	}
}

// oldestSessionLocked 找出最久没用的那份。调用方必须持有 sessions 锁。
func oldestSessionLocked() string {
	oldest := ""
	var at time.Time
	for sid, sess := range sessions.m {
		if oldest == "" || sess.LastSeen.Before(at) {
			oldest, at = sid, sess.LastSeen
		}
	}
	return oldest
}

// sweepSessions 常驻清扫。没有它，被丢弃的会话会一直占着内存。
func (s *server) sweepSessions() {
	t := time.NewTicker(sessionSweepTick)
	for range t.C {
		sessions.Lock()
		sweepSessionsLocked(time.Now())
		sessions.Unlock()
	}
}

// lookupSession 内存优先，miss 才回 COS 捞一次（服务重启的场景）。
func (s *server) lookupSession(sid string) (*session, bool) {
	if sid == "" {
		return nil, false
	}
	sessions.Lock()
	sess, ok := sessions.m[sid]
	sessions.Unlock()
	if ok {
		if time.Since(sess.LastSeen) > sessionTTL(sess.Kind) {
			s.destroySession(sid)
			return nil, false
		}
		// 滑动续期。节流写 COS：十分钟最多写一次。
		if time.Since(sess.LastSeen) > sessionThrottle {
			sessions.Lock()
			sess.LastSeen = time.Now()
			sessions.Unlock()
			go s.persistSession(sid, sess)
		}
		return sess, true
	}
	return s.loadSession(sid)
}

func (s *server) destroySession(sid string) {
	sessions.Lock()
	delete(sessions.m, sid)
	sessions.Unlock()
	st := s.storage
	go func() {
		_ = st.DeleteSession(context.Background(), sid)
	}()
}

// resolveUID 从请求里认出用户：cookie → Bearer → 都没有则是匿名。
//
// ⚠️ 红线：匿名不等于 owner。authRequired() 打开之后拿不到会话必须 401，
// 绝不能退回 owner —— 否则陌生人能往老师的档案里写东西，比不加鉴权还糟。
func (s *server) resolveUID(r *http.Request) string {
	sid := sessionIDFrom(r)
	if sid == "" {
		return ""
	}
	sess, ok := s.lookupSession(sid)
	if !ok {
		return ""
	}
	return sess.UID
}

// sessionUser 返回本次请求的会话（含角色），用于鉴权判定。
func (s *server) sessionUser(r *http.Request) (*session, bool) {
	sid := sessionIDFrom(r)
	if sid == "" {
		return nil, false
	}
	return s.lookupSession(sid)
}

func sessionIDFrom(r *http.Request) string {
	if c, err := r.Cookie(sidCookie); err == nil && c.Value != "" {
		return c.Value
	}
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func setSessionCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sidCookie,
		Value:    sid,
		Path:     "/",
		MaxAge:   int(sidTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// SameSite=Lax 顺带把跨站 POST 的 CSRF 挡了；/api/export 是 top-level GET 导航，
// Lax 下仍然带 cookie，下载不受影响。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sidCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func setPollCookie(w http.ResponseWriter, pollToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     pollCookie,
		Value:    pollToken,
		Path:     "/",
		MaxAge:   int(ticketTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func pollTokenFrom(r *http.Request) string {
	if c, err := r.Cookie(pollCookie); err == nil {
		return c.Value
	}
	return ""
}

// ---------------------------------------------------------------------------
// 票据
// ---------------------------------------------------------------------------

func newLoginTicket() *loginTicket {
	t := &loginTicket{
		Ticket:    newTicket(),
		PollToken: randomToken(24),
		Expires:   time.Now().Add(ticketTTL),
	}
	tickets.Lock()
	tickets.m[t.Ticket] = t
	tickets.Unlock()
	return t
}

func getTicket(ticket string) (*loginTicket, bool) {
	tickets.Lock()
	defer tickets.Unlock()
	t, ok := tickets.m[ticket]
	if !ok {
		return nil, false
	}
	if time.Now().After(t.Expires) {
		delete(tickets.m, ticket)
		return nil, false
	}
	return t, true
}

func confirmTicket(ticket, uid string) bool {
	tickets.Lock()
	defer tickets.Unlock()
	t, ok := tickets.m[ticket]
	if !ok || time.Now().After(t.Expires) {
		return false
	}
	t.UID = uid
	t.Confirmed = true
	return true
}

func dropTicket(ticket string) {
	tickets.Lock()
	delete(tickets.m, ticket)
	tickets.Unlock()
}

// sweepTickets 清掉过期票据，防止长时间运行后内存里堆一堆。
func sweepTickets() {
	tickets.Lock()
	defer tickets.Unlock()
	now := time.Now()
	for k, t := range tickets.m {
		if now.After(t.Expires) {
			delete(tickets.m, k)
		}
	}
}

// ---------------------------------------------------------------------------
// COS 兜底
// ---------------------------------------------------------------------------

func (s *server) persistSession(sid string, sess *session) {
	sess.persisted = true
	_ = s.storage.SaveSession(context.Background(), sid, sess)
}

func (s *server) loadSession(sid string) (*session, bool) {
	sess, err := s.storage.LoadSession(context.Background(), sid)
	if err != nil || sess == nil {
		return nil, false
	}
	if time.Since(sess.LastSeen) > sessionTTL(sess.Kind) {
		return nil, false
	}
	sessions.Lock()
	sessions.m[sid] = sess
	sess.persisted = true
	sess.lastWrite = time.Now()
	sessions.Unlock()
	return sess, true
}

// destroySessionsForUID 踢掉某人的所有会话（冻结 / 删号时用）。
//
// COS 上按 sid 存对象，没法按 uid 反查，所以这里只清内存表；落在 COS 上的那几份
// 会在 sidTTL 之后自然过期，而被冻结的账号本来就在 withUser 里被拒，
// 不会有实际风险。真要较真就等会话过期，不值得为这个再建一张索引。
func (s *server) destroySessionsForUID(uid string) {
	sessions.Lock()
	doomed := make([]string, 0, 4)
	for sid, sess := range sessions.m {
		if sess.UID == uid {
			doomed = append(doomed, sid)
			delete(sessions.m, sid)
		}
	}
	sessions.Unlock()
	if len(doomed) == 0 {
		return
	}
	st := s.storage
	go func() {
		for _, sid := range doomed {
			_ = st.DeleteSession(context.Background(), sid)
		}
	}()
}

// 小程序凭证只从环境变量读：COS 上的对象是明文，secret 放进去等于账号体系被接管。
// 服务器写在 /etc/jrp/jrp.env（chmod 600），由 jrp.service 的 EnvironmentFile 带进来。
var (
	wxAppID     string
	wxAppSecret string
	wxEnv       string // release / trial / develop，见 loadWXConfig
)

func loadWXConfig() {
	wxAppID = strings.TrimSpace(os.Getenv("JRP_WX_APPID"))
	wxAppSecret = strings.TrimSpace(os.Getenv("JRP_WX_APPSECRET"))
	wxEnv = resolveWXEnv(os.Getenv("JRP_WX_ENV"))
}

// resolveWXEnv 解析 JRP_WX_ENV：小程序码指向哪个版本。
//
// release（默认，小程序已发布）/ trial（体验版）/ develop（开发版）。
// ⚠️ 小程序没发布时 getwxacodeunlimit 会报 41030，开发期要设成 trial。
// 不认识的值不直接报错——笔误不该让整个登录挂掉，回退 release 并打一行日志。
func resolveWXEnv(v string) string {
	switch s := strings.TrimSpace(v); s {
	case "release", "trial", "develop":
		return s
	case "":
		return "release"
	default:
		log.Printf("auth: JRP_WX_ENV 值不认识（%q），回退 release", s)
		return "release"
	}
}

// wxEnabled 报告有没有配小程序 appid/secret。没配时登录接口返回 503，
// 其余功能照常 —— 老师还没注册完小程序也不影响日常练习。
func wxEnabled() bool { return wxAppID != "" && wxAppSecret != "" }

func (s *Storage) sessionKey(sid string) string { return s.authBase() + "/sessions/" + sid + ".json" }

func (s *Storage) SaveSession(ctx context.Context, sid string, sess *session) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.sessionKey(sid), []byte(toJSON(sess)))
}

func (s *Storage) LoadSession(ctx context.Context, sid string) (*session, error) {
	if s.store == nil {
		return nil, fmt.Errorf("storage 未连接 COS")
	}
	data, err := s.store.GetAll(ctx, s.sessionKey(sid))
	if err != nil || len(data) == 0 {
		return nil, err
	}
	var sess session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Storage) DeleteSession(ctx context.Context, sid string) error {
	if s.dryRun || s.store == nil {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.sessionKey(sid))
}
