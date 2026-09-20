package main

import (
	"net/http"
	"strings"
	"time"
)

// handleReview —— 当天已回写练习的只读回看。
//
//	GET    /api/review?mode=words&date=2026-09-20  → { success, review: {...} | null }
//	DELETE /api/review?mode=words&date=2026-09-20  → { success }
//
// 存在的意义：回写成功后到期日被推到未来，「今日练习」就空了，老师当天练了什么、
// 哪题写错，从此查不到。快照在 /api/record 回写成功时自动写，这里只负责读。
func (s *server) handleReview(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
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
		snap, err := s.storage.DownloadReview(s.ctx(), date, mode)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "review": snap, "date": date, "mode": mode, "dry_run": s.dryRun,
		})

	case http.MethodDelete:
		if err := s.storage.DeleteReview(s.ctx(), date, mode); err != nil {
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
