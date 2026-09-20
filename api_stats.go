package main

import (
	"net/http"
	"strconv"
)

// handleStats 学习进度统计，复用 CLI 的 ComputeStats。
//
//	GET /api/stats?days=30
func (s *server) handleStats(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}

	res, err := ComputeStats(s.ctx(), s.storage, s.lang, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"language":  s.lang,
		"days":      days,
		"snapshots": res.Snapshots,
		"changes":   res.Changes,
		"detail":    res.Detail,
	})
}
