package offline

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zhangyf/jrp/internal/sentence"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// 锁住字段名、顺序与「无 omitempty 的字段必出」—— 这是 COS 存档契约的字节基线。
func TestPlanWordMinimalJSONIsByteStable(t *testing.T) {
	w := PlanWord{Number: 7, Word: "なかなか"}
	want := `{"number":7,"word":"なかなか","definition":"","group":"","status":""}`
	if got := mustJSON(t, w); got != want {
		t.Errorf("PlanWord JSON 变了\n got: %s\nwant: %s", got, want)
	}
}

// 日常 plan 不带钉子户统计与 join 字段，omitempty 必须生效。
func TestPlanWordDailyOmitsHardFields(t *testing.T) {
	w := PlanWord{Number: 1, Word: "とおる", Definition: "通过", Group: "第1课", Status: "🟢抽查"}
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, w)), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"error_count", "review_count", "accuracy", "pos", "sub", "note", "circle"} {
		if _, ok := m[k]; ok {
			t.Errorf("daily plan 不该出现 %s：%v", k, m)
		}
	}
}

// 真正的 0% 正确率不能被引擎吞掉 —— Accuracy 用 *float64 的全部理由。
func TestPlanWordZeroAccuracySurvives(t *testing.T) {
	zero := 0.0
	w := PlanWord{Number: 1, Word: "とおる", ErrorCount: 3, ReviewCount: 5, Accuracy: &zero}
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, w)), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m["accuracy"]
	if !ok {
		t.Fatalf("0%% 正确率被 omitempty 吃掉了：%v", m)
	}
	if v != float64(0) {
		t.Errorf("accuracy = %v, want 0", v)
	}
}

func TestReviewPlanJSONShape(t *testing.T) {
	p := ReviewPlan{
		Date: "2026-11-04", Language: "ja",
		Words:     []PlanWord{{Number: 1, Word: "とおる"}},
		Sentences: []sentence.PlanSentence{{Number: 1, Chinese: "我去学校", Answer: "私は学校へ行きます"}},
		Kind:      "hard", MinAccuracy: 0.6, MinReviews: 3,
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, p)), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"date", "language", "words", "sentences", "kind", "min_accuracy", "min_reviews"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少 key %s：%v", k, m)
		}
	}
	if m["kind"] != "hard" || m["min_accuracy"] != float64(0.6) || m["min_reviews"] != float64(3) {
		t.Errorf("可选字段值不对：%v", m)
	}
	sents, ok := m["sentences"].([]any)
	if !ok || len(sents) != 1 {
		t.Fatalf("sentences 形态不对：%v", m["sentences"])
	}
	s0, ok := sents[0].(map[string]any)
	if !ok {
		t.Fatalf("sentences[0] 不是对象：%v", sents[0])
	}
	if s0["number"] != float64(1) || s0["chinese"] != "我去学校" || s0["answer"] != "私は学校へ行きます" {
		t.Errorf("sentences[0] 不对：%v", s0)
	}
}

func TestReviewPlanOmitsEmptyOptional(t *testing.T) {
	p := ReviewPlan{Date: "2026-11-04", Language: "ja"}
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, p)), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"kind", "min_accuracy", "min_reviews"} {
		if _, ok := m[k]; ok {
			t.Errorf("零值 %s 不该出现：%v", k, m)
		}
	}
}

func TestReviewPlanRoundTrip(t *testing.T) {
	zero := 0.0
	want := ReviewPlan{
		Date: "2026-11-04", Language: "ja",
		Words:     []PlanWord{{Number: 1, Word: "とおる", Accuracy: &zero, Circle: "三圆"}},
		Sentences: []sentence.PlanSentence{{Number: 1, Chinese: "我去学校", Answer: "私は学校へ行きます"}},
		Kind:      "hard", MinAccuracy: 0.6, MinReviews: 3,
	}
	var got ReviewPlan
	if err := json.Unmarshal([]byte(mustJSON(t, want)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip 不等价\n got: %+v\nwant: %+v", got, want)
	}
}
