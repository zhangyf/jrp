package main

import (
	"strings"
	"testing"
)

// 词性 / 备注 join 到练习题上（老师 2026-09-28 要的：卡片上不能只有中文释义）。
// 这两样存在独立的 word_meta.json 里，按完整词形「假名(汉字)」匹配。
func TestLookupWordMeta(t *testing.T) {
	meta := &WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "空间上从某处穿过；過ぎます 是时间流逝"},
		"なかなか":        {Pos: "副词", Note: "程度副词，比预想要高"},
		"おふろ(お風呂)":    {Pos: "名词"},
	}}

	// 三样齐全
	pos, sub, note := lookupWordMeta(meta, "とおります(通ります)")
	if pos != "动词" || sub != "一类" || note != "空间上从某处穿过；過ぎます 是时间流逝" {
		t.Errorf("词性/细分/备注应同时取到，got %q %q %q", pos, sub, note)
	}

	// 没细分的（名词/副词没有 sub）
	pos, sub, note = lookupWordMeta(meta, "なかなか")
	if pos != "副词" || sub != "" || note != "程度副词，比预想要高" {
		t.Errorf("没细分时 sub 应为空，got %q %q %q", pos, sub, note)
	}

	// 没备注 → note 空着。这不是缺数据：词义明确的词本来就不用写，
	// 页面会拿释义括号里那截兜底（前端 app.defParts）。
	pos, sub, note = lookupWordMeta(meta, "おふろ(お風呂)")
	if pos != "名词" || sub != "" || note != "" {
		t.Errorf("没备注的应返回空 note，got %q %q %q", pos, sub, note)
	}

	// 词性表里没有这个词（比如新导入还没标注）→ 全空，页面显示「—」
	pos, sub, note = lookupWordMeta(meta, "あたらしい(新しい)")
	if pos != "" || sub != "" || note != "" {
		t.Errorf("未标注的词应返回空，got %q %q %q", pos, sub, note)
	}

	// 词形对不上（裸假名 vs 带汉字）不猜 —— 猜错会把另一个词的词性贴上来
	pos, sub, note = lookupWordMeta(meta, "とおります")
	if pos != "" || sub != "" || note != "" {
		t.Errorf("裸假名不该命中带汉字的条目，got %q %q %q", pos, sub, note)
	}

	// 词性表整个读不到（COS 挂了 / 还没上传）→ 不能炸，整批留空
	pos, sub, note = lookupWordMeta(nil, "とおります(通ります)")
	if pos != "" || sub != "" || note != "" {
		t.Errorf("meta 为 nil 应返回空，got %q %q %q", pos, sub, note)
	}
	pos, sub, note = lookupWordMeta(&WordMeta{}, "とおります(通ります)")
	if pos != "" || sub != "" || note != "" {
		t.Errorf("空表应返回空，got %q %q %q", pos, sub, note)
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
