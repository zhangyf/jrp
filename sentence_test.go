// sentence_test.go —— 句库 / 回炉变形 / 课文导入 / circle 圈组规则相关测试（配 cmd_sentence.go、cmd_lesson.go、sentence*.go）
//
// 腿一收尾：把原本平铺在根目录的 4 个 *_test.go 按被测域物理合并到本文件。
// 测试函数体逐字保留，仅对 import 取并集去重；被测行为、断言、测试名均不变。
// 每个分区以「原文件名」标注，便于定位；文件头来源清单列出各源顶层声明。
//
// 来源与顶层声明：
// sentence_review_test.go: TestGroupLesson, TestRoundAcc, TestGroupProtosByLesson, TestPickPrototypesSameLessonFirst, TestPickPrototypesFallsBackToNearbyLesson, TestPickPrototypesUnknownLesson, TestProtoScoreNounNeedsSlot, TestProtoScoreVerbPrefersMasu, TestProtoScoreAdjPrefersKeiyoushi, TestPickPrototypesPrefersUsableSlot, ... (共 19 个顶层声明)
// sentence_test.go: TestSentencePlanLocked
// circle_test.go: TestCircleGroupOf, TestLessonOfGroup, TestHoldSet, TestBuildDuePlanSkipsHold, TestArchiveAppendAndSetDefinition
// lesson_import_test.go: TestResolveAndCollectImport
package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/lessonimport"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
	"github.com/zhangyf/jrp/internal/wordmeta"
)

// ==========================================================================
// 原 sentence_review_test.go
// ==========================================================================

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
		if got := sentencereview.GroupLesson(c.group); got != c.want {
			t.Errorf("groupLesson(%q) = %d, want %d", c.group, got, c.want)
		}
	}
}

func TestRoundAcc(t *testing.T) {
	if got := sentencereview.RoundAcc(0.9166666666666666); got != 0.92 {
		t.Errorf("roundAcc = %v, want 0.92", got)
	}
	if got := sentencereview.RoundAcc(1); got != 1 {
		t.Errorf("roundAcc(1) = %v, want 1", got)
	}
}

