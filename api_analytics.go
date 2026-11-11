package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangyf/objstore"
)

// ============================================================================
// 用户行为埋点（analytics）
//
// 目标：回答「页面停留时间 / 页面浏览顺序 / 功能用得多少」三类问题，
// 又不引入任何第三方依赖、不让个人学习数据出域。
//
// 采集三类事件（前端 web/track.js 上报，navigator.sendBeacon 异步）：
//   - view_enter / view_leave：板块切换 + 停留时长
//   - action：核心动作（提交判分、提交造句、听力播放、搜索、录词……）
//
// 存储：全站一份，落在 <cosPrefix>/analytics/<YYYY-MM-DD>/<纳秒>_<随机>.json，
// 每个事件一个独立对象。COS 是对象存储、没有 append，一条一对象天然无并发
// 冲突、多实例也安全；量级每天几百条，对象数完全不是负担。分析时按天 prefix 读回。
// ============================================================================

const (
	analyticsEventMaxBatch = 20                  // 单次上报最多条数
	analyticsMaxPageLen    = 20                  // 板块名长度上限
	analyticsMaxActLen     = 40                  // 动作名长度上限
	analyticsMaxDurMs      = 24 * 60 * 60 * 1000 // 停留时长上限：一天
	analyticsMaxDays       = 90                  // 报表最多回看天数
)

// analyticsEvent 单条埋点事件。UID 不信任前端，由 withUser 注入。
type analyticsEvent struct {
	V    int64  `json:"v"`
	UID  string `json:"uid,omitempty"`
	SID  string `json:"sid,omitempty"`
	E    string `json:"e"`              // view_enter / view_leave / action
	Page string `json:"page,omitempty"` // 板块名
	Dur  int64  `json:"dur,omitempty"`  // 停留时长 ms
	Act  string `json:"act,omitempty"`  // 动作名
	N    int    `json:"n,omitempty"`    // 可选计数
	T    int64  `json:"t"`              // 客户端时间戳 ms
}

var analyticsEventNames = map[string]bool{
	"view_enter": true,
	"view_leave": true,
	"action":     true,
}

// analyticsKey 某天的埋点目录前缀。
func (s *Storage) analyticsKey(date string) string {
	return s.cosPrefix() + "/analytics/" + date
}

// uploadAnalyticsEvent 写一条事件。dryRun 时短路（跟其余 Upload* 一致）。
func (s *Storage) uploadAnalyticsEvent(ctx context.Context, date string, ev analyticsEvent) error {
	if s.dryRun {
		return nil
	}
	key := fmt.Sprintf("%s/%d_%s.json", s.analyticsKey(date), time.Now().UnixNano(), randomToken(4))
	return s.store.PutObject(ctx, key, []byte(toJSON(ev)))
}

// listAnalyticsDates 列出有埋点数据的日期（从 key 第三段解析），按天倒序。
func (s *Storage) listAnalyticsDates(ctx context.Context) ([]string, error) {
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: s.cosPrefix() + "/analytics/", Delimiter: ""})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var dates []string
	for _, o := range objs {
		// key 形如 <prefix>/analytics/2026-11-11/1752..._xxxx.json
		rest := strings.TrimPrefix(o.Key, s.cosPrefix()+"/analytics/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 {
			continue
		}
		if _, err := time.Parse("2006-01-02", parts[0]); err != nil {
			continue
		}
		if !seen[parts[0]] {
			seen[parts[0]] = true
			dates = append(dates, parts[0])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates, nil
}

// listAnalyticsEvents 读某天的所有事件（按 key 排序保证时间大致有序）。
func (s *Storage) listAnalyticsEvents(ctx context.Context, date string) ([]analyticsEvent, error) {
	objs, err := s.store.ListObjects(ctx, objstore.ListOptions{Prefix: s.analyticsKey(date) + "/", Delimiter: ""})
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
	out := make([]analyticsEvent, 0, len(objs))
	for _, o := range objs {
		data, err := s.store.GetAll(ctx, o.Key)
		if err != nil {
			continue // 单条读不到不拖垮整体
		}
		var ev analyticsEvent
		if jsonUnmarshal(data, &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

// handleEvent 收埋点事件并落 COS。走 withUser：uid 从会话拿，不信前端。
func (s *server) handleEvent(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 POST"})
		return
	}
	var body struct {
		Events []analyticsEvent `json:"events"`
	}
	if err := jsonUnmarshalBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "body 不是合法 JSON"})
		return
	}
	if len(body.Events) == 0 || len(body.Events) > analyticsEventMaxBatch {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("events 条数要在 1~%d 之间", analyticsEventMaxBatch),
		})
		return
	}

	uid := st.UserID()
	now := time.Now()
	accepted := 0
	for i := range body.Events {
		ev := body.Events[i]
		ev.UID = uid // 覆盖前端可能伪造的 uid
		if err := validateAnalyticsEvent(ev, now); err != nil {
			continue // 单条不合法跳过，不整批拒绝
		}
		date := time.UnixMilli(ev.T).Format("2006-01-02")
		if err := st.uploadAnalyticsEvent(r.Context(), date, ev); err != nil {
			continue
		}
		accepted++
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "accepted": accepted})
}

