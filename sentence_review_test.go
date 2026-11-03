package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

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
	// 同课内：基本课文在前，长的在前（2026-10-20 起长句优先 —— 骨架有内容，
	// 换进去的那个词才练得到东西；变形句原来偏简单就是短句优先排出来的）
	if by[3][0].ID != "B2" || by[3][1].ID != "B1" || by[3][2].ID != "A1" {
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
	// 距离 7 相等时先加后减：第8课 在 第6课 前面
	// （2026-10-20 改：跨课补句先往高课扩，别把变形句越造越简单）
	if got[1].ID != "P3" || got[2].ID != "P2" {
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

// ---------- 圆级优先（2026-11-03）----------

// reviewBankWithCircle 造一个「n3 条三圆 + n2 条二圆」的回炉池子。
// 每条用独立原型句，免得「同骨架当天只留一条」把分层结果搅乱。
func reviewBankWithCircle(n3, n2 int) (*ReviewBank, map[string]string) {
	b := &ReviewBank{}
	circle := map[string]string{}
	mk := func(tag, prefix string, i int) {
		w := fmt.Sprintf("%s_%02d", prefix, i)
		circle[w] = tag
		b.Sentences = append(b.Sentences, ReviewSentence{
			ID:        "R" + w,
			Word:      w,
			Answer:    tag + "の文" + w + "。",
			Prototype: "原型" + w,
		})
	}
	for i := 0; i < n3; i++ {
		mk("三圆", "c3", i)
	}
	for i := 0; i < n2; i++ {
		mk("二圆", "c2", i)
	}
	return b, circle
}

func countCircle(got []BankSentence) (three, other int) {
	for _, g := range got {
		if strings.Contains(g.Answer, "三圆の文") {
			three++
		} else {
			other++
		}
	}
	return
}

// 三圆占大头：20 句里 14 句三圆（70%），剩下 6 句给二圆 —— 不是独占。
func TestReviewSentencesCircleTakesMajority(t *testing.T) {
	// 三圆池子要够 14 条 —— 不够的话其他档会补上来（那是另一个用例）
	bank, circle := reviewBankWithCircle(16, 12)
	got := BuildReviewSentences(bank, SentenceHistory{}, nil, time.Now(), 20, nil, 0, circle, 70)
	if len(got) != 20 {
		t.Fatalf("要 20 句，给了 %d 句", len(got))
	}
	three, other := countCircle(got)
	if three != 14 || other != 6 {
		t.Errorf("70%% 配额应是 14 三圆 + 6 其他，实际 %d + %d", three, other)
	}
}

// 三圆池子不够时由其他档补满 —— 配额是上限不是保底，题量绝不缩水。
func TestReviewSentencesCircleShortfallFillsUp(t *testing.T) {
	bank, circle := reviewBankWithCircle(3, 20)
	got := BuildReviewSentences(bank, SentenceHistory{}, nil, time.Now(), 20, nil, 0, circle, 70)
	if len(got) != 20 {
		t.Fatalf("三圆不够也得给满 20 句，实际 %d 句", len(got))
	}
	three, other := countCircle(got)
	if three != 3 || other != 17 {
		t.Errorf("三圆只有 3 条时应是 3 + 17，实际 %d + %d", three, other)
	}
}

// ratio=0（有意关掉）和 circle=nil（读不到词表）都退回「不分层」，
// 结果必须跟加这个功能之前一致 —— 这是回滚路径的护栏。
func TestReviewSentencesCircleDisabledKeepsOldBehaviour(t *testing.T) {
	bank, circle := reviewBankWithCircle(12, 12)
	base := func(ratio int, c map[string]string) []BankSentence {
		return BuildReviewSentences(bank, SentenceHistory{}, nil, time.Now(), 20, nil, 0, c, ratio)
	}
	off := base(0, circle)
	noMeta := base(70, nil)
	if len(off) != 20 || len(noMeta) != 20 {
		t.Fatalf("关掉分层也得给满 20 句：off=%d nil=%d", len(off), len(noMeta))
	}
	for i := range off {
		if off[i].ID != noMeta[i].ID {
			t.Fatalf("ratio=0 与 circle=nil 结果不一致：第 %d 句 %s vs %s", i, off[i].ID, noMeta[i].ID)
		}
	}
	// 不分层时按 ID 排，二圆（Rc2_xx）字典序在前 —— 不再出现 14:6 的分层结果
	three, other := countCircle(off)
	if three == 14 {
		t.Errorf("关掉分层后不该还是 14 句三圆")
	}
	_ = other
}

// 同骨架去重跨层也要生效：分层不能变成「同一原型句一天出两遍」的漏洞。
func TestReviewSentencesCircleKeepsPrototypeDedupe(t *testing.T) {
	b := &ReviewBank{Sentences: []ReviewSentence{
		{ID: "R1", Word: "w3_a", Answer: "三圆の文1。", Prototype: "同じ骨格"},
		{ID: "R2", Word: "w3_b", Answer: "三圆の文2。", Prototype: "同じ骨格"},
		{ID: "R3", Word: "w2_a", Answer: "二圆の文1。", Prototype: "別の骨格"},
	}}
	circle := map[string]string{"w3_a": "三圆", "w3_b": "三圆", "w2_a": "二圆"}
	got := BuildReviewSentences(b, SentenceHistory{}, nil, time.Now(), 3, nil, 0, circle, 70)
	if len(got) != 2 {
		t.Fatalf("同骨架的两条只能留一条，应出 2 句，实际 %d 句", len(got))
	}
	seen := map[string]bool{}
	for _, g := range got {
		if g.Prototype == "同じ骨格" && seen["同じ骨格"] {
			t.Errorf("同一骨架出了两遍：%+v", got)
		}
		seen[g.Prototype] = true
	}
}

func TestCircleMapAndRank(t *testing.T) {
	meta := &WordMeta{Items: map[string]WordPOS{
		"みず(水)":   {Pos: "名词", Circle: "三圆"},
		"ほん(本)":   {Pos: "名词", Circle: "二圆"},
		"つくえ(机)":  {Pos: "名词", Circle: "一圆"},
		"いす(椅子)":  {Pos: "名词"},
		"でんわ(電話)": {Pos: "名词", Circle: "3圆"}, // 手写分级写歪了 → 落「没标」
	}}
	m := circleMap(meta)
	if got := circleRank(m, "みず(水)"); got != 0 {
		t.Errorf("三圆 rank=%d，要 0", got)
	}
	if got := circleRank(m, "ほん(本)"); got != 1 {
		t.Errorf("二圆 rank=%d，要 1", got)
	}
	if got := circleRank(m, "つくえ(机)"); got != 2 {
		t.Errorf("一圆 rank=%d，要 2", got)
	}
	if got := circleRank(m, "いす(椅子)"); got != 3 {
		t.Errorf("没标 rank=%d，要 3", got)
	}
	if got := circleRank(m, "でんわ(電話)"); got != 3 {
		t.Errorf("写歪的分级（3圆）rank=%d，要 3（不参与优先）", got)
	}
	if circleMap(nil) != nil {
		t.Error("词表为 nil 时 circleMap 应返回 nil")
	}
	if circleMap(&WordMeta{}) != nil {
		t.Error("空词表 circleMap 应返回 nil")
	}
}

func TestPlanCircleRatioDefaults(t *testing.T) {
	if got := planCircleRatio(PlanConfig{}); got != 70 {
		t.Errorf("没配时该用内置 70，实际 %d", got)
	}
	v := 0
	if got := planCircleRatio(PlanConfig{SentenceReviewCircleRatio: &v}); got != 0 {
		t.Errorf("显式 0 = 关掉，实际 %d", got)
	}
	bad := 150
	if got := planCircleRatio(PlanConfig{SentenceReviewCircleRatio: &bad}); got != 70 {
		t.Errorf("越界值该回退 70，实际 %d", got)
	}
	if !validCircleRatio(PlanConfig{}) || !validCircleRatio(PlanConfig{SentenceReviewCircleRatio: &v}) {
		t.Error("nil / 0 都该判合法")
	}
	if validCircleRatio(PlanConfig{SentenceReviewCircleRatio: &bad}) {
		t.Error("150 该判越界")
	}
}
