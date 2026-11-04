package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/zhangyf/jrp/internal/llm"
	"github.com/zhangyf/jrp/internal/worddef"
)

// 查中文意思 + 补汉字写法：录入新词时只用打假名，剩下的服务端补齐。
//
//	POST /api/word-def { words:["みず", "たべもの"] }
//	→ { success, enabled, items:[{input:"みず", word:"みず(水)", def:"水"}], missing:[…] }
//
// 老师 2026-10-21 反馈「这个日语词的汉字不加上吗」：光给中文没用，档案里词形是
// 「假名(汉字)」，不补汉字的话这个词录进去就没有汉字，复习时根本学不到写法。
//
// 两条来源，按顺序：
//  1. **档案里已有这个词** → 直接用它的**完整词形 + 释义**（最准、不要钱，
//     而且保证跟复习时看到的中文是同一份 —— 同一个词两处释义不一样等于白练）。
//     先按原样精确找，找不到再按假名部分找（老师打「みず」，库里是「みず(水)」）。
//  2. 剩下的才问模型，模型一并给汉字写法。
//
// ⚠️ 模型不可用（没配 JRP_LLM_*）不是错误：返回 enabled:false + 已有的那部分。
// 前端看到缺释义就把条目留在列表里让老师补，不阻断录入。

// WordDefItem 保根包名字，定义随迁到 internal/worddef（类型别名 = 同一类型，JSON 字节不变）。
// api_vision.go 也用这个名字，故保留别名不改名。
type WordDefItem = worddef.Item

// hasParen 是给 api_vision.go 借用的薄壳；实现已在 internal/worddef。
// vision 域（P4 后一域）合并薄壳时再一起收口。
func hasParen(s string) bool { return worddef.HasParen(s) }

// 编译期锁住接口实现关系：*Storage 读档案、wordDefCaller 问模型链。
var _ worddef.ArchiveStore = (*Storage)(nil)
var _ worddef.Caller = wordDefCaller{}

var errWordDefNoModel = errors.New("没配置大模型")

// wordDefCaller 把「模型是否可用 / 主备链 / 冷却 / 真正发请求」全留在根包 IO 层，
// 与 llm_failover.go 的 judgeWithFallback 共用 llm.SkipModel / MarkResult 冷却状态。
// 用例层只认 worddef.Caller，不认识 llm.Config / LoadConfig / llmChat。
type wordDefCaller struct{}

func (wordDefCaller) Lookup(prompt string) (string, string, error) {
	cfg, ok := llm.LoadConfig()
	if !ok {
		return "", "", errWordDefNoModel
	}
	return llmChat(cfg, prompt)
}

// lookupWordDefs 是两入口（/api/word-def 与 /api/vision）共用的薄适配：
// 注入 ctx / lang / 档案读取 / 模型调用，编排本体在 internal/worddef.Lookup。
// 抽成函数是因为两处要用，口径必须一模一样：
//   - POST /api/word-def（手打录入时补中文）
//   - POST /api/vision（拍照认完词顺手补 —— 2026-10-21 老师反馈「认出来的词没有中文释义」）
//
// 返回按入参顺序排好的结果，以及生效的模型名。模型不可用时 Def 留空，不算错误。
func (s *server) lookupWordDefs(st *Storage, words []string) ([]WordDefItem, string) {
	return worddef.Lookup(s.ctx(), st, wordDefCaller{}, st.lang, words)
}

func (s *server) handleWordDef(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var body struct {
		Words []string `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "请求格式不对：" + err.Error(),
		})
		return
	}

	// 去重、去空，保序
	seen := map[string]bool{}
	var words []string
	for _, w := range body.Words {
		w = strings.TrimSpace(w)
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	if len(words) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "没有要查的词",
		})
		return
	}
	if len(words) > 60 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "一次最多查 60 个",
		})
		return
	}

	items, model := s.lookupWordDefs(st, words)

	var missing []string
	for _, it := range items {
		if it.Def == "" {
			missing = append(missing, it.Input)
		}
	}

	out := map[string]interface{}{
		"success": true,
		"enabled": model != "",
		"items":   items,
		"missing": missing,
	}
	if model != "" {
		out["model"] = model
	}
	writeJSON(w, http.StatusOK, out)
}
