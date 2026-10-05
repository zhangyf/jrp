package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestReviewKey(t *testing.T) {
	s := &Storage{lang: "ja"}
	got := s.reviewKey("2026-09-20", "words")
	if !strings.HasPrefix(got, "language-review/ja/reviews/") {
		t.Fatalf("回看快照键应落在 reviews/ 前缀下，实际 %q", got)
	}
	if !strings.HasSuffix(got, "review_2026-09-20_words.json") {
		t.Fatalf("回看快照键应带日期和模式，实际 %q", got)
	}
	// 今日练习和钉子户各占一个键
	if s.reviewKey("2026-09-20", "words") == s.reviewKey("2026-09-20", "hard") {
		t.Fatal("words 与 hard 的回看键撞了")
	}
	// 隔天的不能串到今天
	if s.reviewKey("2026-09-20", "words") == s.reviewKey("2026-09-21", "words") {
		t.Fatal("相邻两天的回看键撞了")
	}
}

func TestValidReviewMode(t *testing.T) {
	for _, m := range []string{"words", "hard", "WORDS"} { // 大小写宽容，调用方已 ToLower
		if !validReviewMode(m) {
			t.Errorf("validReviewMode(%q) = false, want true", m)
		}
	}
	// 模式直接进 COS 键，任意字符串会把键打散
	for _, m := range []string{"", "../../etc", "sentences", "offline"} {
		if validReviewMode(m) {
			t.Errorf("validReviewMode(%q) = true, want false", m)
		}
	}
}

func TestMergeReview(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "a", Correct: true},
		{Number: 2, Word: "b", Correct: false},
		{Number: 3, Word: "c", Correct: true},
	}

	// 覆盖旧的 + 追加新的，且保持旧的顺序（号码合并后重排成 1..N 连续）
	got := mergeReview([]ReviewItem{
		{Number: 2, Word: "b", Correct: true, Manual: true},
		{Number: 9, Word: "i", Correct: false},
		{Number: 1, Word: "a", Correct: false},
	}, old)

	want := []ReviewItem{
		{Number: 1, Word: "a", Correct: false},
		{Number: 2, Word: "b", Correct: true, Manual: true},
		{Number: 3, Word: "c", Correct: true},
		{Number: 4, Word: "i", Correct: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeReview =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMergeReviewEmptyOld(t *testing.T) {
	in := []ReviewItem{{Number: 5, Word: "e"}}
	got := mergeReview(in, nil)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("mergeReview with no old = %+v, want %+v", got, in)
	}
}

// 同一批词分两次回写（先写一半回写，剩下的写完再回写）不能把前一半冲掉。
func TestMergeReviewPartialCommit(t *testing.T) {
	first := []ReviewItem{
		{Number: 1, Word: "a", Correct: true},
		{Number: 2, Word: "b", Correct: false},
	}
	merged := mergeReview(first, nil)

	second := []ReviewItem{
		{Number: 3, Word: "c", Correct: true},
		{Number: 4, Word: "d", Answer: "で", Correct: false},
	}
	merged = mergeReview(second, merged)

	if len(merged) != 4 {
		t.Fatalf("merged len = %d, want 4", len(merged))
	}
	if merged[0].Number != 1 || merged[3].Number != 4 {
		t.Errorf("order wrong: %+v", merged)
	}
}

// 老师中途刷新页面（客户端状态清零）后整批重发，早前批次已答的词在
// 新批次里全成了 blank —— blank 不能把已答条目（尤其错词）覆盖掉。
// 2026-10-05 事故：当天 10 个错词被抹得只剩 7 个。
func TestMergeReviewBlankDoesNotClobberAnswered(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "a", Correct: false, Answer: "あけみ", Status: "🔴待巩固"},
		{Number: 2, Word: "b", Correct: true},
		{Number: 3, Word: "c", Blank: true},
	}
	// 刷新后的整批重发：#1 #2 变 blank，#3 照旧 blank，#4 是新练的
	newer := []ReviewItem{
		{Number: 1, Word: "a", Blank: true},
		{Number: 2, Word: "b", Blank: true},
		{Number: 3, Word: "c", Blank: true},
		{Number: 4, Word: "d", Correct: false, Answer: "で"},
	}
	got := mergeReview(newer, old)

	if len(got) != 4 {
		t.Fatalf("merged len = %d, want 4", len(got))
	}
	if got[0].Number != 1 || got[0].Blank || got[0].Correct || got[0].Answer != "あけみ" {
		t.Errorf("#1 blank 不该覆盖已答错词: %+v", got[0])
	}
	if got[1].Number != 2 || got[1].Blank || !got[1].Correct {
		t.Errorf("#2 blank 不该覆盖已答对词: %+v", got[1])
	}
	if got[2].Number != 3 || !got[2].Blank {
		t.Errorf("#3 双方都是 blank，取新: %+v", got[2])
	}
	if got[3].Number != 4 || got[3].Blank {
		t.Errorf("#4 新练的照常追加: %+v", got[3])
	}
}

