package main

import (
	"encoding/json"
	"net/http"
	"strings"
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

// WordDefItem 是一个词查回来的结果。word 是建议录进档案的词形（可能带了括号汉字）。
type WordDefItem struct {
	Input string `json:"input"` // 老师原来打的
	Word  string `json:"word"`  // 建议录进去的（补了汉字）
	Def   string `json:"def"`   // 中文释义
}

const wordDefPrompt = `你是日语词典。给下面这些日语词条补上汉字写法和中文释义。

词条：
{words}

要求：
1. kanji 填这个词的汉字写法；这个词本来就不写汉字的（さん、ありがとう 这类）就填空字符串。
2. def 填中文意思：只给这个词本身的意思，不要例句、不要读音、不要词性说明；
   意思不止一个时用顿号并列最常见的两三个。
3. 动词、形容词给基本形的意思。
4. word 原样返回词条，不要改动。

只输出一行 JSON 数组，不要解释、不要代码块：
[{"word": "みず", "kanji": "水", "def": "水"}, {"word": "さん", "kanji": "", "def": "先生、女士等称呼"}]`

func fillWordDefPrompt(tpl string, words []string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return strings.ReplaceAll(tpl, "{words}", strings.TrimRight(b.String(), "\n"))
}

// hasParen 判断词形里已经带了括号（老师自己写了汉字或注释），这种就别再补。
func hasParen(s string) bool {
	return strings.ContainsAny(s, "(（")
}

// findWordByKana 按假名部分找词：老师打「みず」，库里存的是「みず(水)」。
func findWordByKana(groups []WordGroup, kana string) *Word {
	for gi := range groups {
		for wi := range groups[gi].Words {
			w := &groups[gi].Words[wi]
			if k, _, _ := splitLexiconWord(w.Word); k == kana {
				return w
			}
		}
	}
	return nil
}

var errNoWordDefJSON = &wordDefError{"模型没按 JSON 回"}

type wordDefError struct{ msg string }

func (e *wordDefError) Error() string { return e.msg }

// llmWordDef 模型返回的一行：word 原样 + kanji 汉字 + def 释义。
type llmWordDef struct {
	Word  string `json:"word"`
	Kanji string `json:"kanji"`
	Def   string `json:"def"`
}

// lookupWordDefs 批量查「中文释义 + 汉字写法」：先档案，档案没有再问模型。
//
// 抽成函数是因为两处要用，口径必须一模一样：
//   - POST /api/word-def（手打录入时补中文）
//   - POST /api/vision（拍照认完词顺手补 —— 2026-10-21 老师反馈「认出来的词没有中文释义」）
//
// 返回按入参顺序排好的结果，以及生效的模型名。模型不可用时 Def 留空，不算错误。
func (s *server) lookupWordDefs(st *Storage, words []string) ([]WordDefItem, string) {
	var items []WordDefItem
	var todo []string

	// ① 档案优先：精确命中 → 按假名命中（顺带把汉字补上）
	if data, _, err := st.DownloadLatestArchive(s.ctx()); err == nil {
		if arc, err := ParseArchive(string(data), st.lang); err == nil {
			for _, w := range words {
				if existing, _ := FindWord(arc.Groups, w); existing != nil &&
					strings.TrimSpace(existing.Definition) != "" {
					items = append(items, WordDefItem{Input: w, Word: existing.Word, Def: strings.TrimSpace(existing.Definition)})
					continue
				}
				if !hasParen(w) {
					if existing := findWordByKana(arc.Groups, w); existing != nil &&
						strings.TrimSpace(existing.Definition) != "" {
						items = append(items, WordDefItem{Input: w, Word: existing.Word, Def: strings.TrimSpace(existing.Definition)})
						continue
					}
				}
				todo = append(todo, w)
			}
		} else {
			todo = words
		}
	} else {
		todo = words
	}

	// ② 剩下的问模型
	model := ""
	if len(todo) > 0 {
		if cfg, ok := loadLLMConfig(); ok {
			reply, used, err := llmChat(cfg, fillWordDefPrompt(wordDefPrompt, todo))
			if err == nil {
				model = used
				var got []llmWordDef
				if raw, perr := parseWordDefReplyRaw(reply); perr == nil {
					got = raw
				}
				byWord := map[string]llmWordDef{}
				for _, g := range got {
					byWord[strings.TrimSpace(g.Word)] = g
				}
				for _, w := range todo {
					it := WordDefItem{Input: w, Word: w}
					if g, ok := byWord[w]; ok {
						it.Def = strings.TrimSpace(g.Def)
						// 汉字补进半角括号 —— 半角括号在档案里的含义就是「汉字写法」
						if k := strings.TrimSpace(g.Kanji); k != "" && !hasParen(w) {
							it.Word = w + "(" + k + ")"
						}
					}
					items = append(items, it)
				}
			}
		}
		// 模型挂了 / 没配 / 回答里漏了某个词：都按「没有释义」补一条，
		// 前端留给老师自己填，不阻断录入。
	}

	// 保序输出（档案命中和模型回答是分两批来的，要按请求的顺序还回去）
	ordered := make([]WordDefItem, 0, len(words))
	byInput := map[string]WordDefItem{}
	for _, it := range items {
		byInput[it.Input] = it
	}
	for _, w := range words {
		it, ok := byInput[w]
		if !ok {
			it = WordDefItem{Input: w, Word: w}
		}
		ordered = append(ordered, it)
	}
	return ordered, model
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

// parseWordDefReplyRaw 抠模型返回的 JSON 数组（带 kanji 字段）。
func parseWordDefReplyRaw(reply string) ([]llmWordDef, error) {
	s := stripCodeFence(reply)
	l := strings.Index(s, "[")
	r := strings.LastIndex(s, "]")
	if l < 0 || r <= l {
		return nil, errNoWordDefJSON
	}
	var out []llmWordDef
	if err := json.Unmarshal([]byte(s[l:r+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}
