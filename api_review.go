package main

import (
	"net/http"
	"strings"
)

// handleReview —— 当天已回写练习的只读回看。
//
//	GET    /api/review?mode=words&date=2026-09-20  → { success, review: {...} | null }
//	DELETE /api/review?mode=words&date=2026-09-20  → { success }
//
// 存在的意义：回写成功后到期日被推到未来，「今日练习」就空了，老师当天练了什么、
// 哪题写错，从此查不到。快照在 /api/record 回写成功时自动写，这里只负责读。
func (s *server) handleReview(w http.ResponseWriter, r *http.Request, st *Storage) {
	// 体验版：没有快照可言（试用从不落档），直接回空，别去 COS 找。
	if st.uid == trialUID {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}

	// ⚠️ date 会拼进 COS key，必须严格校验（跟 /api/draft 一个口径）。
	date, ok := safeDateParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "日期格式不对，要 YYYY-MM-DD",
		})
		return
	}
	mode := strings.ToLower(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = draftModeWords
	}
	if !validReviewMode(mode) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "未知回看模式：" + mode,
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		snap, err := st.DownloadReview(s.ctx(), date, mode)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		// 词性 / 备注同样只在读的时候 join，不落进快照
		s.enrichReviewMeta(s.ctx(), st, snap)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "review": snap, "date": date, "mode": mode, "dry_run": s.dryRun,
		})

	case http.MethodDelete:
		if err := st.DeleteReview(s.ctx(), date, mode); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "dry_run": s.dryRun,
		})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET / DELETE only",
		})
	}
}