// 反向：刷新后老师把之前空着的词练了（非 blank）→ 正常覆盖。
func TestMergeReviewAnsweredClobbersBlank(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "a", Blank: true},
	}
	got := mergeReview([]ReviewItem{
		{Number: 1, Word: "a", Correct: false, Answer: "みせ"},
	}, old)
	if got[0].Blank || got[0].Correct || got[0].Answer != "みせ" {
		t.Errorf("非 blank 该覆盖 blank: %+v", got[0])
	}
}

// 一天里会出好几批 plan（回写成功 → plan 清 → 新一批），**每批题号都从 1 重新编**。
// 按号码合并的话，后一批的 #1 会盖掉前一批的 #1 —— 前几批练过的词全丢。
// 2026-10-05 事故：当天档案里 83 个词的 LastReview 是当天，快照只剩 44 个词，
// 老师看到的「共 75 / 对 62 / 错 9」全是虚的。
func TestMergeReviewNewBatchKeepsEarlierWords(t *testing.T) {
	// 第一批：#1 #2
	old := []ReviewItem{
		{Number: 1, Word: "あ", Correct: true, Answer: "あ"},
		{Number: 2, Word: "い", Correct: false, Answer: "いけ"},
	}
	// 第二批（回写后新出的）：题号重新从 1 开始，且是另一批词
	got := mergeReview([]ReviewItem{
		{Number: 1, Word: "う", Correct: true, Answer: "う"},
		{Number: 2, Word: "え", Correct: true, Answer: "え"},
	}, old)

	if len(got) != 4 {
		t.Fatalf("新一批的词应全部保留（按词合并），len = %d, want 4: %+v", len(got), got)
	}
	want := []string{"あ", "い", "う", "え"}
	for i, w := range want {
		if got[i].Word != w {
			t.Errorf("got[%d].Word = %q, want %q", i, got[i].Word, w)
		}
	}
	// 第一批的错词不能因为号码撞车被冲掉
	if got[1].Correct || got[1].Answer != "いけ" {
		t.Errorf("第一批的错词被新一批盖掉了: %+v", got[1])
	}
	// 号码重排成 1..N 连续，否则前端按号码索引会错位
	for i, it := range got {
		if it.Number != i+1 {
			t.Errorf("got[%d].Number = %d, want %d（重排避免跨批次撞号）", i, it.Number, i+1)
		}
	}
}

// 同一天同一个词被练了两次（两批都抽到它）→ 只留一条，后一次覆盖前一次。
func TestMergeReviewSameWordSameDay(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "あ", Correct: false, Answer: "い"},
	}
	got := mergeReview([]ReviewItem{
		{Number: 7, Word: "あ", Correct: true, Answer: "あ"},
	}, old)
	if len(got) != 1 {
		t.Fatalf("同一天同一个词只留一条，len = %d, want 1: %+v", len(got), got)
	}
	if !got[0].Correct || got[0].Answer != "あ" {
		t.Errorf("后一次该覆盖前一次: %+v", got[0])
	}
}

// 历史遗留：号码撞车时同一个词被写了两遍（快照里出现两条同词不同号）。
// 合并后必须收敛成一条，否则「共 N / 对 X」会把同一个词数两次。
// 2026-10-05：同一事故里快照 101 条其实只有 68 个词。
func TestMergeReviewDropsDuplicateWordRows(t *testing.T) {
	old := []ReviewItem{
		{Number: 6, Word: "あ", Correct: false, Answer: "い"},
		{Number: 18, Word: "あ", Blank: true},
		{Number: 49, Word: "あ", Blank: true},
		{Number: 7, Word: "う", Correct: true},
	}
	got := mergeReview([]ReviewItem{
		{Number: 1, Word: "う", Correct: true},
	}, old)

	if len(got) != 2 {
		t.Fatalf("同一个词只应留一条，len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Word != "あ" || got[0].Blank {
		t.Errorf("重复条目应保留有信息量那条（错词，不是 blank）: %+v", got[0])
	}
	if got[0].Number != 1 || got[1].Number != 2 {
		t.Errorf("号码应重排成 1..N: %+v", got)
	}
}
