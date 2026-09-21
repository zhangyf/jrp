package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// handleRecord 统一落库入口。
//
//	POST /api/record
//	{ "plan_date":"2026-09-20", "mode":"daily",
//	  "word_results":[{"number":1,"correct":true}],
//	  "sentence_results":[{"number":1,"correct":false,"answer":"...","chinese":"..."}] }
//
// 单词结果走原有的 ApplyRecord（改档案、bump 版本），造句结果走 ApplySentenceState
// （写 sentence_wrong.json / sentence_history.json）—— 两者是两套独立的间隔重复。
//
// 返回里带 tomorrow 预览：老师练完立刻能看到明天要复习多少，这就是
// 「练习完自动生成下次的练习题」。
func (s *server) handleRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var input RecordInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "bad body: " + err.Error(),
		})
		return
	}
	if input.Language == "" {
		input.Language = s.lang
	}
	if input.PlanDate == "" {
		input.PlanDate = time.Now().Format("2006-01-02")
	}

	out := map[string]interface{}{"success": true, "dry_run": s.dryRun}

	// --- 单词 ---
	if len(input.WordResults) > 0 {
		res, err := ApplyRecord(s.ctx(), s.storage, s.lang, input)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		out["words"] = res

		// 回写成功 → 存当天快照，供老师当天回看（只读）。
		// 档案里只有累计值，不记当次对错，不存就永远查不回来了。
		// 存失败不影响回写结果 —— 练习已经落库了，回看只是锦上添花。
		if len(input.ReviewItems) > 0 {
			mode := draftModeWords
			if input.Hard {
				mode = draftModeHard
			}
			snap := &ReviewSnapshot{
				Date:  input.PlanDate,
				Mode:  mode,
				Items: input.ReviewItems,
			}
			if err := s.storage.UploadReview(s.ctx(), snap); err != nil {
				out["review_saved"] = false
				out["review_error"] = err.Error()
			} else {
				out["review_saved"] = true
			}
		}
	}

	// --- 造句 ---
	if len(input.SentenceResults) > 0 {
		hist, err := s.storage.DownloadSentenceHistory(s.ctx())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "读取造句历史失败：" + err.Error(),
			})
			return
		}
		wrong, err := s.storage.DownloadSentenceWrong(s.ctx())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "读取错句失败：" + err.Error(),
			})
			return
		}
		wrong, hist = ApplySentenceState(wrong, hist, input.SentenceResults, input.PlanDate)
		if err := s.storage.UploadSentenceWrong(s.ctx(), wrong); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "写错句失败：" + err.Error(),
			})
			return
		}
		if err := s.storage.UploadSentenceHistory(s.ctx(), hist); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "写造句历史失败：" + err.Error(),
			})
			return
		}
		// 造句「提交之后才换下一批」：清掉当天锁定的 plan。
		// 下一次 GET /api/plan?mode=sentences 发现没有存档，才会重新出题。
		// 删失败不报错 —— 最坏结果是下一批还是这几句，不会丢数据。
		_ = s.storage.DeleteSentencePlan(s.ctx(), input.PlanDate)

		correct, wrongN := 0, 0
		for _, sr := range input.SentenceResults {
			if sr.Correct {
				correct++
			} else {
				wrongN++
			}
		}
		out["sentences"] = map[string]interface{}{
			"correct":         correct,
			"wrong":           wrongN,
			"plan_date":       input.PlanDate,
			"next_on_refresh": true, // 前端提示「换下一批」
		}
	}

	// --- 明日预告 ---
	out["tomorrow"] = s.tomorrowPreview()

	writeJSON(w, http.StatusOK, out)
}

// tomorrowPreview 算明天到期多少词。只读，不写任何 COS 对象。
func (s *server) tomorrowPreview() map[string]interface{} {
	ctx := s.ctx()
	tomorrow := time.Now().AddDate(0, 0, 1)

	data, _, err := s.storage.DownloadLatestArchive(ctx)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	arc, err := ParseArchive(string(data), s.lang)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	plan := BuildDuePlan(arc, s.lang, tomorrow)
	return map[string]interface{}{
		"date":      tomorrow.Format("2006-01-02"),
		"due_count": len(plan.Words),
		"by_status": countByStatus(plan.Words),
	}
}

func parseFloatParam(r *http.Request, name string, def float64) float64 {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

func intParam(r *http.Request, name string, def int) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
