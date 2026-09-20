package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// handleDraft —— 未回写练习的草稿存取。
//
//	GET    /api/draft?mode=words&date=2026-09-20  → { success, draft: {...} | null }
//	POST   /api/draft   { date, mode, grade_mode, items:[{number,answer,unknown,manual}] }
//	DELETE /api/draft?mode=words&date=2026-09-20  → { success }
//
// 存在的意义：写一半关掉标签页，换来的是「119 个词要重写一遍」。
// 草稿按「日期 + 模式」分键，回写成功后立即清除。
func (s *server) handleDraft(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	mode := strings.ToLower(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = draftModeWords
	}
	if !validDraftMode(mode) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "未知草稿模式：" + mode,
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		d, err := s.storage.DownloadDraft(s.ctx(), date, mode)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "draft": d, "date": date, "mode": mode, "dry_run": s.dryRun,
		})

	case http.MethodPost:
		var d Draft
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false, "error": "bad body: " + err.Error(),
			})
			return
		}
		if d.Date == "" {
			d.Date = date
		}
		if d.Mode == "" {
			d.Mode = mode
		}
		d.Mode = strings.ToLower(d.Mode)
		if !validDraftMode(d.Mode) {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false, "error": "未知草稿模式：" + d.Mode,
			})
			return
		}
		// 全空草稿不落盘，改成清掉旧的 —— 否则老师打开页面什么都没写也会留个对象
		if !d.HasContent() {
			_ = s.storage.DeleteDraft(s.ctx(), d.Date, d.Mode)
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": true, "saved": false, "dry_run": s.dryRun,
			})
			return
		}
		if err := s.storage.UploadDraft(s.ctx(), &d); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "saved": true, "saved_at": d.SavedAt, "dry_run": s.dryRun,
		})

	case http.MethodDelete:
		if err := s.storage.DeleteDraft(s.ctx(), date, mode); err != nil {
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
			"success": false, "error": "GET / POST / DELETE only",
		})
	}
}

const (
	draftModeWords     = "words"
	draftModeHard      = "hard"
	draftModeSentences = "sentences"
	draftModeOffline   = "offline"
)

// validDraftMode 白名单。模式直接进入 COS 键，不放宽会让键被任意字符串打散，
// 恢复时永远对不上号。
func validDraftMode(m string) bool {
	switch strings.ToLower(m) {
	case draftModeWords, draftModeHard, draftModeSentences, draftModeOffline:
		return true
	}
	return false
}
