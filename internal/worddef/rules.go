package worddef

import (
	"encoding/json"
	"strings"

	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/textutil"
	"github.com/zhangyf/jrp/internal/words"
)

// Item 是一个词查回来的结果。Word 是建议录进档案的词形（可能带了括号汉字）。
// 根包用 `type WordDefItem = worddef.Item` 保 HTTP 响应 JSON 字节不变。
type Item struct {
	Input string `json:"input"` // 老师原来打的
	Word  string `json:"word"`  // 建议录进去的（补了汉字）
	Def   string `json:"def"`   // 中文释义
}

// LLMItem 模型返回的一行：word 原样 + kanji 汉字 + def 释义。
type LLMItem struct {
	Word  string `json:"word"`
	Kanji string `json:"kanji"`
	Def   string `json:"def"`
}

// Prompt 是查释义模板。要求补汉字这一条不能丢（老师 2026-10-21 发现没补汉字）。
const Prompt = `你是日语词典。给下面这些日语词条补上汉字写法和中文释义。

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

// FillPrompt 把词条列表填进模板的 {words} 占位符。
func FillPrompt(tpl string, words []string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return strings.ReplaceAll(tpl, "{words}", strings.TrimRight(b.String(), "\n"))
}

// HasParen 判断词形里已经带了括号（老师自己写了汉字或注释），这种就别再补。
func HasParen(s string) bool {
	return strings.ContainsAny(s, "(（")
}

// FindWordByKana 按假名部分找词：老师打「みず」，库里存的是「みず(水)」。
func FindWordByKana(groups []review.WordGroup, kana string) *review.Word {
	for gi := range groups {
		for wi := range groups[gi].Words {
			w := &groups[gi].Words[wi]
			if k, _, _ := words.SplitLexiconWord(w.Word); k == kana {
				return w
			}
		}
	}
	return nil
}

var errNoJSON = &wordDefError{"模型没按 JSON 回"}

type wordDefError struct{ msg string }

func (e *wordDefError) Error() string { return e.msg }

// ParseReply 抠模型返回的 JSON 数组（带 kanji 字段）。
func ParseReply(reply string) ([]LLMItem, error) {
	s := textutil.StripCodeFence(reply)
	l := strings.Index(s, "[")
	r := strings.LastIndex(s, "]")
	if l < 0 || r <= l {
		return nil, errNoJSON
	}
	var out []LLMItem
	if err := json.Unmarshal([]byte(s[l:r+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}
