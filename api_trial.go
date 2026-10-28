package main

import (
	"net/http"
	"strings"
	"time"
)

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

const trialUID = "trial"

// trialAllowed trial 身份允许访问的路由。
//
// 加路由进这个白名单之前，先问一句：这个接口会不会带出老师的个人数据？
// 会 → 不许加。判分（/api/grade）是唯一无状态的例外：它只读请求里的
// word 和 answer，不读档案，所以放行没有泄露可言。
// 回看（/api/review）和草稿（/api/draft）放进来是因为练习页一进页面就会调它们
// （钉子户先查快照、拉完题回填草稿），不放行会 403 挂住整页；它们在 handler
// 里对 trial 直接回空，读的也是 trial 自己的 prefix，不牵扯老师的数据。
// 判句（/api/judge-sentence）同 /api/grade：只读请求里的句子，prompt 模板在
// sharedBase（configs/）下，不碰任何人的个人数据 —— 但它会花钱调模型，
// 所以白名单就到这儿为止，/api/lexicon /api/stats /api/words 一律不给。
func trialAllowed(path string) bool {
	p := strings.TrimSuffix(path, "/")
	switch p {
	case "/api/plan", "/api/hard", "/api/grade", "/api/auth/me",
		"/api/review", "/api/draft", "/api/judge-sentence":
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
	u := User{UID: trialUID, Role: "trial", Name: "体验用户"}
	sid := s.createSession(u, r.UserAgent())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   sid,
		"uid":     trialUID,
		"role":    "trial",
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
