package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// 查中文意思：录入新词时不用手打释义，服务端自己补。
//
//	POST /api/word-def { words:["みず(水)", "たべもの"] }
//	→ { success, enabled, defs:{"みず(水)":"水", "たべもの":"食物"}, model:"…" }
//
// 两条来源，按顺序：
//  1. **档案里已有这个词** → 直接用它的释义（最准、最快、不要钱，而且保证跟
//     复习时看到的中文一致 —— 同一个词两处释义不一样等于白练）。
//  2. 剩下的才问模型。
//
// ⚠️ 模型不可用（没配 JRP_LLM_*）不是错误：返回 enabled:false + 已有的那部分。
// 前端看到缺释义就把条目留在列表里让老师补，不阻断录入。

const wordDefPrompt = `你是日语词典。给下面这些日语词条各配一个中文释义。

词条：
{words}

要求：
1. 只给这个词条本身的意思，不要例句、不要读音、不要词性说明。
2. 意思不止一个时，用顿号并列最常见的两三个（例：家、房子）。
3. 词条里的半角括号是汉字写法（みず(水) = 水），全角括号是语法注释（さん（敬称）= 先生/女士等称呼）。
4. 动词、形容词给基本形的意思就行。
5. 拿不准就给最常用的那个意思，别写「未知」。

只输出一行 JSON，不要解释：{"みず(水)": "水", "たべもの": "食物"}`

func fillWordDefPrompt(tpl string, words []string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return strings.ReplaceAll(tpl, "{words}", strings.TrimRight(b.String(), "\n"))
}

// parseWordDefReply 从模型回话里抠出 JSON。模型爱在外面套 ```json 和前后废话，
// 所以取第一个 { 到最后一个 } 之间的一段再解。
func parseWordDefReply(reply string) (map[string]string, error) {
	s := strings.TrimSpace(reply)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
		if strings.HasPrefix(strings.ToLower(s), "json") {
			s = strings.TrimSpace(s[4:])
		}
	}
	l := strings.Index(s, "{")
	r := strings.LastIndex(s, "}")
	if l < 0 || r <= l {
		return nil, errNoWordDefJSON
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s[l:r+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

var errNoWordDefJSON = &wordDefError{"模型没按 JSON 回"}

type wordDefError struct{ msg string }

func (e *wordDefError) Error() string { return e.msg }

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

	defs := map[string]string{}
	var todo []string

	// ① 档案里有的直接用 —— 保证跟复习时看到的中文是同一份
	if data, _, err := st.DownloadLatestArchive(s.ctx()); err == nil {
		if arc, err := ParseArchive(string(data), st.lang); err == nil {
			for _, w := range words {
				if existing, _ := FindWord(arc.Groups, w); existing != nil &&
					strings.TrimSpace(existing.Definition) != "" {
					defs[w] = strings.TrimSpace(existing.Definition)
					continue
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
				if got, perr := parseWordDefReply(reply); perr == nil {
					for _, w := range todo {
						if d := strings.TrimSpace(got[w]); d != "" {
							defs[w] = d
						}
					}
				}
			}
		}
	}

	out := map[string]interface{}{
		"success": true,
		"enabled": model != "",
		"defs":    defs,
		"missing": missingWords(words, defs),
	}
	if model != "" {
		out["model"] = model
	}
	writeJSON(w, http.StatusOK, out)
}

func missingWords(words []string, defs map[string]string) []string {
	var out []string
	for _, w := range words {
		if _, ok := defs[w]; !ok {
			out = append(out, w)
		}
	}
	return out
}
