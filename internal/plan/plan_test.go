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

// ==========================================================================
// 当天分多次练习：plan 锁定 + 提交剪枝（2026-11-17）
// ==========================================================================

// 空 plan 必须也算锁定 —— 那是「今天已经出过题且全交完」的标记。
// 不锁的话 hard 会立刻重算同一批词，刷新一次就多回写一遍档案。
func TestWordPlanLocked(t *testing.T) {
	const date = "2026-11-17"
	cases := []struct {
		name string
		old  *offline.ReviewPlan
		date string
		want bool
	}{
		{"下载失败 / 对象不存在", nil, date, false},
		{"陈年 plan", &offline.ReviewPlan{Date: "2026-11-16", Words: []offline.PlanWord{{Number: 1}}}, date, false},
		{"当天有词", &offline.ReviewPlan{Date: date, Words: []offline.PlanWord{{Number: 1}}}, date, true},
		{"当天全交完的空 plan", &offline.ReviewPlan{Date: date}, date, true},
		{"请求日期对不上", &offline.ReviewPlan{Date: date}, "2026-11-18", false},
	}
	for _, c := range cases {
		if got := WordPlanLocked(c.old, c.date); got != c.want {
			t.Errorf("%s: WordPlanLocked = %v, want %v", c.name, got, c.want)
		}
	}
}

func testWordPlan() *offline.ReviewPlan {
	return &offline.ReviewPlan{
		Date: "2026-11-17",
		Words: []offline.PlanWord{
			{Number: 1, Word: "みず(水)"},
			{Number: 2, Word: "はし(橋)"},
			{Number: 3, Word: "なつ(夏)"},
			{Number: 4, Word: "ゆき(雪)"},
		},
	}
}

// 部分提交：剩下的词留在 plan 里，题号保持原编号。
func TestPruneWordPlanPartial(t *testing.T) {
	p := testWordPlan()
	left := PruneWordPlan(p, []int{1, 3})
	if left != 2 || len(p.Words) != 2 {
		t.Fatalf("应剩 #2 #4，实际 left=%d words=%+v", left, p.Words)
	}
	if p.Words[0].Number != 2 || p.Words[0].Word != "はし(橋)" ||
		p.Words[1].Number != 4 || p.Words[1].Word != "ゆき(雪)" {
		t.Fatalf("题号被重排或词对不上：%+v", p.Words)
	}
}

// 全交完 → 空 plan。上层保留这份空对象，不删。
func TestPruneWordPlanAll(t *testing.T) {
	p := testWordPlan()
	left := PruneWordPlan(p, []int{1, 2, 3, 4})
	if left != 0 || len(p.Words) != 0 {
		t.Fatalf("全交完应清空：left=%d words=%+v", left, p.Words)
	}
	if p.Date != "2026-11-17" {
		t.Errorf("剪枝不该动 Date：%q", p.Date)
	}
}

// 一天分三次提交：每次只剪本批，已剪掉的不受影响，剩下的题号仍然原样。
func TestPruneWordPlanTwice(t *testing.T) {
	p := testWordPlan()
	if l := PruneWordPlan(p, []int{2}); l != 3 {
		t.Fatalf("第一次后应剩 3，实际 %d", l)
	}
	if l := PruneWordPlan(p, []int{4}); l != 2 {
		t.Fatalf("第二次后应剩 2，实际 %d", l)
	}
	if p.Words[0].Number != 1 || p.Words[1].Number != 3 {
		t.Fatalf("应只剩 #1 #3：%+v", p.Words)
	}
	if l := PruneWordPlan(p, []int{1, 3, 999}); l != 0 {
		t.Fatalf("第三次应全清空，实际 %d", l)
	}
}

// 题号对不上（老 plan / 外部 CLI 生成的 plan / 客户端多报）不能误删任何词。
func TestPruneWordPlanUnknownNumbersAndNil(t *testing.T) {
	p := testWordPlan()
	if l := PruneWordPlan(p, []int{88, 999}); l != 4 {
		t.Errorf("对不上的题号不该剪掉词，实际剩 %d", l)
	}
	if l := PruneWordPlan(p, nil); l != 4 {
		t.Errorf("空提交不该剪掉词，实际剩 %d", l)
	}
	if l := PruneWordPlan(nil, []int{1}); l != 0 {
		t.Errorf("nil plan 应返回 0 且不 panic，实际 %d", l)
	}
}
