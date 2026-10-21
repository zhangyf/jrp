package main

import (
	"encoding/json"
	"net/http"
)

// 录入新词。
//
//	POST /api/words  { group, items:[{word, def}] }
//	→ { success, added, duplicates, version, total_words, dup_definitions… }
//
// 跟 CLI 的 add-words 共用 words_add.go 里那一份实现：重复词跳过、
// 中文释义撞车点名、版本号、changelog 全都在那边，不会两边各写一套漂掉。
//
// 权限：跟其他 withUser 接口一样（已登录且不是 pending/disabled）。
// 儿子也能录 —— 录进的是各自的档案，不会串到老师那份。
func (s *server) handleAddWords(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var body struct {
		Group string `json:"group"`
		Items []struct {
			Word string `json:"word"`
			Def  string `json:"def"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "请求格式不对：" + err.Error(),
		})
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "没有要录的词",
		})
		return
	}
	if len(body.Items) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "一次最多录 200 个词",
		})
		return
	}

	items := make([]WordToAdd, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, WordToAdd{Word: it.Word, Definition: it.Def})
	}

	res, err := addWordsToArchive(s.ctx(), st, body.Group, items, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":              true,
		"added":                res.Added,
		"duplicates":           res.Duplicates,
		"version":              res.Version,
		"new_filename":         res.NewFilename,
		"total_words":          res.TotalWords,
		"dup_definitions":      res.DupDefs,
		"dup_core_definitions": res.DupCores,
		"dry_run":              s.dryRun,
	})
}
