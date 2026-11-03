package main

import (
	"context"
	"crypto/subtle"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zhangyf/jrp/internal/knowledge"
)

// ---------- 知识点板块（小程序「新标日III知识点」，2026-11-03 老师要求） ----------
//
// 需求原话：小程序里新增一个板块，把新标日各课的知识点总结按课时展示出来；
// **非登录状态下仅展示一部分，其余被模糊滤镜挡住**。
//
// 三条设计决定，动这个文件之前先读：
//
//  1. **markdown 在服务端解析，不在小程序里解析**。
//     小程序没有 markdown 渲染能力（rich-text 只吃 HTML 节点，且样式不可控），
//     而且「哪几块模糊」必须按块切 —— 所以这里把 md 拆成结构化块数组发给前端，
//     前端只负责按 type 摆样式。
//
//  2. **这个接口不走 withUser**。
//     匿名和体验版也要能读到（只是读到的内容被截断），而 withUser 在鉴权打开后
//     对匿名直接 401。所以这里自己认会话：认出来就给全量，认不出来就给受限版。
//     ⛔ 只读 sharedBase（教材内容，不属于任何人的个人数据），一行都不碰 uid 目录。
//
//  3. **模糊是展示层软限制，不是加密**。
//     后端照样把全文发下去，只多给一个 visible 数字让前端把后面的块盖住。
//     想拿全文的人抓个包就有 —— 这是有意的取舍：知识点是教材整理而非隐私数据，
//     挡一下是为了引导登录，不是防攻击。真要防就在服务端按块截断（那前端就没有
//     「模糊」的视觉了，跟老师要的效果不符）。

type KSpan = knowledge.Span
type KItem = knowledge.Item
type KBlock = knowledge.Block

// lessonDocRe 从文件名里认课号：「标准日本语初级上册_第21课知识点.md」→ 21。
//
// 只认「第N课知识点」这一种命名 —— knowledge 目录里还混着
// 「课本原文汇编_第N单元（基本课文）.md」和「造句易错语法速查（第12-13课）.md」，
// 它们不是按课的知识点总结，不能进这个板块。
var lessonDocRe = regexp.MustCompile(`第(\d+)课知识点`)

// handleKnowledge 知识点板块的唯一入口。
//
//	GET /api/knowledge            → 课号列表（按课号数字排序，不是字符串排序）
//	GET /api/knowledge?lesson=21  → 第 21 课全文（分块）
func (s *server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	objs, err := s.storage.ListKnowledge(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "知识点目录读不了：" + err.Error(),
		})
		return
	}

	type lesson struct {
		num  int
		name string
		size int64
	}
	var ls []lesson
	for _, o := range objs {
		m := lessonDocRe.FindStringSubmatch(o.Key)
		if len(m) != 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ls = append(ls, lesson{num: n, name: o.Key[strings.LastIndex(o.Key, "/")+1:], size: o.Size})
	}
	// 课号必须按数字排：字符串排序会把第 10 课排在第 1 课前面。
	sort.Slice(ls, func(i, j int) bool { return ls[i].num < ls[j].num })

	if len(ls) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "lessons": []interface{}{},
		})
		return
	}

	full := s.knowledgeFullAccess(r, ctx)
	want := strings.TrimSpace(r.URL.Query().Get("lesson"))

	// 没传 lesson：只回列表（体验版也要能看到有哪些课，才有「登录看全部」的动力）。
	if want == "" {
		out := make([]map[string]interface{}, 0, len(ls))
		for _, l := range ls {
			out = append(out, map[string]interface{}{
				"lesson": l.num,
				"title":  "第" + strconv.Itoa(l.num) + "课",
				"size":   l.size,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"locked":  !full,
			"lessons": out,
		})
		return
	}

	num, err := strconv.Atoi(want)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "lesson 应该是课号数字",
		})
		return
	}
	var hit *lesson
	for i := range ls {
		if ls[i].num == num {
			hit = &ls[i]
			break
		}
	}
	if hit == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false, "error": "没有第 " + want + " 课的知识点",
		})
		return
	}

	data, err := s.storage.DownloadKnowledge(ctx, hit.name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "知识点读不了：" + err.Error(),
		})
		return
	}
	blocks := knowledge.ParseMarkdown(string(data))

	visible := len(blocks)
	if !full {
		visible = knowledge.VisibleBlockCount(blocks)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"lesson":  num,
		"title":   knowledge.Title(blocks, num),
		"locked":  !full,
		"visible": visible,
		"blocks":  blocks,
	})
}

// knowledgeFullAccess 这次请求能不能看全文。
//
// 三种情况给全量：正常登录（含 7 天体验期内的 pending）、被冻结/过期 pending 不算、
// 以及 CLI 的共享 token。体验版（trial）和匿名一律受限。
func (s *server) knowledgeFullAccess(r *http.Request, ctx context.Context) bool {
	if s.token != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1 {
		return true
	}
	sess, logged := s.sessionUser(r)
	if !logged {
		return false
	}
	// ⚠️ uid == "" ⇔ owner（零迁移约定，见 auth.go / api_auth.go 的 is_owner）。
	// 2026-11-03 踩过：这里原来写成 `sess.UID == "" || sess.UID == trialUID`，
	// 把 owner 当成匿名挡在门外 —— 老师在小程序里「每日复习已登录、知识点还是未登录」
	// 就是这个。匿名压根没有会话（sessionUser 已经先返回 false 了），
	// 能走到这里的 uid=="" 只可能是 owner。
	if sess.UID == trialUID {
		return false // 体验版凭证是公开的，等于匿名
	}
	if sess.UID == "" {
		return true // owner：全量
	}
	u, ok := findUserByUID(cachedUsers(ctx, s.storage), sess.UID)
	if !ok {
		return true // 用户表里查不到（老会话）就按会话角色给
	}
	if u.Role == roleDisabled {
		return false
	}
	if u.Role == rolePending && pendingExpired(u) {
		return false
	}
	return true
}
