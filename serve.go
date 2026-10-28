package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// server 持有 HTTP 层共享的东西。所有 handler 都是它的方法，方便拿 storage / lang。
type server struct {
	storage *Storage
	lang    string
	token   string
	dryRun  bool
}

// runServe starts the review website.
//
// 后端全部走 COS，没有本地状态 —— 所以这个进程可以在任何能连 COS 的机器上跑
// （笔记本、家里的 Mac、公网服务器），前端已由 go:embed 打进二进制。
func runServe(fs_ *flag.FlagSet, lang string) {
	addr := fs_.String("addr", "127.0.0.1", "监听地址（0.0.0.0 表示对外暴露，届时必须配 auth-token）")
	port := fs_.Int("port", 8080, "监听端口")
	token := fs_.String("auth-token", os.Getenv("JRP_AUTH_TOKEN"),
		"Bearer token；不设则不做鉴权（仅限 127.0.0.1 本机访问）")
	dryRun := fs_.Bool("dry-run", false,
		"演练模式：判分/匹配/版本号照常计算，但不写入 COS，用于放心点一遍验收")
	fs_.Parse(cmdArgs)

	// fail-closed：对外暴露却没设 token = 任何人都能改档案，直接拒绝启动。
	if !isLoopback(*addr) && strings.TrimSpace(*token) == "" {
		fmt.Fprintf(os.Stderr,
			"Error: 监听地址 %s 不是本机地址，必须设置 --auth-token 或环境变量 JRP_AUTH_TOKEN\n"+
				"（/api/record 会直接改写你的学习档案，不设 token 等于把档案公开）\n", *addr)
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}
	// 演练模式掐在 Storage 层：所有 Upload* 变空操作，读取照旧。
	// 这样 ApplyRecord 的判分、序号匹配、版本号 bump 全都是真的，只是不落盘。
	storage.SetDryRun(*dryRun)
	loadWXConfig() // 小程序凭证；没配就是没开登录，其余功能照常

	srv := &server{storage: storage, lang: lang, token: strings.TrimSpace(*token), dryRun: *dryRun}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error mounting web assets: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	// 静态资源不鉴权：浏览器第一次打开要能拿到页面，token 在页面里输入后存 localStorage。
	mux.Handle("/", staticHandler(sub))

	mux.HandleFunc("/api/plan", srv.withUser(srv.handlePlan))
	mux.HandleFunc("/api/hard", srv.withUser(srv.handleHard))
	mux.HandleFunc("/api/record", srv.withUser(srv.handleRecord))
	mux.HandleFunc("/api/export", srv.withUser(srv.handleExport))
	mux.HandleFunc("/api/import", srv.withUser(srv.handleImport))
	mux.HandleFunc("/api/stats", srv.withUser(srv.handleStats))
	mux.HandleFunc("/api/draft", srv.withUser(srv.handleDraft))
	mux.HandleFunc("/api/review", srv.withUser(srv.handleReview))
	mux.HandleFunc("/api/lexicon", srv.withUser(srv.handleLexicon))
	mux.HandleFunc("/api/words", srv.withUser(srv.handleAddWords))
	mux.HandleFunc("/api/vision", srv.withUser(srv.handleVision))
	mux.HandleFunc("/api/word-def", srv.withUser(srv.handleWordDef))
	mux.HandleFunc("/api/conjugation", srv.withUser(srv.handleConjugation))
	mux.HandleFunc("/api/grade", srv.withUser(srv.handleGrade)) // 小程序端判分（web 端仍在浏览器本地判）
	mux.HandleFunc("/api/judge-sentence", srv.withUser(srv.handleJudgeSentence))
	mux.HandleFunc("/api/judge-prompt", srv.withUser(srv.handleJudgePrompt))

	// 登录：小程序码确认登录。这几个都不走 withUser —— 它们自己就是登录入口。
	mux.HandleFunc("/api/mini-qr", srv.withUser(srv.handleMiniQR)) // 常驻小程序码（进小程序，不是登录码）
	mux.HandleFunc("/api/auth/ticket", srv.handleAuthTicket)
	mux.HandleFunc("/api/auth/qr", srv.handleAuthQR)
	mux.HandleFunc("/api/auth/status", srv.handleAuthStatus)
	mux.HandleFunc("/api/auth/wxlogin", srv.handleAuthWXLogin)
	mux.HandleFunc("/api/auth/trial", srv.handleAuthTrial) // 体验版：领试用身份，匿名可用
	mux.HandleFunc("/api/auth/confirm", srv.handleAuthConfirm)
	// ⚠️ auth/me 也过 withUser：sess.Role 是登录那一刻的快照，批准/冻结之后
	// 得在 withUser 里改正，前端才不用逼着用户重新扫码。
	mux.HandleFunc("/api/auth/me", srv.withUser(srv.handleAuthMe))
	mux.HandleFunc("/api/auth/logout", srv.handleAuthLogout)

	// 管理后台：只有 owner 能调（见 withAdmin 的注释）。页面是 /admin.html。
	mux.HandleFunc("/api/admin/users", srv.withAdmin(srv.handleAdminUsers))
	mux.HandleFunc("/api/admin/user", srv.withAdmin(srv.handleAdminUser))

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	authNote := "（无鉴权，仅本机）"
	if srv.token != "" {
		authNote = "（已启用 token 鉴权）"
	}
	fmt.Fprintf(os.Stderr, "jrp serve (%s) listening on http://%s %s\n", lang, listen, authNote)
	if *dryRun {
		fmt.Fprintf(os.Stderr,
			"⚠️  演练模式（--dry-run）：本次所有回写都不会写入 COS，档案版本号只在内存里变化\n")
	}

	httpSrv := &http.Server{
		Addr:         listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // 导出 Excel / 上传解析会比较慢
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

// staticHandler 服务内嵌的前端资源，带内容 ETag 协商缓存。
//
// 不能用裸 http.FileServer：embed.FS 的 ModTime 是零值，它既不发 Last-Modified
// 也不发 ETag，浏览器拿不到任何缓存依据，只能启发式缓存 —— 结果就是部署了新
// 二进制，老师浏览器还拿着旧 JS 继续跑，「明明修了怎么还有 bug」的锅全是它。
//
// 做法：启动时给每个文件算内容 hash 当 ETag（前端总共不到 200KB，开销可忽略）；
// Cache-Control: no-cache 让浏览器每次部署后重新协商，平时靠 ETag 命中 304
// 不重复下载。nginx gzip 会把 ETag 弱化成 W/"..." 再回传，比较时剥掉。
func staticHandler(sub fs.FS) http.Handler {
	etags := map[string]string{}
	if err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		sum := sha1.Sum(data)
		etags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	}); err != nil {
		panic(fmt.Errorf("扫描内嵌前端资源失败: %w", err))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		etag, ok := etags[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		if etagMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		data, _ := fs.ReadFile(sub, p)
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(data))
	})
}

