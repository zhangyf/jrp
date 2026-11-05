package main

// api_listening_progress.go —— 听力实际播放时长的上报与统计。
//
// 老师要的：统计「实际听的时长」（小程序 + web 两端），在统计页的 JLPT
// N4/N5 等级那里体现练习进度。口径（2026-11-05 老师拍板）：
//
//	① 只算**正在播放**的秒数 —— 暂停、拖动进度条跳过的时间不计。
//	   前端在 timeupdate / onTimeUpdate 里累加相邻两次的差值，
//	   seek 造成的大跳变自己排除，攒够一小段或退出页面时上报一次。
//	② 不设目标，只显示累计时长。
//
// 存储：个人档案下 listening_progress.json（跟 quota 一样落在 users/<uid>/
// 下，owner 落裸前缀）。按等级累计，同时留一份按自然日的明细，将来想画
// 「每天听了多久」的趋势不用回头补数据。
//
//	{ "total": {"N5": 1234, "N4": 567}, "by_day": {"2026-11-05": {"N5": 300}} }
//
// 单位一律是**秒**（float），前端换算成分钟显示。上报单次封顶 120 秒，
// 防止前端 bug（比如差值没排除 seek）一次灌进几千秒。

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/stats"
)

// listeningProgressFile 个人听力时长档案的文件名（相对 personalBase）。
const listeningProgressFile = "/listening_progress.json"

// maxReportSeconds 单次上报封顶（秒）。正常前端每 15 秒左右上报一次，
// 120 秒已经很宽松；超过说明差值没排除 seek，直接截断。
const maxReportSeconds = 120

// levelKeyRe 统计档里等级名的校验，跟 api_listening.go 的 levelRe 一致。
var levelKeyRe = regexp.MustCompile(`^N[1-5]$`)

// listeningProgress 听力时长档案。
type listeningProgress struct {
	Total map[string]float64            `json:"total"`  // 等级 -> 累计秒
	ByDay map[string]map[string]float64 `json:"by_day"` // 日期 -> 等级 -> 当日秒
}

// listeningProgressKey 个人档案 key。
func (s *Storage) listeningProgressKey() string {
	return s.personalBase() + listeningProgressFile
}

// loadListeningProgress 读档案；没有或坏档返回空结构（不报错，第一听就是空）。
func loadListeningProgress(ctx context.Context, st *Storage) *listeningProgress {
	p := &listeningProgress{Total: map[string]float64{}, ByDay: map[string]map[string]float64{}}
	if st.store == nil {
		return p
	}
	data, err := st.store.GetAll(ctx, st.listeningProgressKey())
	if err != nil || len(data) == 0 {
		return p
	}
	var got listeningProgress
	if json.Unmarshal(data, &got) != nil {
		return p
	}
	if got.Total != nil {
		p.Total = got.Total
	}
	if got.ByDay != nil {
		p.ByDay = got.ByDay
	}
	return p
}

// handleListeningProgress 上报听力时长。POST {level, seconds}。
//
// 挂 withUser（跟 /api/record 一样，改个人档案的接口不匿名）。体验版不在
// 白名单里，走不到这儿 —— trial 用户点了视频就先弹登录。
func (s *server) handleListeningProgress(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}
	if st.uid == trialUID {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}

	var body struct {
		Level   string  `json:"level"`
		Seconds float64 `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "bad body: " + err.Error(),
		})
		return
	}
	level := strings.ToUpper(strings.TrimSpace(body.Level))
	if !levelKeyRe.MatchString(level) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "level 必须是 N1..N5",
		})
		return
	}
	// 负数（时钟回拨 / 前端 bug）当 0；超上限截断。
	secs := body.Seconds
	if secs < 0 {
		secs = 0
	}
	if secs > maxReportSeconds {
		secs = maxReportSeconds
	}
	if secs == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "added": 0})
		return
	}

	if st.dryRun {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "added": secs, "dry_run": true,
		})
		return
	}

	ctx := s.ctx()
	p := loadListeningProgress(ctx, st)
	today := time.Now().Format("2006-01-02")
	p.Total[level] += secs
	if p.ByDay[today] == nil {
		p.ByDay[today] = map[string]float64{}
	}
	p.ByDay[today][level] += secs

	if err := st.store.PutObject(ctx, st.listeningProgressKey(), []byte(toJSON(p))); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "写听力档案失败：" + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "added": secs, "total": p.Total[level],
	})
}

// listeningTotals 给统计页用的：各等级累计秒数。
// 读不到（没听过）返回空 map，前端按「没有听力数据」处理。
func listeningTotals(ctx context.Context, st *Storage) map[string]float64 {
	if st.uid == trialUID {
		return map[string]float64{}
	}
	return loadListeningProgress(ctx, st).Total
}

// sortedListeningLevels 把累计秒数排成稳定的等级列表，前端直接摆。
func sortedListeningLevels(total map[string]float64) []stats.ListeningLevel {
	var out []stats.ListeningLevel
	for lv, sec := range total {
		if sec <= 0 {
			continue
		}
		out = append(out, stats.ListeningLevel{Level: lv, Seconds: sec})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	return out
}
