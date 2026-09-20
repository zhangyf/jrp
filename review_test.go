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
