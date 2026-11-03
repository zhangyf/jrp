package llm

import (
	"sync"
	"testing"
	"time"
)

var healthTestNow = time.Date(2026, time.November, 3, 9, 0, 0, 0, time.UTC)

func testHealthModel(t *testing.T, suffix string) string {
	t.Helper()
	model := t.Name() + "/" + suffix
	health.Lock()
	delete(health.m, model)
	health.Unlock()
	t.Cleanup(func() {
		health.Lock()
		delete(health.m, model)
		health.Unlock()
	})
	return model
}

func snapshotHealth(model string) (modelHealth, bool) {
	health.Lock()
	defer health.Unlock()
	h, ok := health.m[model]
	if !ok {
		return modelHealth{}, false
	}
	return *h, true
}

// 冷却：失败 1 次照常用，连续 2 次进冷却，2 分钟到期后恢复试探。
func TestLLMModelCooling(t *testing.T) {
	now := healthTestNow
	if modelCooling(1, now.Add(cooldown), now) {
		t.Fatal("只失败 1 次不该被冷却")
	}
	if !modelCooling(failThreshold, now.Add(cooldown), now) {
		t.Fatal("连续失败到阈值必须进冷却")
	}
	if modelCooling(failThreshold, now.Add(-time.Second), now) {
		t.Fatal("冷却时间过了就该恢复")
	}
	if modelCooling(failThreshold, now, now) {
		t.Fatal("冷却精确到期时必须恢复")
	}
}

func TestMarkResultFailureThreshold(t *testing.T) {
	model := testHealthModel(t, "model")
	now := healthTestNow
	MarkResult(model, false, now)
	first, ok := snapshotHealth(model)
	if !ok || first.fails != 1 || !first.coolUntil.IsZero() || SkipModel(model, now) {
		t.Fatalf("第一次失败应只累计计数，不冷却：%+v，存在=%v", first, ok)
	}

	secondAt := now.Add(10 * time.Second)
	MarkResult(model, false, secondAt)
	second, _ := snapshotHealth(model)
	until := secondAt.Add(2 * time.Minute)
	if second.fails != 2 || !second.coolUntil.Equal(until) {
		t.Fatalf("第二次失败应从本次失败起冷却 2 分钟：%+v，期望截止 %v", second, until)
	}
	if !SkipModel(model, secondAt) || !SkipModel(model, until.Add(-time.Nanosecond)) {
		t.Fatal("从第二次失败到截止前 1 纳秒都必须跳过")
	}
	if SkipModel(model, until) || SkipModel(model, until.Add(time.Nanosecond)) {
		t.Fatal("精确到期及到期后必须恢复试探")
	}
}

func TestMarkResultSuccessClearsHealth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fails int
	}{
		{"单次失败后成功", 1},
		{"冷却期间成功", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := testHealthModel(t, "model")
			for i := 0; i < tc.fails; i++ {
				MarkResult(model, false, healthTestNow)
			}
			MarkResult(model, true, healthTestNow.Add(time.Second))
			got, ok := snapshotHealth(model)
			if !ok || got.fails != 0 || !got.coolUntil.IsZero() || SkipModel(model, healthTestNow) {
				t.Fatalf("成功应清零计数和冷却截止时间：%+v，存在=%v", got, ok)
			}
			MarkResult(model, false, healthTestNow.Add(2*time.Second))
			got, _ = snapshotHealth(model)
			if got.fails != 1 || !got.coolUntil.IsZero() || SkipModel(model, healthTestNow) {
				t.Fatalf("成功后第一次失败必须重新从 1 开始：%+v", got)
			}
		})
	}
}

func TestSkipModelExpiryDoesNotResetFailures(t *testing.T) {
	model := testHealthModel(t, "model")
	MarkResult(model, false, healthTestNow)
	MarkResult(model, false, healthTestNow)
	before, _ := snapshotHealth(model)
	until := healthTestNow.Add(2 * time.Minute)
	for _, now := range []time.Time{until.Add(-time.Nanosecond), until, until.Add(time.Minute)} {
		if got, want := SkipModel(model, now), now.Before(until); got != want {
			t.Fatalf("SkipModel(%v) = %v，期望 %v", now, got, want)
		}
		after, _ := snapshotHealth(model)
		if after != before {
			t.Fatalf("查询冷却不得改写状态：之前=%+v，之后=%+v", before, after)
		}
	}
	MarkResult(model, false, until)
	got, _ := snapshotHealth(model)
	if got.fails != 3 || !got.coolUntil.Equal(until.Add(2*time.Minute)) || !SkipModel(model, until) {
		t.Fatalf("到期不清零，再失败一次必须立即重新冷却：%+v", got)
	}
}