func TestGroupProtosByLesson(t *testing.T) {
	ss := []sentence.BankSentence{
		{ID: "A1", Lesson: "第3课", Source: "应用课文", Answer: "とても 長い アプリの 文です。"},
		{ID: "B1", Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"},
		{ID: "B2", Lesson: "第3课", Source: "基本课文", Answer: "トイレは どこに ありますか。"},
		{ID: "C1", Lesson: "第5课", Source: "基本课文", Answer: "今 4時です。"},
		{ID: "D1", Lesson: "单元末", Source: "阅读文", Answer: "今日は 金曜日です。"},
	}
	by := sentencereview.GroupProtosByLesson(ss)
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
	ss := []sentence.BankSentence{
		{ID: "P1", Lesson: "第4课", Source: "基本课文", Answer: "部屋に 机と 椅子が あります。"},
		{ID: "P2", Lesson: "第4课", Source: "基本课文", Answer: "机の 上に 猫が います。"},
		{ID: "P3", Lesson: "第4课", Source: "基本课文", Answer: "売店は 駅の 外に あります。"},
		{ID: "P4", Lesson: "第4课", Source: "基本课文", Answer: "吉田さんは 庭に います。"},
		{ID: "Q1", Lesson: "第2课", Source: "基本课文", Answer: "これは 本です。"},
	}
	by := sentencereview.GroupProtosByLesson(ss)
	got := sentencereview.PickPrototypes(by, 4, 3, "名词")
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
	ss := []sentence.BankSentence{
		{ID: "P1", Lesson: "第7课", Source: "基本课文", Answer: "李さんは 毎日 コーヒーを 飲みます。"},
		{ID: "P2", Lesson: "第6课", Source: "基本课文", Answer: "李さんは 先月 北京から 来ました。"},
		{ID: "P3", Lesson: "第8课", Source: "基本课文", Answer: "李さんは 日本語で 手紙を 書きます。"},
	}
	by := sentencereview.GroupProtosByLesson(ss)
	got := sentencereview.PickPrototypes(by, 7, 3, "名词")
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
	ss := []sentence.BankSentence{
		{ID: "P1", Lesson: "第2课", Source: "基本课文", Answer: "これは 本です。"},
		{ID: "P2", Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"},
	}
	by := sentencereview.GroupProtosByLesson(ss)
	got := sentencereview.PickPrototypes(by, 0, 2, "名词")
	if len(got) != 2 {
		t.Fatalf("课号未知时要能兜底给满，实际 %d 条", len(got))
	}
}

func TestProtoScoreNounNeedsSlot(t *testing.T) {
	// 名词要找有名词槽的句子：「はい、そうです。」这种没槽位的必须被压下去
	withSlot := sentence.BankSentence{Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"}
	noSlot := sentence.BankSentence{Lesson: "第3课", Source: "基本课文", Answer: "はい、そうです。"}
	if sentencereview.ProtoScore(withSlot, "名词") <= sentencereview.ProtoScore(noSlot, "名词") {
		t.Errorf("有名词槽的句子该排前面：有槽=%d 无槽=%d",
			sentencereview.ProtoScore(withSlot, "名词"), sentencereview.ProtoScore(noSlot, "名词"))
	}
}

func TestProtoScoreVerbPrefersMasu(t *testing.T) {
	masu := sentence.BankSentence{Lesson: "第7课", Source: "基本课文", Answer: "李さんは 毎日 コーヒーを 飲みます。"}
	desu := sentence.BankSentence{Lesson: "第7课", Source: "基本课文", Answer: "これは 本です。"}
	if sentencereview.ProtoScore(masu, "动词") <= sentencereview.ProtoScore(desu, "动词") {
		t.Errorf("动词该优先ます结尾的：ます=%d です=%d",
			sentencereview.ProtoScore(masu, "动词"), sentencereview.ProtoScore(desu, "动词"))
	}
}

func TestProtoScoreAdjPrefersKeiyoushi(t *testing.T) {
	i := sentence.BankSentence{Lesson: "第9课", Source: "基本课文", Answer: "四川料理は 辛いです。"}
	n := sentence.BankSentence{Lesson: "第9课", Source: "基本课文", Answer: "これは 本です。"}
	if sentencereview.ProtoScore(i, "形容词") <= sentencereview.ProtoScore(n, "形容词") {
		t.Errorf("形容词该优先い形句：い=%d 普通=%d",
			sentencereview.ProtoScore(i, "形容词"), sentencereview.ProtoScore(n, "形容词"))
	}
}

// 真实场景回归：给地名「なごや」推荐时，第4课里带地点槽的「売店は 駅の 外に あります。」
// 必须排在「だれも いません。」前面 —— 早期版本只按短句优先，推荐的都是没槽位的废句。
func TestPickPrototypesPrefersUsableSlot(t *testing.T) {
	ss := []sentence.BankSentence{
		{ID: "A", Lesson: "第4课", Source: "基本课文", Answer: "だれも いません。"},
		{ID: "B", Lesson: "第4课", Source: "基本课文", Answer: "売店は 駅の 外に あります。"},
		{ID: "C", Lesson: "第4课", Source: "基本课文", Answer: "小野さんの 家は どこに ありますか。"},
		{ID: "D", Lesson: "第4课", Source: "基本课文", Answer: "横浜に あります。"},
	}
	by := sentencereview.GroupProtosByLesson(ss)
	got := sentencereview.PickPrototypes(by, 4, 3, "名词")
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
	by := sentencereview.GroupProtosByLesson([]sentence.BankSentence{{ID: "P1", Lesson: "第1课", Answer: "李さんは 中国人です。"}})
	if got := sentencereview.PickPrototypes(by, 1, 0, "名词"); got != nil {
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

func countCircle(got []sentence.BankSentence) (three, other int) {
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
	got := sentencereview.BuildReviewSentences(bank, sentence.SentenceHistory{}, nil, time.Now(), 20, nil, 0, circle, 70)
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
	got := sentencereview.BuildReviewSentences(bank, sentence.SentenceHistory{}, nil, time.Now(), 20, nil, 0, circle, 70)
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
	base := func(ratio int, c map[string]string) []sentence.BankSentence {
		return sentencereview.BuildReviewSentences(bank, sentence.SentenceHistory{}, nil, time.Now(), 20, nil, 0, c, ratio)
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
	got := sentencereview.BuildReviewSentences(b, sentence.SentenceHistory{}, nil, time.Now(), 3, nil, 0, circle, 70)
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
	m := wordmeta.CircleMap(meta)
	if got := sentencereview.CircleRank(m, "みず(水)"); got != 0 {
		t.Errorf("三圆 rank=%d，要 0", got)
	}
	if got := sentencereview.CircleRank(m, "ほん(本)"); got != 1 {
		t.Errorf("二圆 rank=%d，要 1", got)
	}
	if got := sentencereview.CircleRank(m, "つくえ(机)"); got != 2 {
		t.Errorf("一圆 rank=%d，要 2", got)
	}
	if got := sentencereview.CircleRank(m, "いす(椅子)"); got != 3 {
		t.Errorf("没标 rank=%d，要 3", got)
	}
	if got := sentencereview.CircleRank(m, "でんわ(電話)"); got != 3 {
		t.Errorf("写歪的分级（3圆）rank=%d，要 3（不参与优先）", got)
	}
	if wordmeta.CircleMap(nil) != nil {
		t.Error("词表为 nil 时 circleMap 应返回 nil")
	}
	if wordmeta.CircleMap(&WordMeta{}) != nil {
		t.Error("空词表 circleMap 应返回 nil")
	}
}

func TestPlanCircleRatioDefaults(t *testing.T) {
	if got := planconfig.PlanCircleRatio(PlanConfig{}); got != 70 {
		t.Errorf("没配时该用内置 70，实际 %d", got)
	}
	v := 0
	if got := planconfig.PlanCircleRatio(PlanConfig{SentenceReviewCircleRatio: &v}); got != 0 {
		t.Errorf("显式 0 = 关掉，实际 %d", got)
	}
	bad := 150
	if got := planconfig.PlanCircleRatio(PlanConfig{SentenceReviewCircleRatio: &bad}); got != 70 {
		t.Errorf("越界值该回退 70，实际 %d", got)
	}
	if !planconfig.ValidCircleRatio(PlanConfig{}) || !planconfig.ValidCircleRatio(PlanConfig{SentenceReviewCircleRatio: &v}) {
		t.Error("nil / 0 都该判合法")
	}
	if planconfig.ValidCircleRatio(PlanConfig{SentenceReviewCircleRatio: &bad}) {
		t.Error("150 该判越界")
	}
}

// ==========================================================================
// 原 sentence_test.go
// ==========================================================================

// 造句「不提交就不换题」的判定。换题只能发生在回写成功之后
// （api_record.go 里 DeleteSentencePlan 清掉当天 plan），在此之前
// 无论刷新多少次、换什么设备，都必须复用同一批。
func TestSentencePlanLocked(t *testing.T) {
	today := "2026-09-21"
	withSentences := &ReviewPlan{Date: today, Kind: "sentences",
		Sentences: []PlanSentence{{Number: 1, Answer: "あ"}}}

	cases := []struct {
		name string
		old  *ReviewPlan
		date string
		want bool
	}{
		{"当天已有 plan → 复用（不换题）", withSentences, today, true},
		{"对象不存在（下载失败）→ 重新出题", nil, today, false},
		{"空 plan → 重新出题", &ReviewPlan{Date: today, Kind: "sentences"}, today, false},
		{"日期对不上（陈年 plan）→ 重新出题",
			&ReviewPlan{Date: "2026-09-01", Kind: "sentences",
				Sentences: []PlanSentence{{Number: 1, Answer: "あ"}}}, today, false},
	}
	for _, c := range cases {
		if got := plan.SentencePlanLocked(c.old, c.date); got != c.want {
			t.Errorf("%s: plan.SentencePlanLocked() = %v, want %v", c.name, got, c.want)
		}
	}
}

// ==========================================================================
// 原 circle_test.go
// ==========================================================================

func TestCircleGroupOf(t *testing.T) {
	cases := []struct {
		in   CircleItem
		want string
	}{
		{CircleItem{Lesson: 21}, "第21课 手册补录"},
		{CircleItem{Lesson: 8}, "第8课 手册补录"},
		{CircleItem{Lesson: 0}, "未分组"},
		{CircleItem{Lesson: 5, Group: "自定义组"}, "自定义组"},
	}
	for _, c := range cases {
		if got := wordmeta.CircleGroupOf(c.in); got != c.want {
			t.Errorf("circleGroupOf(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLessonOfGroup(t *testing.T) {
	cases := []struct {
		title string
		want  int
	}{
		{"第21课 手册补录", 21},
		{"📖 第1课01 基础词（5/25）", 1},
		{"第19课 生词表（10/13）", 19},
		{"追加词汇（8/7）", 0},
		{"天气/季节词（7/13）", 0},
		{"第课", 0},
	}
	for _, c := range cases {
		if got := wordmeta.LessonOfGroup(c.title); got != c.want {
			t.Errorf("lessonOfGroup(%q) = %d, want %d", c.title, got, c.want)
		}
	}
}

func TestHoldSet(t *testing.T) {
	// meta 为 nil → 一个都不排除（fail-open：读不到配置不能把计划清空）
	if got := wordmeta.HoldSet(nil); got != nil {
		t.Errorf("holdSet(nil) = %v, want nil", got)
	}
	// 没有 hold → 也是 nil
	m := &WordMeta{Items: map[string]WordPOS{"あ": {Pos: "名词"}}}
	if got := wordmeta.HoldSet(m); got != nil {
		t.Errorf("holdSet(no hold) = %v, want nil", got)
	}
	// 有 hold → 只收 hold 的
	m.Items["ほーるど"] = WordPOS{Hold: true}
	got := wordmeta.HoldSet(m)
	if !got["ほーるど"] {
		t.Error("holdSet 应该包含 hold 的词")
	}
	if got["あ"] {
		t.Error("holdSet 不该包含没挂起的词")
	}
}

// BuildDuePlan 必须跳过挂起的词 —— 这是「补录的 21–24 课不涌进每日练习」的守门测试。
func TestBuildDuePlanSkipsHold(t *testing.T) {
	arc := &Archive{
		Language: "ja",
		Groups: []WordGroup{
			{Title: "第1课 生词表", Words: []Word{
				{Word: "みず(水)", Definition: "水", Group: "第1课 生词表"},
			}},
			{Title: "第21课 手册补录", Words: []Word{
				{Word: "わたす(渡す)", Definition: "交给", Group: "第21课 手册补录"},
			}},
		},
	}
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)

	// 不传 hold：两个词都在（ReviewCount=0 → 待测试 → IsDue）
	p := plan.BuildDuePlan(arc, "ja", today, nil)
	if len(p.Words) != 2 {
		t.Fatalf("不带 hold 时应出 2 词，实际 %d", len(p.Words))
	}

	// 传 hold：只剩没挂起的那个
	p = plan.BuildDuePlan(arc, "ja", today, map[string]bool{"わたす(渡す)": true})
	if len(p.Words) != 1 {
		t.Fatalf("带 hold 时应出 1 词，实际 %d", len(p.Words))
	}
	if p.Words[0].Word != "みず(水)" {
		t.Errorf("不该把挂起的词出出来，实际 %q", p.Words[0].Word)
	}
}

func TestArchiveAppendAndSetDefinition(t *testing.T) {
	arc := &Archive{Language: "ja"}

	if created := arc.AppendWord("第9课 手册补录", Word{Word: "え(絵)", Definition: "画儿"}); !created {
		t.Error("第一次追加应该新建组")
	}
	if created := arc.AppendWord("第9课 手册补录", Word{Word: "せ(背)", Definition: "个子"}); created {
		t.Error("第二次追加不该新建组")
	}
	if got := len(arc.Groups); got != 1 {
		t.Fatalf("分组数 = %d, want 1", got)
	}
	if got := arc.Groups[0].Words[1].Group; got != "第9课 手册补录" {
		t.Errorf("词的 Group 没回填：%q", got)
	}
	// 空分组名兜底
	arc.AppendWord("   ", Word{Word: "x", Definition: "y"})
	if arc.Groups[len(arc.Groups)-1].Title != "未分组" {
		t.Errorf("空分组名应落「未分组」，实际 %q", arc.Groups[len(arc.Groups)-1].Title)
	}

	if !arc.SetDefinition("え(絵)", "画儿（绘画作品）") {
		t.Error("SetDefinition 应该找到词")
	}
	if arc.Groups[0].Words[0].Definition != "画儿（绘画作品）" {
		t.Errorf("释义没改：%q", arc.Groups[0].Words[0].Definition)
	}
	if arc.SetDefinition("そんな言葉はない", "x") {
		t.Error("不存在的词不该改成功")
	}
}

// ==========================================================================
// 原 lesson_import_test.go
// ==========================================================================

// save-lesson 自动入库走的就是这两个函数（resolveImportDocs + collectImport），
// 这里用内存文档跑一遍：查重只对"一模一样"生效，重复导不涨条目。
func TestResolveAndCollectImport(t *testing.T) {
	basic := "## 第19课 基本课文\n\n### 基本课文 4 句\n\n| 序号 | 日文 | 中文 |\n|---|---|---|\n" +
		"| 1 | 部屋の かぎを 忘れないで ください。 | 请别忘了房间钥匙。 |\n" +
		"| 2 | あの 人が 吉田課長ですよ。 | 那个人是吉田科长。 |\n"
	applied := "## 第19课 应用课文\n\n| 说话人 | 日文 | 中文 |\n|---|---|---|\n" +
		"| 李 | 小野さん、初めてですから、ちょっと 恐いです。 | 小野女士，我是第一次滑雪，有点害怕。 |\n"
	docs := map[string]string{
		lessonimport.LessonDocName(5, lessonimport.LessonDocKinds[0]): basic,
		lessonimport.LessonDocName(5, lessonimport.LessonDocKinds[1]): applied,
	}
	fetch := func(n string) (string, bool) { c, ok := docs[n]; return c, ok }

	existing := []sentence.BankSentence{
		{ID: "L19B001", Lesson: "第19课", Source: "基本课文", Type: "original", Answer: "部屋の かぎを 忘れないで ください。"},
	}
	unit, list, found := resolveImportDocs(fetch, 19, "")
	if !found || unit != 5 || len(list) != 2 {
		t.Fatalf("应定位到两份文档，实际 unit=%d found=%v docs=%d", unit, found, len(list))
	}

	all, added, reports := collectImport(fetch, existing, 19, list)
	if len(added) != 2 {
		t.Fatalf("3 句里 1 句重复 → 应只加 2 条，实际 %d（%+v）", len(added), reports)
	}
	if len(all) != 3 {
		t.Errorf("句库应从 1 条变 3 条，实际 %d", len(all))
	}

	again, added2, _ := collectImport(fetch, all, 19, list)
	if len(added2) != 0 || len(again) != 3 {
		t.Errorf("再导一次应 0 新增、条数不变，实际 new=%d total=%d", len(added2), len(again))
	}
}
