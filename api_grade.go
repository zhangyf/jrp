package main

import (
	"encoding/json"
	"net/http"
)

// handleGrade 判分接口（给小程序用）。
//
//	POST /api/grade
//	{ "mode":"either", "items":[{"word":"すし(寿司)","answer":"寿司"}] }
//	→ { "success":true, "mode":"either", "results":[true] }
//
// web 端在浏览器里用 web/grade.js 本地判；小程序没法复用那份代码，
// 与其在小程序里再抄一份（两份实现迟早漂移），不如让它调这里 ——
// 判分口径复用 Go 的 GradeAnswer，全站只有一份实现。
//
// 「对答案」才调，一次把整屏的答案批量判完，不是每敲一个字就打一次请求。
func (s *server) handleGrade(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var input struct {
		Mode  string `json:"mode"`
		Items []struct {
			Word   string `json:"word"`   // 目标词形「かな(漢字)」，活用练习里传期望答案
			Answer string `json:"answer"` // 老师写的答案
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "bad body: " + err.Error(),
		})
		return
	}

	mode := ParseGradeMode(input.Mode)
	results := make([]bool, len(input.Items))
	for i, it := range input.Items {
		// 空答案不可能是对的，别白跑一遍判分（也避免空串被当合法写法）
		if it.Answer == "" || it.Word == "" {
			results[i] = false
			continue
		}
		results[i] = GradeAnswer(it.Answer, it.Word, mode)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"mode":    string(mode),
		"results": results,
		"dry_run": s.dryRun,
	})
}
