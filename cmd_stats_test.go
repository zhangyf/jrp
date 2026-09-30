package main

import "testing"

func TestMaxLessonPicksLargestLessonNumber(t *testing.T) {
	words := []Word{
		{Word: "a", Group: "第1课 生词表"},
		{Word: "b", Group: "第16课 生词表"},
		{Word: "c", Group: "📖 第9课08 城市/形容（5/30）"},
		{Word: "d", Group: "第2课 生词表"},
	}
	if got := maxLesson(words); got != 16 {
		t.Errorf("maxLesson = %d, want 16", got)
	}
}

func TestMaxLessonNoLessonReturnsZero(t *testing.T) {
	words := []Word{
		{Word: "a", Group: "基础词"},
		{Word: "b", Group: ""},
	}
	if got := maxLesson(words); got != 0 {
		t.Errorf("maxLesson = %d, want 0", got)
	}
}

// 熟练词只数「复习过且正确率 ≥80%」的，未复习过的一律不算。
func TestBuildJlptProgressCountsFirmWordsOnly(t *testing.T) {
	groups := []WordGroup{{Title: "第16课 生词表", Words: []Word{
		{Word: "w1", Group: "第16课 生词表", ReviewCount: 10, ErrorCount: 1},  // 90%
		{Word: "w2", Group: "第16课 生词表", ReviewCount: 10, ErrorCount: 2},  // 80%
		{Word: "w3", Group: "第16课 生词表", ReviewCount: 10, ErrorCount: 3},  // 70%
		{Word: "w4", Group: "第16课 生词表"},                                 // 没复习过
	}}}

	p := buildJlptProgress(groups)
	if p.VocabTotal != 4 {
		t.Errorf("VocabTotal = %d, want 4", p.VocabTotal)
	}
	if p.VocabFirm != 2 {
		t.Errorf("VocabFirm = %d, want 2", p.VocabFirm)
	}
	if p.Lesson != 16 {
		t.Errorf("Lesson = %d, want 16", p.Lesson)
	}
	if p.N5.Vocab != 800 || p.N5.Lesson != 22 {
		t.Errorf("N5 = %+v, want 800/22", p.N5)
	}
	if p.N4.Vocab != 1500 || p.N4.Lesson != 48 {
		t.Errorf("N4 = %+v, want 1500/48", p.N4)
	}
}
