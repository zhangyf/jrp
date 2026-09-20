package main

import (
	"strings"
	"testing"
)

func TestDraftKey(t *testing.T) {
	s := &Storage{lang: "ja"}
	got := s.draftKey("2026-09-20", "words")
	if !strings.HasPrefix(got, "language-review/ja/drafts/") {
		t.Fatalf("草稿键应落在 drafts/ 前缀下，实际 %q", got)
	}
	if !strings.HasSuffix(got, "draft_2026-09-20_words.json") {
		t.Fatalf("草稿键应带日期和模式，实际 %q", got)
	}

	// 同一天的今日练习和钉子户必须各占一个键，否则会互相覆盖
	if s.draftKey("2026-09-20", "words") == s.draftKey("2026-09-20", "hard") {
		t.Fatal("words 与 hard 的草稿键撞了")
	}
	// 隔天的草稿绝不能串到今天
	if s.draftKey("2026-09-20", "words") == s.draftKey("2026-09-21", "words") {
		t.Fatal("相邻两天的草稿键撞了")
	}
}

func TestDraftHasContent(t *testing.T) {
	cases := []struct {
		name string
		d    *Draft
		want bool
	}{
		{"nil 草稿", nil, false},
		{"没有条目", &Draft{}, false},
		{"全空答案", &Draft{Items: []DraftItem{{Number: 1}, {Number: 2}}}, false},
		{"写了答案", &Draft{Items: []DraftItem{{Number: 1, Answer: "きもち"}}}, true},
		{"只点了不会", &Draft{Items: []DraftItem{{Number: 1, Unknown: true}}}, true},
		{"只勾了算对", &Draft{Items: []DraftItem{{Number: 1, Manual: true}}}, true},
	}
	for _, c := range cases {
		if got := c.d.HasContent(); got != c.want {
			t.Errorf("%s: HasContent() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidDraftMode(t *testing.T) {
	ok := []string{"words", "hard", "sentences", "offline", "WORDS", "Hard"}
	for _, m := range ok {
		if !validDraftMode(m) {
			t.Errorf("合法模式 %q 被拒了", m)
		}
	}
	bad := []string{"", "whatever", "../etc", "words/../x", "单词"}
	for _, m := range bad {
		if validDraftMode(m) {
			t.Errorf("非法模式 %q 被放行了，它会污染 COS 键", m)
		}
	}
}
