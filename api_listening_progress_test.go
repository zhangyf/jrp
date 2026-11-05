package main

// api_listening_progress_test.go —— 听力时长上报的口径单测。
//
// 老师定的口径（2026-11-05）：
//   - level 必须是 N1..N5，否则 400；
//   - 单次上报封顶 120 秒（防 seek 差值没排除一次灌进几千秒）；
//   - 负数当 0，0 不落盘；
//   - 累加进个人档案，按等级累计。
//
// dryRun 模式下不写 COS，但校验与封顶逻辑照跑，正好用来测口径。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhangyf/jrp/internal/listening"
)

func TestListeningProgressRejectsBadLevel(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	for _, lv := range []string{"", "N6", "n9", "N0", "../../etc", "N5/../../x"} {
		body := `{"level":"` + lv + `","seconds":30}`
		req := httptest.NewRequest(http.MethodPost, "/api/listening/progress", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.handleListeningProgress(rec, req, srv.storage)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("level=%q: got %d, want 400（body=%s）", lv, rec.Code, rec.Body.String())
		}
	}
}

func TestListeningProgressCapsAndClamps(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	cases := []struct {
		name    string
		body    string
		wantAdd float64 // 期望 added 字段
	}{
		{"正常", `{"level":"N5","seconds":30}`, 30},
		{"超上限截断", `{"level":"N5","seconds":9999}`, maxReportSeconds},
		{"负数当0", `{"level":"N5","seconds":-5}`, 0},
		{"小数保留", `{"level":"N4","seconds":12.3}`, 12.3},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/listening/progress", strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		srv.handleListeningProgress(rec, req, srv.storage)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200（body=%s）", c.name, rec.Code, rec.Body.String())
			continue
		}
		got := map[string]interface{}{}
		if err := json.Unmarshal([]byte(rec.Body.String()), &got); err != nil {
			t.Errorf("%s: 解析响应失败：%v", c.name, err)
			continue
		}
		added, _ := got["added"].(float64)
		if added != c.wantAdd {
			t.Errorf("%s: added=%v, want %v", c.name, added, c.wantAdd)
		}
	}
}

func TestListeningProgressRejectsBadBody(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	req := httptest.NewRequest(http.MethodPost, "/api/listening/progress", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	srv.handleListeningProgress(rec, req, srv.storage)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("坏 body: got %d, want 400", rec.Code)
	}
}

func TestListeningProgressRejectsNonPost(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/listening/progress", nil)
	rec := httptest.NewRecorder()
	srv.handleListeningProgress(rec, req, srv.storage)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: got %d, want 405", rec.Code)
	}
}

func TestSortedListeningLevels(t *testing.T) {
	goal := listening.Goal{N5: 1500, N4: 3000}
	got := sortedListeningLevels(map[string]float64{
		"N4": 300, "N5": 1200, "N3": 0, // N3 是 0，不该出现
	}, goal)
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2（0 秒的等级要过滤掉）", len(got))
	}
	if got[0].Level != "N4" || got[1].Level != "N5" {
		t.Errorf("顺序错：got %s,%s want N4,N5", got[0].Level, got[1].Level)
	}
	if got[0].Seconds != 300 || got[1].Seconds != 1200 {
		t.Errorf("秒数错：got %v,%v", got[0].Seconds, got[1].Seconds)
	}
	// goal 要按等级带上，前端好算百分比
	if got[0].GoalMinutes != 3000 || got[1].GoalMinutes != 1500 {
		t.Errorf("goal 错：got %d,%d want N4=3000,N5=1500", got[0].GoalMinutes, got[1].GoalMinutes)
	}
}

// goal 配置异常（零值）时，sortedListeningLevels 要回退内置默认，不能除零。
func TestSortedListeningLevelsBadGoalFallback(t *testing.T) {
	got := sortedListeningLevels(map[string]float64{"N5": 600}, listening.Goal{})
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].GoalMinutes != listening.DefaultN5GoalMinutes {
		t.Errorf("坏 goal 应回内置默认：got %d, want %d", got[0].GoalMinutes, listening.DefaultN5GoalMinutes)
	}
}
