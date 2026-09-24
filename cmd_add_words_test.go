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
