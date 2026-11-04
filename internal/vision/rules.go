package vision

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// Prompt 认词用的提示词。
//
// 「只输出 JSON 数组」这句不能省 —— 省了模型就开始给你讲解它看到了什么，
// 解析直接拿不到东西。第 1 条（例句/标题不要）是实测出来的：不给这条，
// 它会把「これは わたしの ほんです。」这种课文句子也当词收进来。
const Prompt = `你是日语老师。图片是日语课本的一页（可能是生词表）。
请提取其中的日语单词词条和对应的中文意思。

要求：
1. 只提取词条。例句、课文正文、标题、页码、序号都不要。
2. word 保留图片里的写法（包括括号），def 给简明中文意思；图片里没有中文就留空字符串。
3. 多个义项用顿号并列。
4. 同一个词出现多次只给一条。

只输出 JSON 数组，不要解释，不要代码块：[{"word": "みず(水)", "def": "水"}]`

// ParseReply 从模型回话里抠出 JSON 数组。
// 模型爱在外面套 ```json、给成对象包数组、或者前后加废话，三种都得接住。
func ParseReply(reply string) ([]Candidate, error) {
	s := stripCodeFence(reply)

	if l, r := strings.Index(s, "["), strings.LastIndex(s, "]"); l >= 0 && r > l {
		var out []Candidate
		if err := json.Unmarshal([]byte(s[l:r+1]), &out); err == nil && len(out) > 0 {
			return CleanCandidates(out), nil
		}
	}
	// 兜一层：模型给成 {"words":[...]} / {"items":[...]}
	var wrapped struct {
		Words []Candidate `json:"words"`
		Items []Candidate `json:"items"`
	}
	if l, r := strings.Index(s, "{"), strings.LastIndex(s, "}"); l >= 0 && r > l {
		if err := json.Unmarshal([]byte(s[l:r+1]), &wrapped); err == nil {
			got := wrapped.Words
			if len(got) == 0 {
				got = wrapped.Items
			}
			if len(got) > 0 {
				return CleanCandidates(got), nil
			}
		}
	}
	return nil, errors.New("模型没按 JSON 数组回")
}

// stripCodeFence 去掉 ```json ... ``` 外壳。
//
// 与 internal/worddef 的 stripCodeFence 是同一份纯规则（逐字一致），各放一份私有副本
// 是为了让两个用例包互不依赖；按老师「2 选 A」，P5 尾部薄壳合并那个独立 commit
// 再统一收敛到一处，P4 不为此新建公共包。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "```")
	if i < 0 {
		return s
	}
	s = s[i+3:]
	if j := strings.Index(s, "```"); j >= 0 {
		s = s[:j]
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "json") {
		s = strings.TrimSpace(s[4:])
	}
	return s
}

// HasJapanese 判断字符串里有没有日文（假名或汉字）—— 滤掉模型偶尔夹带的中文标题之类。
func HasJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

// CleanCandidates 去空、去噪、去重。
func CleanCandidates(in []Candidate) []Candidate {
	out := make([]Candidate, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		w := strings.TrimSpace(c.Word)
		if w == "" || !HasJapanese(w) {
			continue
		}
		if len([]rune(w)) > 24 { // 整句话，不是词条
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, Candidate{Word: w, Def: strings.TrimSpace(c.Def)})
	}
	return out
}
