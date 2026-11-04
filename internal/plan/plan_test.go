package plan

import (
	"math"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/review"
)

// 分区边界是硬口径：0.30 / 0.45 两侧不能漂。
func TestHardSeverityBuckets(t *testing.T) {
	cases := []struct {
		acc  float64
		want string
	}{
		{0.0, offline.HardSevere},
		{0.29, offline.HardSevere},
		{0.30, offline.HardModerate},
		{0.44, offline.HardModerate},
		{0.45, offline.HardMild},
		{0.9, offline.HardMild},
	}
	for _, c := range cases {
		if got := hardSeverity(c.acc); got != c.want {
			t.Errorf("hardSeverity(%v) = %q, want %q", c.acc, got, c.want)
		}
	}
}

func TestCountByStatus(t *testing.T) {
	got := CountByStatus([]offline.PlanWord{
		{Status: "待巩固"},
		{Status: ""},
		{Status: "待巩固"},
		{Status: "抽查"},
	})
	if len(got) != 2 || got["待巩固"] != 2 || got["抽查"] != 1 {
		t.Fatalf("CountByStatus = %+v", got)
	}
	if _, ok := got[""]; ok {
		t.Error("空状态不该计入")
	}
}

// BuildHardPlan 是全量普查（不做 IsDue 过滤），按正确率升序、编号连续，
// 且 Accuracy 必须用指针保住真正的 0%（omitempty 下别被吞掉）。
func TestBuildHardPlanOrderAndZeroAccuracy(t *testing.T) {
	arc := &archive.Archive{
		Language: "ja",
		Groups: []review.WordGroup{
			{Title: "第1课", Words: []review.Word{
				{Word: "こう(0.9)", Definition: "高", Group: "第1课", ReviewCount: 10, ErrorCount: 1},
				{Word: "ぜろ(0%)", Definition: "零", Group: "第1课", ReviewCount: 5, ErrorCount: 5},
				{Word: "てんいち(0.1)", Definition: "低", Group: "第1课", ReviewCount: 10, ErrorCount: 9},
				{Word: "てんよん(0.4)", Definition: "中", Group: "第1课", ReviewCount: 10, ErrorCount: 6},
			}},
		},
	}
	today := time.Date(2026, 11, 4, 0, 0, 0, 0, time.Local)

	p := BuildHardPlan(arc, "ja", today, 0.6, 1)
	if p.Kind != "hard" || p.MinAccuracy != 0.6 || p.MinReviews != 1 ||
		p.Date != "2026-11-04" || p.Language != "ja" {
		t.Fatalf("plan 头部字段不符: %+v", p)
	}
	if len(p.Words) != 3 {
		t.Fatalf("应只收 3 个钉子户（0.9 那个排除），实际 %d", len(p.Words))
	}

	wantAcc := []float64{0, 0.1, 0.4}
	wantStatus := []string{offline.HardSevere, offline.HardSevere, offline.HardModerate}
	for i, w := range p.Words {
		if w.Number != i+1 {
			t.Errorf("第 %d 个词编号 = %d, want %d", i, w.Number, i+1)
		}
		if w.Accuracy == nil {
			t.Fatalf("第 %d 个词 Accuracy 不该是 nil（0%% 也要保真）", i)
		}
		if math.Abs(*w.Accuracy-wantAcc[i]) > 1e-9 {
			t.Errorf("第 %d 个词正确率 = %v, want %v", i, *w.Accuracy, wantAcc[i])
		}
		if w.Status != wantStatus[i] {
			t.Errorf("第 %d 个词分区 = %q, want %q", i, w.Status, wantStatus[i])
		}
	}
	if p.Words[0].Word != "ぜろ(0%)" {
		t.Errorf("0%% 的词应排最前，实际 %q", p.Words[0].Word)
	}
}
