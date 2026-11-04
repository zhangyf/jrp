package main

import (
	"testing"

	"github.com/zhangyf/jrp/internal/plan"
)

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
