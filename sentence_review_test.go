package main

import "testing"

func TestGroupLesson(t *testing.T) {
	cases := []struct {
		group string
		want  int
	}{
		{"第1课01 基础词（5/25）", 1},
		{"第13课01 基础词（10/25）", 13},
		{"第3课02 应用词（8/12）", 3},
		{"单元末", 0},
		{"复习2026-09-21", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := groupLesson(c.group); got != c.want {
			t.Errorf("groupLesson(%q) = %d, want %d", c.group, got, c.want)
		}
	}
}

func TestRoundAcc(t *testing.T) {
	if got := roundAcc(0.9166666666666666); got != 0.92 {
		t.Errorf("roundAcc = %v, want 0.92", got)
	}
	if got := roundAcc(1); got != 1 {
		t.Errorf("roundAcc(1) = %v, want 1", got)
	}
}

func TestGroupProtosByLesson(t *testing.T) {
	ss := []BankSentence{
		{ID: "A1", Lesson: "第3课", Source: "应用课文", Answer: "とても 長い アプリの 文です。"},
		{ID: "B1", Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"},
		{ID: "B2", Lesson: "第3课", Source: "基本课文", Answer: "トイレは どこに ありますか。"},
		{ID: "C1", Lesson: "第5课", Source: "基本课文", Answer: "今 4時です。"},
		{ID: "D1", Lesson: "单元末", Source: "阅读文", Answer: "今日は 金曜日です。"},
	}
	by := groupProtosByLesson(ss)
	if len(by[3]) != 3 || len(by[5]) != 1 || len(by[0]) != 1 {
		t.Fatalf("分组不对：%v", by)
	}
	// 同课内：基本课文在前，短的在前
	if by[3][0].ID != "B1" || by[3][1].ID != "B2" || by[3][2].ID != "A1" {
		t.Errorf("第3课排序不对：%+v", by[3])
	}
}

func TestPickPrototypesSameLessonFirst(t *testing.T) {
	ss := []BankSentence{
		{ID: "P1", Lesson: "第4课", Source: "基本课文", Answer: "部屋に 机と 椅子が あります。"},
		{ID: "P2", Lesson: "第4课", Source: "基本课文", Answer: "机の 上に 猫が います。"},
		{ID: "P3", Lesson: "第4课", Source: "基本课文", Answer: "売店は 駅の 外に あります。"},
		{ID: "P4", Lesson: "第4课", Source: "基本课文", Answer: "吉田さんは 庭に います。"},
		{ID: "Q1", Lesson: "第2课", Source: "基本课文", Answer: "これは 本です。"},
	}
	by := groupProtosByLesson(ss)
	got := pickPrototypes(by, 4, 3, "名词")
	if len(got) != 3 {
		t.Fatalf("要 3 条，给了 %d 条", len(got))
	}
	for _, g := range got {
		if g.Lesson != "第4课" {
			t.Errorf("同课的句子够用时不该跨课：%s %s", g.ID, g.Lesson)
		}
	}
}

func TestPickPrototypesFallsBackToNearbyLesson(t *testing.T) {
	ss := []BankSentence{
		{ID: "P1", Lesson: "第7课", Source: "基本课文", Answer: "李さんは 毎日 コーヒーを 飲みます。"},
		{ID: "P2", Lesson: "第6课", Source: "基本课文", Answer: "李さんは 先月 北京から 来ました。"},
		{ID: "P3", Lesson: "第8课", Source: "基本课文", Answer: "李さんは 日本語で 手紙を 書きます。"},
	}
	by := groupProtosByLesson(ss)
	got := pickPrototypes(by, 7, 3, "名词")
	if len(got) != 3 {
		t.Fatalf("要 3 条，给了 %d 条", len(got))
	}
	if got[0].ID != "P1" {
		t.Errorf("第一条应该是同课的 P1，实际 %s", got[0].ID)
	}
	// 距离 7 相等时先减后加：第6课 在 第8课 前面
	if got[1].ID != "P2" || got[2].ID != "P3" {
		t.Errorf("跨课顺序不对：%s %s", got[1].ID, got[2].ID)
	}
}

func TestPickPrototypesUnknownLesson(t *testing.T) {
	ss := []BankSentence{
		{ID: "P1", Lesson: "第2课", Source: "基本课文", Answer: "これは 本です。"},
		{ID: "P2", Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"},
	}
	by := groupProtosByLesson(ss)
	got := pickPrototypes(by, 0, 2, "名词")
	if len(got) != 2 {
		t.Fatalf("课号未知时要能兜底给满，实际 %d 条", len(got))
	}
}

func TestProtoScoreNounNeedsSlot(t *testing.T) {
	// 名词要找有名词槽的句子：「はい、そうです。」这种没槽位的必须被压下去
	withSlot := BankSentence{Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"}
	noSlot := BankSentence{Lesson: "第3课", Source: "基本课文", Answer: "はい、そうです。"}
	if protoScore(withSlot, "名词") <= protoScore(noSlot, "名词") {
		t.Errorf("有名词槽的句子该排前面：有槽=%d 无槽=%d",
			protoScore(withSlot, "名词"), protoScore(noSlot, "名词"))
	}
}

func TestProtoScoreVerbPrefersMasu(t *testing.T) {
	masu := BankSentence{Lesson: "第7课", Source: "基本课文", Answer: "李さんは 毎日 コーヒーを 飲みます。"}
	desu := BankSentence{Lesson: "第7课", Source: "基本课文", Answer: "これは 本です。"}
	if protoScore(masu, "动词") <= protoScore(desu, "动词") {
		t.Errorf("动词该优先ます结尾的：ます=%d です=%d",
			protoScore(masu, "动词"), protoScore(desu, "动词"))
	}
}

func TestProtoScoreAdjPrefersKeiyoushi(t *testing.T) {
	i := BankSentence{Lesson: "第9课", Source: "基本课文", Answer: "四川料理は 辛いです。"}
	n := BankSentence{Lesson: "第9课", Source: "基本课文", Answer: "これは 本です。"}
	if protoScore(i, "形容词") <= protoScore(n, "形容词") {
		t.Errorf("形容词该优先い形句：い=%d 普通=%d",
			protoScore(i, "形容词"), protoScore(n, "形容词"))
	}
}

// 真实场景回归：给地名「なごや」推荐时，第4课里带地点槽的「売店は 駅の 外に あります。」
// 必须排在「だれも いません。」前面 —— 早期版本只按短句优先，推荐的都是没槽位的废句。
func TestPickPrototypesPrefersUsableSlot(t *testing.T) {
	ss := []BankSentence{
		{ID: "A", Lesson: "第4课", Source: "基本课文", Answer: "だれも いません。"},
		{ID: "B", Lesson: "第4课", Source: "基本课文", Answer: "売店は 駅の 外に あります。"},
		{ID: "C", Lesson: "第4课", Source: "基本课文", Answer: "小野さんの 家は どこに ありますか。"},
		{ID: "D", Lesson: "第4课", Source: "基本课文", Answer: "横浜に あります。"},
	}
	by := groupProtosByLesson(ss)
	got := pickPrototypes(by, 4, 3, "名词")
	if len(got) != 3 {
		t.Fatalf("要 3 条，给了 %d 条", len(got))
	}
	ids := []string{got[0].ID, got[1].ID, got[2].ID}
	for _, id := range ids {
		if id == "A" {
			t.Errorf("没名词槽的 A（だれも いません。）不该进前三：%v", ids)
		}
	}
}

func TestPickPrototypesZeroOrNegative(t *testing.T) {
	by := groupProtosByLesson([]BankSentence{{ID: "P1", Lesson: "第1课", Answer: "李さんは 中国人です。"}})
	if got := pickPrototypes(by, 1, 0, "名词"); got != nil {
		t.Errorf("n=0 应返回 nil，实际 %v", got)
	}
}
