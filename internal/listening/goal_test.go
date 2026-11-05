package listening

import (
	"errors"
	"testing"
)

func TestParseGoalEmpty(t *testing.T) {
	g, err := ParseGoal(nil)
	if err != nil || g != (Goal{}) {
		t.Errorf("空数据应返回零值，got %v err=%v", g, err)
	}
}

func TestParseGoalBad(t *testing.T) {
	if _, err := ParseGoal([]byte("not json")); err == nil {
		t.Error("坏 JSON 应返回 error")
	}
}

func TestParseGoalOK(t *testing.T) {
	g, err := ParseGoal([]byte(`{"n5":1500,"n4":3000}`))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if g.N5 != 1500 || g.N4 != 3000 {
		t.Errorf("got %+v, want {1500 3000}", g)
	}
}

func TestBuiltinGoal(t *testing.T) {
	g := BuiltinGoal()
	if g.N5 != DefaultN5GoalMinutes || g.N4 != DefaultN4GoalMinutes {
		t.Errorf("内置默认错：got %+v", g)
	}
}

func TestValidGoal(t *testing.T) {
	if !ValidGoal(Goal{N5: 1500, N4: 3000}) {
		t.Error("正常值应合法")
	}
	if ValidGoal(Goal{N5: 0, N4: 3000}) {
		t.Error("0 应判坏（进度条永远 0% 没意义）")
	}
	if ValidGoal(Goal{N5: -5, N4: 3000}) {
		t.Error("负数应判坏")
	}
	if ValidGoal(Goal{N5: MaxGoalMinutes + 1, N4: 3000}) {
		t.Error("超上限应判坏")
	}
}

func TestChooseGoalFallbacks(t *testing.T) {
	// 读失败 → 内置
	g, src := ChooseGoal(Goal{}, errors.New("cos down"))
	if src != "builtin" || g != BuiltinGoal() {
		t.Errorf("读失败应回内置：src=%s g=%v", src, g)
	}
	// 没配 → 内置
	g, src = ChooseGoal(Goal{}, nil)
	if src != "builtin" || g != BuiltinGoal() {
		t.Errorf("没配应回内置：src=%s g=%v", src, g)
	}
	// 越界 → 内置
	g, src = ChooseGoal(Goal{N5: 0, N4: 3000}, nil)
	if src != "builtin" || g != BuiltinGoal() {
		t.Errorf("越界应回内置：src=%s g=%v", src, g)
	}
	// 正常 → 用 COS
	cos := Goal{N5: 2000, N4: 4000}
	g, src = ChooseGoal(cos, nil)
	if src != "cos" || g != cos {
		t.Errorf("正常配置应用 COS：src=%s g=%v", src, g)
	}
}