// validateAnalyticsEvent 校验单条事件：事件名白名单、字段长度与数值范围。
func validateAnalyticsEvent(ev analyticsEvent, now time.Time) error {
	if !analyticsEventNames[ev.E] {
		return fmt.Errorf("未知事件名 %q", ev.E)
	}
	if len(ev.Page) > analyticsMaxPageLen {
		return fmt.Errorf("page 太长")
	}
	if len(ev.Act) > analyticsMaxActLen {
		return fmt.Errorf("act 太长")
	}
	if ev.Dur < 0 || ev.Dur > analyticsMaxDurMs {
		return fmt.Errorf("dur 越界")
	}
	if ev.T <= 0 {
		return fmt.Errorf("缺时间戳")
	}
	// 时间戳别太离谱：前后各放宽一天（客户端时钟可能偏），挡住乱写的历史/未来值。
	day := int64(24 * 60 * 60 * 1000)
	if ev.T < now.Add(-2*24*time.Hour).UnixMilli()-day || ev.T > now.Add(24*time.Hour).UnixMilli()+day {
		return fmt.Errorf("时间戳越界")
	}
	return nil
}

// handleAnalytics 报表聚合接口。走 withAdmin：只有 owner 能看全站行为。
// GET /api/analytics?days=7
func (s *server) handleAnalytics(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{"success": false, "error": "用 GET"})
		return
	}
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= analyticsMaxDays {
			days = n
		}
	}

	dates, err := st.listAnalyticsDates(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if len(dates) > days {
		dates = dates[:days]
	}

	var all []analyticsEvent
	for _, d := range dates {
		evs, err := st.listAnalyticsEvents(r.Context(), d)
		if err != nil {
			continue
		}
		all = append(all, evs...)
	}

	writeJSON(w, http.StatusOK, aggregateAnalytics(dates, all))
}

// aggregateAnalytics 把一批事件聚合成报表。
func aggregateAnalytics(dates []string, events []analyticsEvent) map[string]interface{} {
	type kv struct {
		K string `json:"k"`
		N int    `json:"n"`
	}
	actionCounts := map[string]int{}
	// 停留：page → 时长列表
	pageDurs := map[string][]int64{}
	pageViews := map[string]int{}
	// 流转：按 (uid,sid) 分组，组内 view_enter 按时间排序，相邻两两成 from→to
	transitions := map[string]int{}
	active := map[string]int{}

	// 组内排序用：先按 uid+sid 归集 view_enter
	type enterKey struct{ uid, sid string }
	enters := map[enterKey][]analyticsEvent{}

	for _, ev := range events {
		active[ev.UID]++
		switch ev.E {
		case "action":
			actionCounts[ev.Act]++
		case "view_enter":
			pageViews[ev.Page]++
			k := enterKey{ev.UID, ev.SID}
			enters[k] = append(enters[k], ev)
		case "view_leave":
			pageDurs[ev.Page] = append(pageDurs[ev.Page], ev.Dur)
		}
	}

	// 流转：组内按时间排序
	for _, list := range enters {
		sort.Slice(list, func(i, j int) bool { return list[i].T < list[j].T })
		for i := 0; i+1 < len(list); i++ {
			from, to := list[i].Page, list[i+1].Page
			if from != "" && to != "" && from != to {
				transitions[from+">"+to]++
			}
		}
	}

	// 排序输出，前端不用自己排
	actionRows := make([]kv, 0, len(actionCounts))
	for k, n := range actionCounts {
		actionRows = append(actionRows, kv{k, n})
	}
	sort.Slice(actionRows, func(i, j int) bool { return actionRows[i].N > actionRows[j].N })

	type pageStat struct {
		Page  string `json:"page"`
		Views int    `json:"views"`
		AvgMS int64  `json:"avg_ms"`
		MedMS int64  `json:"med_ms"`
	}
	pageStats := make([]pageStat, 0, len(pageViews))
	for page, views := range pageViews {
		durs := pageDurs[page]
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		pageStats = append(pageStats, pageStat{Page: page, Views: views, AvgMS: meanInt64(durs), MedMS: medianInt64(durs)})
	}
	sort.Slice(pageStats, func(i, j int) bool { return pageStats[i].Views > pageStats[j].Views })

	transRows := make([]kv, 0, len(transitions))
	for k, n := range transitions {
		transRows = append(transRows, kv{k, n})
	}
	sort.Slice(transRows, func(i, j int) bool { return transRows[i].N > transRows[j].N })

	activeRows := make([]kv, 0, len(active))
	for k, n := range active {
		if k == "" {
			k = "(owner)"
		}
		activeRows = append(activeRows, kv{k, n})
	}
	sort.Slice(activeRows, func(i, j int) bool { return activeRows[i].N > activeRows[j].N })

	return map[string]interface{}{
		"success":       true,
		"command":       "analytics",
		"days":          len(dates),
		"dates":         dates,
		"events_total":  len(events),
		"action_counts": actionRows,
		"page_stats":    pageStats,
		"transitions":   transRows,
		"active_users":  activeRows,
	}
}

func meanInt64(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	var sum int64
	for _, v := range vals {
		sum += v
	}
	return sum / int64(len(vals))
}

func medianInt64(vals []int64) int64 {
	n := len(vals)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}

// jsonUnmarshalBody 读请求体解析 JSON（不校验 Content-Type，sendBeacon 会带
// application/json 或 text/plain，body 都是 JSON 字符串）。
func jsonUnmarshalBody(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return jsonUnmarshal(data, v)
}
