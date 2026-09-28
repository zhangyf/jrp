package main

import "testing"

// 中文释义撞车：两个词释义一字不差，复习时看中文分不出该写哪个。
// 2026-09-24 老师反馈 だめ / いけません 都写成「不行，不可以」。
func TestSameDefinitionWords(t *testing.T) {
	groups := []WordGroup{
		{Title: "A", Words: []Word{
			{Word: "だめ", Definition: "不行，不可以"},
			{Word: "いけません", Definition: "不行，不可以（接て形）"},
			{Word: "エアコン", Definition: "空调"},
		}},
		{Title: "B", Words: []Word{
			{Word: "クーラー", Definition: "空调"},
			{Word: "むり", Definition: "勉强"},
		}},
	}

	got := sameDefinitionWords(groups, "空调", "")
	if len(got) != 2 || got[0] != "エアコン" || got[1] != "クーラー" {
		t.Errorf("空调 撞车词 = %v, want [エアコン クーラー]", got)
	}

	// 排除自己：update-def 改完不该把自己报出来
	got = sameDefinitionWords(groups, "空调", "クーラー")
	if len(got) != 1 || got[0] != "エアコン" {
		t.Errorf("排除自己后 = %v, want [エアコン]", got)
	}

	// 已加括号区分的不再算撞车
	if got := sameDefinitionWords(groups, "不行，不可以（接て形）", ""); len(got) != 1 || got[0] != "いけません" {
		t.Errorf("带区分的 = %v, want [いけません]", got)
	}

	// 空释义不算撞车（否则一堆空释义的词会互相报警）
	if got := sameDefinitionWords(groups, "", ""); len(got) != 0 {
		t.Errorf("空释义不该报撞车，got %v", got)
	}
	if got := sameDefinitionWords(groups, "   ", ""); len(got) != 0 {
		t.Errorf("空白释义不该报撞车，got %v", got)
	}

	// 唯一的释义不报
	if got := sameDefinitionWords(groups, "勉强", ""); len(got) != 1 || got[0] != "むり" {
		t.Errorf("勉强 = %v, want [むり]", got)
	}
}

func TestDefinitionCore(t *testing.T) {
	cases := map[string]string{
		"开(电器/灯)":     "开",
		"开（灯）":       "开",
		"浴室、洗澡（泡澡）":   "浴室、洗澡",
		"过":          "过",
		"通过，穿过（空间上…）": "通过，穿过",
		"（只有括号）":      "",
		"   ":        "",
	}
	for in, want := range cases {
		if got := definitionCore(in); got != want {
			t.Errorf("definitionCore(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameCoreWords(t *testing.T) {
	groups := []WordGroup{{Title: "g", Words: []Word{
		{Word: "とても", Definition: "非常"},
		{Word: "あけます", Definition: "开(门窗)"},
		{Word: "つけます", Definition: "开(电器/灯)"},
		{Word: "すぎます", Definition: "过"},
	}}}
	// 义项部分重合（老师 09-28 反馈的真实场景：なかなか「相当，很，非常」vs とても「非常」）
	if got := sameCoreWords(groups, "相当，很，非常", "なかなか"); len(got) != 1 || got[0] != "とても" {
		t.Errorf("共享义项「非常」应命中とても，got %v", got)
	}
	// 主部完全相同也命中
	if got := sameCoreWords(groups, "非常", "XXX"); len(got) != 1 || got[0] != "とても" {
		t.Errorf("同义项应命中とても，got %v", got)
	}
	// 两边都带区分括号 → 不算撞车（开(门窗) / 开(电器/灯) 是合格范例）
	if got := sameCoreWords(groups, "开(门窗类)", "XXXX"); len(got) != 0 {
		t.Errorf("两边都有括号区分点不应报警，got %v", got)
	}
	// 自己有括号、对方是裸释义 → 对方没区分度，报
	if got := sameCoreWords(groups, "过（时间流逝）", "とおります"); len(got) != 1 || got[0] != "すぎます" {
		t.Errorf("对方无括号应报警，got %v", got)
	}
	// 义项集合完全不同 → 不报
	if got := sameCoreWords(groups, "迟到", "ちこく"); len(got) != 0 {
		t.Errorf("无义项交集不应报警，got %v", got)
	}
	// 空释义不报
	if got := sameCoreWords(groups, "", "x"); len(got) != 0 {
		t.Errorf("空释义不应报警，got %v", got)
	}
	// 只含括号（主部为空）不报
	if got := sameCoreWords(groups, "（仅注释）", "x"); len(got) != 0 {
		t.Errorf("主部为空不应报警，got %v", got)
	}
}
