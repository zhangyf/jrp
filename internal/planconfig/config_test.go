package planconfig

import (
	"errors"
	"testing"
)

// planConfigEqual 比「生效值」而不是指针地址 —— 圆级占比是指针字段
// （nil = 没配，0 = 关掉），直接 != 会因为两个指针地址不同而永远判不等。
func planConfigEqual(a, b PlanConfig) bool {
	return a.SentenceBaseCount == b.SentenceBaseCount &&
		a.SentenceReviewCount == b.SentenceReviewCount &&
		PlanCircleRatio(a) == PlanCircleRatio(b)
}

// 配额外置的护栏全在 ChoosePlanConfig 里 —— 这里盯着四类输入：
// 没配、读失败、解析坏（体现为零值+err）、值越界，一律回退内置默认。
// 少一条就可能出现「COS 上写了份坏配置 → 造句练习整个挂掉」。
func TestChoosePlanConfigFallsBackToBuiltin(t *testing.T) {
	builtin := BuiltinPlanConfig()

	t.Run("没配过（零值）", func(t *testing.T) {
		cfg, src := ChoosePlanConfig(PlanConfig{}, nil)
		if src != "builtin" || !planConfigEqual(cfg, builtin) {
			t.Fatalf("期望回退内置，得到 %+v / %s", cfg, src)
		}
	})

	t.Run("读失败", func(t *testing.T) {
		cfg, src := ChoosePlanConfig(PlanConfig{}, errors.New("COS 炸了"))
		if src != "builtin" || !planConfigEqual(cfg, builtin) {
			t.Fatalf("读失败必须回退内置，得到 %+v / %s", cfg, src)
		}
	})

	t.Run("值越界", func(t *testing.T) {
		for _, bad := range []PlanConfig{
			{SentenceBaseCount: 5000, SentenceReviewCount: 20},
			{SentenceBaseCount: 20, SentenceReviewCount: -1},
			{SentenceBaseCount: -5, SentenceReviewCount: -5},
		} {
			cfg, src := ChoosePlanConfig(bad, nil)
			if src != "builtin" || !planConfigEqual(cfg, builtin) {
				t.Fatalf("%+v 越界必须回退内置，得到 %+v / %s", bad, cfg, src)
			}
		}
	})

	t.Run("正常配置原样生效", func(t *testing.T) {
		want := PlanConfig{SentenceBaseCount: 15, SentenceReviewCount: 24}
		cfg, src := ChoosePlanConfig(want, nil)
		if src != "cos" || cfg != want {
			t.Fatalf("期望 %+v / cos，得到 %+v / %s", want, cfg, src)
		}
	})

	t.Run("写 0 表示关掉这一类句子，是合法值", func(t *testing.T) {
		want := PlanConfig{SentenceBaseCount: 20, SentenceReviewCount: 0}
		cfg, src := ChoosePlanConfig(want, nil)
		if src != "cos" || cfg != want {
			t.Fatalf("0 必须被当成有意关掉，得到 %+v / %s", cfg, src)
		}
	})
}

// ParsePlanConfig 任何异常都返回零值，绝不把半截配置当成正常值。
func TestParsePlanConfig(t *testing.T) {
	if c, err := ParsePlanConfig(nil); err != nil || c != (PlanConfig{}) {
		t.Fatalf("空输入应返回零值无错，得到 %+v / %v", c, err)
	}
	if c, err := ParsePlanConfig([]byte("{不是 JSON")); err == nil {
		t.Fatalf("坏 JSON 必须返回 error，得到 %+v", c)
	}
	c, err := ParsePlanConfig([]byte(`{"sentence_base_count":18,"sentence_review_count":30}`))
	if err != nil || c.SentenceBaseCount != 18 || c.SentenceReviewCount != 30 {
		t.Fatalf("解析结果不对：%+v / %v", c, err)
	}
}