// etagMatch 宽松比较 If-None-Match：忽略 W/ 前缀与引号差异，支持逗号分隔多值。
func etagMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	strip := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "W/")
		return strings.Trim(s, `"`)
	}
	want := strip(etag)
	for _, part := range strings.Split(header, ",") {
		if strip(part) == want {
			return true
		}
	}
	return false
}

// authedHandler 是需要登录的 API handler 的形状：st 已经绑好了本次请求的 uid。
//
// 之所以把 *Storage 硬塞进签名（而不是塞进 context）：只要有一个 handler 忘了取、
// 继续用 s.storage（= owner 的存储），就是「儿子的进度写进老师档案」的静默数据事故。
// 改签名 ⇒ 漏改直接编译不过，把这类事故挡在编译期。
type authedHandler func(w http.ResponseWriter, r *http.Request, st *Storage)

// withUser 认出用户、校验权限，再把带 uid 的 Storage 交给 handler。
//
// 三道门：
//  1. 会话（cookie jrp_sid 或 Bearer）—— 正常登录的用户走这里；
//  2. 共享 token（--auth-token）—— CLI 和脚本，仍然有效；
//  3. authRequired() 打开后，两者都没有 → 401。
//
// ⚠️ 红线：匿名请求绝不能退回 owner。没开登录时（还没人扫码绑过 owner）
// uid 为空 = owner，那正是今天的行为；一旦开了，匿名就是 401。
func (s *server) withUser(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, logged := s.sessionUser(r)
		tokenOK := s.token != "" &&
			subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1

		if s.authRequired() && !logged && !tokenOK {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false, "error": "unauthorized", "login": "/login.html",
			})
			return
		}
		if s.token != "" && !logged && !tokenOK {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false, "error": "unauthorized：缺少或错误的 token",
			})
			return
		}

		uid := ""
		if logged {
			uid = sess.UID
			// 角色改了（被禁用 / 还没批准）要让存量会话立刻失效。
			if u, ok := findUserByUID(cachedUsers(r.Context(), s.storage), uid); ok {
				// 冻结的照样挡在门外（不发会话那道在 handleAuthWXLogin 里，
				// 这里是存量会话的补刀）。
				if u.Role == roleDisabled {
					writeJSON(w, http.StatusForbidden, map[string]interface{}{
						"success": false, "error": "账号已被冻结，找管理员开通",
					})
					return
				}
				// pending 有 7 个自然日的**完整体验期**（权限跟 member 一样，
				// 数据在 users/<uid>/ 下）。到期还没被批准才拦 —— 老师要的是
				// 「给管理员缓冲，但不让陌生人无限期占着」。
				// ⚠️ 库里还有他这条记录，所以下次登录照样挡（见 handleAuthWXLogin）。
				if u.Role == rolePending && pendingExpired(u) {
					writeJSON(w, http.StatusForbidden, map[string]interface{}{
						"success": false,
						"error":   "管理员还没审核通过，7 天体验期已结束，不可继续使用",
						"pending": true, "expired": true,
					})
					return
				}
				// owner 恒用空 uid 走老路径。存量会话里存的是旧 uid（历史上给 owner 也发过
				// u_xxx），这里自愈一下，免得老师还得重新扫码。
				if u.Role == roleOwner {
					uid = ""
				}
				// 会话里的 role 是登录那一刻的快照。批准/冻结之后把它改正，
				// 免得用户必须重新扫码才生效（/api/auth/me 也就跟着对了）。
				sess.Role = u.Role
			}
		}

		// 体验版（试用账号）：凭证是公开的，所以除了三个版块和判分一律挡掉。
		// 白名单见 api_trial.go —— 加路由进去之前先确认它不带出老师的个人数据。
		if uid == trialUID && !trialAllowed(r.URL.Path) {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{
				"success": false, "error": "体验版只能看每日复习、造句、钉子户、活用、词汇总表和统计",
			})
			return
		}

		next(w, r, s.storage.ForUser(uid))
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// 也接受 ?token= ，方便 curl 和下载链接
	return r.URL.Query().Get("token")
}

// isLoopback 判断监听地址是否只对本机开放。
func isLoopback(addr string) bool {
	if addr == "" {
		return false
	}
	if addr == "localhost" || addr == "::1" || strings.HasPrefix(addr, "127.") {
		return true
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// parseDateParam 解析 ?date=YYYY-MM-DD，缺省为今天。
func parseDateParam(r *http.Request) (time.Time, bool) {
	s := r.URL.Query().Get("date")
	if s == "" {
		return time.Now(), true
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}

func (s *server) ctx() context.Context { return context.Background() }

// bgctx 包级的 background context。命名为 bgctx 而不是 context，是为了不和
// 各文件里 import 的 "context" 包撞名。
func bgctx() context.Context { return context.Background() }
