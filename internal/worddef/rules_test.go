package worddef

import (
	"strings"
	"testing"

	"github.com/zhangyf/jrp/internal/review"
)

// 模型爱在 JSON 外面套 ```json 和前后废话，抠 JSON 这层必须稳。
func TestParseReply(t *testing.T) {
	cases := []struct {
		name, reply, word, kanji, def string
	}{
		{"纯 JSON", `[{"word":"みず","kanji":"水","def":"水"}]`, "みず", "水", "水"},
		{"代码块", "```json\n[{\"word\":\"みず\",\"kanji\":\"水\",\"def\":\"水\"}]\n```", "みず", "水", "水"},
		{"前后废话", "好的：\n[{\"word\":\"みず\",\"kanji\":\"水\",\"def\":\"水\"}]\n希望有帮助。", "みず", "水", "水"},
		{"没汉字的词", `[{"word":"さん","kanji":"","def":"称呼"}]`, "さん", "", "称呼"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseReply(c.reply)
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if len(got) != 1 {
				t.Fatalf("得到 %d 条，想要 1 条", len(got))
			}
			if got[0].Word != c.word || got[0].Kanji != c.kanji || got[0].Def != c.def {
				t.Errorf("%+v，想要 word=%q kanji=%q def=%q", got[0], c.word, c.kanji, c.def)
			}
		})
	}

	if _, err := ParseReply("我没有找到答案"); err == nil {
		t.Error("没有 JSON 时应该报错")
	}
}

// 要汉字这一条不能从 prompt 里丢掉 —— 老师 2026-10-21 就是发现没补汉字。
func TestFillPromptKeepsKanjiRule(t *testing.T) {
	p := FillPrompt(Prompt, []string{"みず", "たべもの"})
	if !strings.Contains(p, "- みず") || !strings.Contains(p, "- たべもの") {
		t.Error("词条没进 prompt：" + p)
	}
	if strings.Contains(p, "{words}") {
		t.Error("占位符没替换")
	}
	if !strings.Contains(p, "kanji 填这个词的汉字写法") {
		t.Error("要汉字的规则丢了")
	}
}

// 老师自己写了括号（みず(水) 或 さん（敬称））就别再补一层括号。
func TestHasParen(t *testing.T) {
	if !HasParen("みず(水)") || !HasParen("さん（敬称）") {
		t.Error("带括号的词形应该被认出来")
	}
	if HasParen("みず") {
		t.Error("没括号的不该被认成有")
	}
}

// 老师打「みず」，库里存的是「みず(水)」—— 要能按假名部分找到，把汉字补回来。
func TestFindWordByKana(t *testing.T) {
	groups := []review.WordGroup{{Title: "g", Words: []review.Word{
		{Word: "みず(水)", Definition: "水"},
		{Word: "たべもの", Definition: "食物"},
	}}}
	if w := FindWordByKana(groups, "みず"); w == nil || w.Word != "みず(水)" {
		t.Errorf("按假名没找到 みず(水)：%+v", w)
	}
	if w := FindWordByKana(groups, "たべもの"); w == nil || w.Word != "たべもの" {
		t.Errorf("没汉字的词也要能找到：%+v", w)
	}
	if w := FindWordByKana(groups, "でんわ"); w != nil {
		t.Error("库里没有的不该找到")
	}
}
