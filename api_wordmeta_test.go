package main

import (
	"strings"
	"testing"

	"github.com/zhangyf/jrp/internal/wordmeta"
)

// 词性 / 备注 / 圆级 join 到练习题上（老师 2026-09-28 要的：卡片上不能只有中文释义；
// 圆级是 2026-11-03 加的「单词复习时展示圆级」）。
// 这几样存在独立的 word_meta.json 里，按完整词形「假名(汉字)」匹配。
func TestLookupWordMeta(t *testing.T) {
	meta := &WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "空间上从某处穿过；過ぎます 是时间流逝", Circle: "三圆"},
		"なかなか":        {Pos: "副词", Note: "程度副词，比预想要高", Circle: "二圆"},
		"おふろ(お風呂)":    {Pos: "名词"},
	}}

	// 四样齐全
	pos, sub, note, circle := wordmeta.LookupWordMeta(meta, "とおります(通ります)")
	if pos != "动词" || sub != "一类" || note != "空间上从某处穿过；過ぎます 是时间流逝" || circle != "三圆" {
		t.Errorf("词性/细分/备注/圆级应同时取到，got %q %q %q %q", pos, sub, note, circle)
	}

	// 没细分的（名词/副词没有 sub）
	pos, sub, note, circle = wordmeta.LookupWordMeta(meta, "なかなか")
	if pos != "副词" || sub != "" || note != "程度副词，比预想要高" || circle != "二圆" {
		t.Errorf("没细分时 sub 应为空，got %q %q %q %q", pos, sub, note, circle)
	}

	// 没备注 → note 空着。这不是缺数据：词义明确的词本来就不用写，
	// 页面会拿释义括号里那截兜底（前端 app.defParts）。
	// 手册没收的词 circle 也空着 —— 页面不显示这一格，不是「没标」。
	pos, sub, note, circle = wordmeta.LookupWordMeta(meta, "おふろ(お風呂)")
	if pos != "名词" || sub != "" || note != "" || circle != "" {
		t.Errorf("没备注/没分级的应返回空，got %q %q %q %q", pos, sub, note, circle)
	}

	// 词性表里没有这个词（比如新导入还没标注）→ 全空，页面显示「—」
	pos, sub, note, circle = wordmeta.LookupWordMeta(meta, "あたらしい(新しい)")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("未标注的词应返回空，got %q %q %q %q", pos, sub, note, circle)
	}

	// 词形对不上（裸假名 vs 带汉字）不猜 —— 猜错会把另一个词的词性贴上来
	pos, sub, note, circle = wordmeta.LookupWordMeta(meta, "とおります")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("裸假名不该命中带汉字的条目，got %q %q %q %q", pos, sub, note, circle)
	}

	// 词性表整个读不到（COS 挂了 / 还没上传）→ 不能炸，整批留空
	pos, sub, note, circle = wordmeta.LookupWordMeta(nil, "とおります(通ります)")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("meta 为 nil 应返回空，got %q %q %q %q", pos, sub, note, circle)
	}
	pos, sub, note, circle = wordmeta.LookupWordMeta(&WordMeta{}, "とおります(通ります)")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("空表应返回空，got %q %q %q %q", pos, sub, note, circle)
	}
}

// 圆级必须真的 join 到当天练习的每个词上（2026-11-03）：
// 后端不带上，前端再怎么画也是「—」。
func TestEnrichWordMetaCarriesCircle(t *testing.T) {
	meta := &WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Circle: "三圆"},
		"おふろ(お風呂)":    {Circle: "一圆"},
	}}
	words := []PlanWord{{Word: "とおります(通ります)"}, {Word: "おふろ(お風呂)"}, {Word: "なかなか"}}
	for i := range words {
		words[i].Pos, words[i].Sub, words[i].Note, words[i].Circle =
			wordmeta.LookupWordMeta(meta, words[i].Word)
	}
	want := []string{"三圆", "一圆", ""}
	for i, w := range words {
		if w.Circle != want[i] {
			t.Errorf("words[%d].Circle = %q, want %q", i, w.Circle, want[i])
		}
	}
	// omitempty：手册没收的词不该往接口里塞一个空 circle 字段
	if one := toJSON(words[2]); strings.Contains(one, "circle") {
		t.Errorf("没分级的词不该出现 circle 字段，got %s", one)
	}
}

// 备注要能存进 word_meta.json 再读回来，且没写备注的词不该多出一个字段。
func TestWordPOSNoteRoundTrip(t *testing.T) {
	m := WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "空间上穿过"},
		"おふろ(お風呂)":    {Pos: "名词"},
	}}
	s := toJSON(m)

	var back WordMeta
	if err := jsonUnmarshal([]byte(s), &back); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got := back.Items["とおります(通ります)"].Note; got != "空间上穿过" {
		t.Errorf("备注应 round-trip，got %q", got)
	}
	if got := back.Items["おふろ(お風呂)"].Note; got != "" {
		t.Errorf("没备注的词读回来应为空，got %q", got)
	}
	// omitempty：写了备注的才序列化，没写的不该往 900 多条的文件里塞一堆空字段
	if one := toJSON(m.Items["とおります(通ります)"]);
		!strings.Contains(one, `"note"`) || !strings.Contains(one, "空间上穿过") {
		t.Errorf("写了备注的应序列化出来，got %s", one)
	}
	if one := toJSON(m.Items["おふろ(お風呂)"]); strings.Contains(one, "note") {
		t.Errorf("没备注的词不该出现 note 字段，got %s", one)
	}
}
