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
	mux.HandleFunc("/api/conjugation", srv.withUser(srv.handleConjugation))
	mux.HandleFunc("/api/judge-sentence", srv.withUser(srv.handleJudgeSentence))
	mux.HandleFunc("/api/judge-prompt", srv.withUser(srv.handleJudgePrompt))

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

// withUser 做两件事：沿用 --auth-token 的校验，再把带 uid 的 Storage 交给 handler。
func (s *server) withUser(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false, "error": "unauthorized：缺少或错误的 token",
			})
			return
		}
		next(w, r, s.storage.ForUser(s.resolveUID(r)))
	}
}

// resolveUID 从请求里认出用户。
//
// 阶段 1（当前）：账号体系还没接上，恒返回 ""（owner）—— 所以 COS key 与改造前
// 逐字节相同，线上行为完全不变。阶段 2 会在这里查会话表
// （cookie jrp_sid 或 Authorization: Bearer 的会话 token）。
func (s *server) resolveUID(r *http.Request) string { return "" }

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
