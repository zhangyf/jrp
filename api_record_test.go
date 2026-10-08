package main

import "testing"

// 造句部分提交：没交的句子必须留在 plan 里（刷新后还在），
// 已交的摘掉；全部交完时 plan 清空（上层据此删 plan 换新的一批）。
// 2026-10-08 之前是只要回写就整批删 plan，部分提交后刷新，没写的句子就没了。
func TestPruneSentencePlanPartial(t *testing.T) {
	plan := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
		{Number: 3, Answer: "c"},
		{Number: 4, Answer: "d"},
	}}
	left := pruneSentencePlan(plan, []SentenceResult{
		{Number: 1, Correct: true},
		{Number: 3, Correct: false},
	})
	if left != 2 {
		t.Fatalf("left = %d, want 2", left)
	}
	if plan.Sentences[0].Number != 2 || plan.Sentences[1].Number != 4 {
		t.Fatalf("剩下的应是 #2 #4（保持原编号）: %+v", plan.Sentences)
	}
}

// 全部交完 → plan 清空，上层删 plan。
func TestPruneSentencePlanAll(t *testing.T) {
	plan := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
	}}
	left := pruneSentencePlan(plan, []SentenceResult{
		{Number: 1, Correct: true},
		{Number: 2, Correct: false},
	})
	if left != 0 || len(plan.Sentences) != 0 {
		t.Fatalf("全交完应清空: left=%d plan=%+v", left, plan.Sentences)
	}
}

// 分两次提交同一批：第二次提交时 plan 已是第一次裁剪过的，
// 号码不重叠的提交不该误删剩下的句子。
func TestPruneSentencePlanTwice(t *testing.T) {
	plan := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
		{Number: 3, Answer: "c"},
	}}
	if l := pruneSentencePlan(plan, []SentenceResult{{Number: 1, Correct: true}}); l != 2 {
		t.Fatalf("第一次后应剩 2: %d", l)
	}
	if l := pruneSentencePlan(plan, []SentenceResult{{Number: 2, Correct: true}}); l != 1 {
		t.Fatalf("第二次后应剩 1: %d", l)
	}
	if plan.Sentences[0].Number != 3 {
		t.Fatalf("最后应只剩 #3: %+v", plan.Sentences)
	}
}