func TestHealthModelIsolation(t *testing.T) {
	first := testHealthModel(t, "first")
	second := testHealthModel(t, "second")
	MarkResult(first, false, healthTestNow)
	MarkResult(first, false, healthTestNow)
	MarkResult(second, false, healthTestNow)
	if !SkipModel(first, healthTestNow) || SkipModel(second, healthTestNow) {
		t.Fatal("一个模型的连续失败不得冷却另一个模型")
	}
	before, _ := snapshotHealth(first)
	MarkResult(second, true, healthTestNow)
	after, _ := snapshotHealth(first)
	if after != before || !SkipModel(first, healthTestNow) {
		t.Fatal("另一个模型成功不得重置本模型状态")
	}
	MarkResult(second, false, healthTestNow)
	MarkResult(second, false, healthTestNow)
	MarkResult(first, true, healthTestNow)
	if SkipModel(first, healthTestNow) || !SkipModel(second, healthTestNow) {
		t.Fatal("两个模型的恢复和冷却必须互相独立")
	}
}

func TestSkipModelUnknownDoesNotCreateState(t *testing.T) {
	model := testHealthModel(t, "unknown")
	for _, now := range []time.Time{healthTestNow, healthTestNow.Add(24 * time.Hour)} {
		if SkipModel(model, now) {
			t.Fatal("未知模型不得被跳过")
		}
		if got, ok := snapshotHealth(model); ok {
			t.Fatalf("查询未知模型不得创建状态：%+v", got)
		}
	}
}

func TestMarkResultFailureExtendsCooldown(t *testing.T) {
	model := testHealthModel(t, "model")
	MarkResult(model, false, healthTestNow)
	MarkResult(model, false, healthTestNow)
	later := healthTestNow.Add(30 * time.Second)
	MarkResult(model, false, later)
	got, _ := snapshotHealth(model)
	until := later.Add(2 * time.Minute)
	if got.fails != 3 || !got.coolUntil.Equal(until) {
		t.Fatalf("冷却中后续失败应顺延到最后一次失败后 2 分钟：%+v", got)
	}
	if !SkipModel(model, healthTestNow.Add(2*time.Minute)) || !SkipModel(model, until.Add(-time.Nanosecond)) {
		t.Fatal("原截止时间和新截止前仍应处于冷却")
	}
	if SkipModel(model, until) {
		t.Fatal("顺延后的精确截止时间应恢复试探")
	}
}

func TestHealthConcurrentFailuresAndQueries(t *testing.T) {
	models := []string{testHealthModel(t, "first"), testHealthModel(t, "second")}
	const workers, updates = 24, 80
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, model := range models {
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(model string) {
				defer wg.Done()
				<-start
				for j := 0; j < updates; j++ {
					MarkResult(model, false, healthTestNow)
					SkipModel(model, healthTestNow)
					SkipModel(model, healthTestNow.Add(2*time.Minute))
				}
			}(model)
		}
	}
	close(start)
	wg.Wait()
	for _, model := range models {
		got, ok := snapshotHealth(model)
		if !ok || got.fails != workers*updates || !got.coolUntil.Equal(healthTestNow.Add(2*time.Minute)) {
			t.Fatalf("并发更新不能丢计数或互相串模型：%s = %+v，期望失败 %d 次", model, got, workers*updates)
		}
		if !SkipModel(model, healthTestNow) || SkipModel(model, healthTestNow.Add(2*time.Minute)) {
			t.Fatalf("并发查询后冷却边界不符：%s", model)
		}
	}
}

func TestHealthConcurrentSuccessAndFailure(t *testing.T) {
	model := testHealthModel(t, "model")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(success bool) {
			defer wg.Done()
			<-start
			for j := 0; j < 80; j++ {
				MarkResult(model, success, healthTestNow)
				SkipModel(model, healthTestNow)
				got, _ := snapshotHealth(model)
				if (got.fails < 2 && !got.coolUntil.IsZero()) || (got.fails >= 2 && !got.coolUntil.Equal(healthTestNow.Add(2*time.Minute))) {
					t.Errorf("并发成功、失败和查询应只能观察到完整状态：%+v", got)
					return
				}
			}
		}(i%2 == 0)
	}
	close(start)
	wg.Wait()
	MarkResult(model, true, healthTestNow)
	got, _ := snapshotHealth(model)
	if got.fails != 0 || !got.coolUntil.IsZero() || SkipModel(model, healthTestNow) {
		t.Fatalf("并发结束后的成功必须完整清零：%+v", got)
	}
}
