package main

// api_analytics_test.go —— 用户行为埋点的口径单测。
//
// 锁住几件事（都是 2026-11-11 定案的口径）：
//   - 事件名白名单：view_enter / view_leave / action 之外一律拒；
//   - 时长、动作名长度、时间戳越界都拒；
//   - 聚合：action 计数、板块停留均值/中位、浏览流转（同 uid+sid 组内相邻
//     enter 才构成 from>to，且 from==to 不算）、active_users 计数。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateAnalyticsEvent(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		ev   analyticsEvent
		ok   bool
	}{
		{"合法 view_enter", analyticsEvent{E: "view_enter", Page: "practice", T: now.UnixMilli()}, true},
		{"合法 view_leave", analyticsEvent{E: "view_leave", Page: "practice", Dur: 5000, T: now.UnixMilli()}, true},
		{"合法 action", analyticsEvent{E: "action", Act: "grade_practice", T: now.UnixMilli()}, true},
		{"未知事件名", analyticsEvent{E: "hack", T: now.UnixMilli()}, false},
		{"page 太长", analyticsEvent{E: "view_enter", Page: strings.Repeat("x", 21), T: now.UnixMilli()}, false},
		{"act 太长", analyticsEvent{E: "action", Act: strings.Repeat("x", 41), T: now.UnixMilli()}, false},
		{"dur 负数", analyticsEvent{E: "view_leave", Page: "p", Dur: -1, T: now.UnixMilli()}, false},
		{"dur 超一天", analyticsEvent{E: "view_leave", Page: "p", Dur: analyticsMaxDurMs + 1, T: now.UnixMilli()}, false},
		{"缺时间戳", analyticsEvent{E: "view_enter", Page: "p"}, false},
		{"时间戳太旧", analyticsEvent{E: "view_enter", Page: "p", T: now.Add(-4 * 24 * time.Hour).UnixMilli()}, false},
		{"时间戳太未来", analyticsEvent{E: "view_enter", Page: "p", T: now.Add(3 * 24 * time.Hour).UnixMilli()}, false},
	}
	for _, c := range cases {
		err := validateAnalyticsEvent(c.ev, now)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestHandleEventRejectsNonPost(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/event", nil)
	rec := httptest.NewRecorder()
	srv.handleEvent(rec, req, srv.storage)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: got %d, want 405", rec.Code)
	}
}

func TestHandleEventRejectsBadBody(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	srv.handleEvent(rec, req, srv.storage)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("坏 body: got %d, want 400", rec.Code)
	}
}

func TestHandleEventRejectsEmptyBatch(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(`{"events":[]}`))
	rec := httptest.NewRecorder()
	srv.handleEvent(rec, req, srv.storage)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("空 batch: got %d, want 400", rec.Code)
	}
}

func TestHandleEventSkipsIllegalKeepsLegal(t *testing.T) {
	srv := &server{storage: &Storage{lang: "ja", dryRun: true}}
	// 两条合法 + 一条非法（事件名不在白名单），accepted 应为 2。
	body := `{"events":[
		{"e":"view_enter","page":"practice","t":` + ms(0) + `},
		{"e":"hack","t":` + ms(0) + `},
		{"e":"action","act":"grade_practice","t":` + ms(0) + `}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleEvent(rec, req, srv.storage)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200（%s）", rec.Code, rec.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["accepted"].(float64) != 2 {
		t.Errorf("accepted=%v, want 2", got["accepted"])
	}
}

func ms(offset int64) string {
	return strconv.FormatInt(time.Now().Add(time.Duration(offset)*time.Millisecond).UnixMilli(), 10)
}

func TestAggregateAnalytics(t *testing.T) {
	// 造一组事件：一个用户一次会话 practice→sentence→practice，
	// 中间埋了 grade_practice 与 commit_practice 各一次。
	now := time.Now().UnixMilli()
	events := []analyticsEvent{
		{E: "view_enter", UID: "u1", SID: "s1", Page: "practice", T: now + 1},
		{E: "action", UID: "u1", SID: "s1", Act: "grade_practice", T: now + 2},
		{E: "view_leave", UID: "u1", SID: "s1", Page: "practice", Dur: 1000, T: now + 3},
		{E: "view_enter", UID: "u1", SID: "s1", Page: "sentence", T: now + 4},
		{E: "action", UID: "u1", SID: "s1", Act: "commit_sentence", T: now + 5},
		{E: "view_enter", UID: "u1", SID: "s1", Page: "practice", T: now + 6},
		{E: "view_leave", UID: "u1", SID: "s1", Page: "practice", Dur: 3000, T: now + 7},
		// 第二个用户，只算活跃，不进流转（sid 不同）
		{E: "view_enter", UID: "u2", SID: "s2", Page: "lexicon", T: now + 8},
	}
	got := aggregateAnalytics([]string{"2026-11-11"}, events)

	// 序列化再解析成通用结构，避免与内部具名类型（[]kv / []pageStat）的断言耦合。
	data, _ := json.Marshal(got)
	var agg struct {
		ActionCounts []struct {
			K string `json:"k"`
			N int    `json:"n"`
		} `json:"action_counts"`
		PageStats []struct {
			Page  string `json:"page"`
			Views int    `json:"views"`
			AvgMS int64  `json:"avg_ms"`
			MedMS int64  `json:"med_ms"`
		} `json:"page_stats"`
		Transitions []struct {
			K string `json:"k"`
			N int    `json:"n"`
		} `json:"transitions"`
		ActiveUsers []struct {
			K string `json:"k"`
			N int    `json:"n"`
		} `json:"active_users"`
	}
	if err := json.Unmarshal(data, &agg); err != nil {
		t.Fatal(err)
	}

	// action 计数
	byAct := map[string]int{}
	for _, a := range agg.ActionCounts {
		byAct[a.K] = a.N
	}
	if byAct["grade_practice"] != 1 || byAct["commit_sentence"] != 1 {
		t.Errorf("action 计数错：%v", byAct)
	}

	// page_stats：practice 浏览 2 次，停留 1000/3000 → avg 2000, median 2000
	byPage := map[string]struct {
		views, avg, med int64
	}{}
	for _, p := range agg.PageStats {
		byPage[p.Page] = struct{ views, avg, med int64 }{int64(p.Views), p.AvgMS, p.MedMS}
	}
	if byPage["practice"].views != 2 {
		t.Errorf("practice views=%d, want 2", byPage["practice"].views)
	}
	if byPage["practice"].avg != 2000 || byPage["practice"].med != 2000 {
		t.Errorf("practice 停留 avg/med=%d/%d, want 2000/2000",
			byPage["practice"].avg, byPage["practice"].med)
	}

	// 流转：practice>sentence 1 次，sentence>practice 1 次
	byTrans := map[string]int{}
	for _, tr := range agg.Transitions {
		byTrans[tr.K] = tr.N
	}
	if byTrans["practice>sentence"] != 1 || byTrans["sentence>practice"] != 1 {
		t.Errorf("流转错：%v", byTrans)
	}
	// from==to 不算：practice→practice 不该出现
	if _, dup := byTrans["practice>practice"]; dup {
		t.Errorf("不该出现 practice>practice（同板块不算流转）")
	}

	// active_users：u1 7 条、u2 1 条
	byUser := map[string]int{}
	for _, u := range agg.ActiveUsers {
		byUser[u.K] = u.N
	}
	if byUser["u1"] != 7 || byUser["u2"] != 1 {
		t.Errorf("active_users 错：%v", byUser)
	}
}
