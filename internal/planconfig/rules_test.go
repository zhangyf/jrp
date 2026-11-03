package planconfig

import (
	"encoding/json"
	"testing"
)

// 内置默认值是配额外置的兜底基准：改这里等于改线上出题量，必须盯着。
func TestBuiltinPlanConfigDefaults(t *testing.T) {
	b := BuiltinPlanConfig()
	if b.SentenceBaseCount != 20 || b.SentenceReviewCount != 20 {
		t.Fatalf("内置默认应为 20+20，得到 %+v", b)
	}
	if b.SentenceReviewCircleRatio == nil || *b.SentenceReviewCircleRatio != 70 {
		t.Fatalf("内置三圆占比应为 70，得到 %v", b.SentenceReviewCircleRatio)
	}
}

func TestPlanCircleRatioBounds(t *testing.T) {
	zero, hundred, neg, over := 0, 100, -1, 101
	cases := []struct {
		name string
		r    *int
		want int
	}{
		{"没配(nil)", nil, 70},
		{"0 有意关掉", &zero, 0},
		{"100 上限", &hundred, 100},
		{"-1 越界", &neg, 70},
		{"101 越界", &over, 70},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlanCircleRatio(PlanConfig{SentenceReviewCircleRatio: c.r})
			if got != c.want {
				t.Fatalf("PlanCircleRatio = %d, want %d", got, c.want)
			}
		})
	}
}

func TestValidCircleRatioBounds(t *testing.T) {
	zero, hundred, neg, over := 0, 100, -1, 101
	cases := []struct {
		name string
		r    *int
		want bool
	}{
		{"nil 合法", nil, true},
		{"0 合法", &zero, true},
		{"100 合法", &hundred, true},
		{"-1 非法", &neg, false},
		{"101 非法", &over, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidCircleRatio(PlanConfig{SentenceReviewCircleRatio: c.r}); got != c.want {
				t.Fatalf("ValidCircleRatio = %v, want %v", got, c.want)
			}
		})
	}
}

func TestValidPlanCountBounds(t *testing.T) {
	cases := []struct {
		n    int
		want bool
	}{
		{-1, false}, {0, true}, {1, true}, {50, true}, {51, false}, {5000, false},
	}
	for _, c := range cases {
		if got := ValidPlanCount(c.n); got != c.want {
			t.Fatalf("ValidPlanCount(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

func TestParsePlanConfigRatioAndJSON(t *testing.T) {
	// ratio 是指针字段：没配必须是 nil，不能塌成 0（否则「没配」和「有意关掉」分不开）。
	c, err := ParsePlanConfig([]byte(`{"sentence_base_count":18,"sentence_review_count":30}`))
	if err != nil || c.SentenceBaseCount != 18 || c.SentenceReviewCount != 30 || c.SentenceReviewCircleRatio != nil {
		t.Fatalf("没配 ratio 时应为 nil：%+v / %v", c, err)
	}
	c, err = ParsePlanConfig([]byte(`{"sentence_base_count":20,"sentence_review_count":20,"sentence_review_circle_ratio":0}`))
	if err != nil || c.SentenceReviewCircleRatio == nil || *c.SentenceReviewCircleRatio != 0 {
		t.Fatalf("配 0 应解析出 0 指针：%+v / %v", c, err)
	}
	// 序列化往返：omitempty 下 nil 不写该键，0 必须写出来。
	b1, _ := json.Marshal(PlanConfig{SentenceBaseCount: 20, SentenceReviewCount: 20})
	if string(b1) != `{"sentence_base_count":20,"sentence_review_count":20}` {
		t.Fatalf("nil ratio 不该出现在 JSON：%s", b1)
	}
	b2, _ := json.Marshal(BuiltinPlanConfig())
	if string(b2) != `{"sentence_base_count":20,"sentence_review_count":20,"sentence_review_circle_ratio":70}` {
		t.Fatalf("内置默认序列化不对：%s", b2)
	}
}

func TestChoosePlanConfigRejectsBadCircleRatio(t *testing.T) {
	bad := -1
	cfg, src := ChoosePlanConfig(PlanConfig{
		SentenceBaseCount: 20, SentenceReviewCount: 20,
		SentenceReviewCircleRatio: &bad,
	}, nil)
	if src != "builtin" || !planConfigEqual(cfg, BuiltinPlanConfig()) {
		t.Fatalf("ratio 越界必须回退内置，得到 %+v / %s", cfg, src)
	}
}
