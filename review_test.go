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

	// 覆盖旧的 + 追加新的，且保持旧的顺序
	got := mergeReview([]ReviewItem{
		{Number: 2, Word: "b", Correct: true, Manual: true},
		{Number: 9, Word: "i", Correct: false},
		{Number: 1, Word: "a", Correct: false},
	}, old)

	want := []ReviewItem{
		{Number: 1, Word: "a", Correct: false},
		{Number: 2, Word: "b", Correct: true, Manual: true},
		{Number: 3, Word: "c", Correct: true},
		{Number: 9, Word: "i", Correct: false},
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
