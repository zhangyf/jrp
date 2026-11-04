package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zhangyf/jrp/internal/judge"
	"github.com/zhangyf/jrp/internal/llm"
	"github.com/zhangyf/jrp/internal/sentence"
)

// 造句的语义判定：字面比对不一致时，问大模型「这句意思成立吗」。
//
// 设计前提（2026-09-29 老师定）：
//  1. 字面一致永远直接判对，不调模型 —— 0 延迟、0 成本、不依赖外网。
//  2. 只有字面不一致才问模型；模型只判「意思成不成立」，不判语法优劣。
//  3. 模型不可用（没配密钥 / 超时 / 报错）就退回字面判定，练习绝不卡住。
//  4. 判定结果按 (原句, 老师写的) 缓存：刷新、换设备重开都得到同一个结论，
//     不能这次判对下次判错 —— 跟「不提交不换题」是同一个规矩。
//
// 密钥只存在于服务器环境变量，前端拿不到，也不会出现在任何响应里。
//
// 本文件现在只留 HTTP 薄适配（参数解析 → 调用例 → 序列化）与根包适配层：
//   - 纯规则 / 模板缓存 / 判定缓存 / 编排：internal/judge（用例层，不认识 IO）
//   - 主→备切换 / 失败冷却 / 真发 HTTP：llm_failover.go（IO 层，全局共享）
// 两个 handler 的响应 map 逐字未动，HTTP 输出字节不变。

// judgeResult 保留根包类型别名：llm_failover.go 的 judgeWithFallback
// 仍以它为返回类型；字段与 json 行为跟 internal/judge.Result 完全一致。
type judgeResult = judge.Result

// judgeCaller 把根包的判句模型链适配成用例层 Caller。
// judgeWithFallback 内含主→备切换、SkipModel/MarkResult 冷却、llm.Post，
// 是全局共享的 IO 实现（查中文释义的 llmChat 也吃这份冷却状态），不进用例层。
// 模型配置在这里持有，不进 Caller 接口 —— 用例层不认识 llm 包。
type judgeCaller struct{ cfg llm.Config }

func (c judgeCaller) Judge(prompt string) (judge.Result, string, error) {
	return judgeWithFallback(c.cfg, prompt)
}

var (
	_ judge.ConfigStore = (*Storage)(nil)
	_ judge.Caller      = judgeCaller{}
)

// resolveJudgePrompt 薄转发：模板选择 + 60 秒缓存已随用例搬进 internal/judge。
// 保留根名字，让 handleJudgePrompt 沿用。
func resolveJudgePrompt(ctx context.Context, st *Storage) (tpl, version, source string) {
	return judge.ResolvePrompt(ctx, st)
}

// judgeWithCache 薄转发：注入 *Storage（ConfigStore）与 judgeCaller（Caller）。
// 判定缓存、模板版本、fail-open 全在用例层，行为跟原实现逐字一致。
func judgeWithCache(ctx context.Context, st *Storage, cfg llm.Config, answer, chinese, input string) (judgeResult, string, string, error) {
	return judge.JudgeWithCache(ctx, st, judgeCaller{cfg: cfg}, answer, chinese, input)
}

// ---------- HTTP ----------

// POST /api/judge-sentence  { answer, chinese, input }
//
//	→ { enabled:true,  correct, reason, by:"llm"|"literal" }
//	→ { enabled:false }                       没配密钥，前端关掉这个功能
//	→ { enabled:true, correct:false, by:"literal", error }   模型挂了，按字面判
func (s *server) handleJudgeSentence(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "只用 POST",
		})
		return
	}

	cfg, enabled := llm.LoadConfig()
	if !enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": false,
			"hint": "服务端没配 JRP_LLM_ENDPOINT / JRP_LLM_API_KEY，造句按字面判定",
		})
		return
	}

	var in struct {
		Answer  string `json:"answer"`
		Chinese string `json:"chinese"`
		Input   string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "bad body: " + err.Error(),
		})
		return
	}
	in.Answer = strings.TrimSpace(in.Answer)
	in.Input = strings.TrimSpace(in.Input)
	if in.Answer == "" || in.Input == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "answer 和 input 都不能为空",
		})
		return
	}

	// 归一化后一致 —— 前端已经判对了，这里兜一下（换设备、旧前端）
	if sentence.Norm(in.Answer) == sentence.Norm(in.Input) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": true, "by": "literal",
		})
		return
	}

	res, ver, model, err := judgeWithCache(r.Context(), st, cfg, in.Answer, in.Chinese, in.Input)
	if err != nil {
		// 模型不可用不是业务错误：不返回 5xx，让前端退回字面判定继续练
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": false, "by": "literal",
			"reason": "模型不可用，按字面判定",
			"chain":  append([]string{cfg.Model}, cfg.Fallback...),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "enabled": true,
		"correct": res.Correct, "reason": res.Reason, "by": "llm",
		"pv":    ver,   // prompt 版本：存进草稿，改了 prompt 就作废旧结论
		"model": model, // 实际判这句的模型（主模型挂了会显示备用）
	})
}

// GET /api/judge-prompt
//
//	→ { enabled, version, source: "cos"|"builtin" }
//
// 只回版本不回内容：前端拿它判断草稿里的旧结论还能不能用。
// 顺带把「有没有配模型」告诉前端，省得前端非要发一次判定才知道。
func (s *server) handleJudgePrompt(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "只用 GET",
		})
		return
	}
	_, enabled := llm.LoadConfig()
	tpl, ver, src := resolveJudgePrompt(r.Context(), st)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"enabled": enabled,
		"version": ver,
		"source":  src,
		"chars":   len(tpl),
	})
}
