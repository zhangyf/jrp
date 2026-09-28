package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// handlePlan 返回某天的练习计划。
//
//	GET /api/plan?date=YYYY-MM-DD&mode=words|sentences
//
// 日期语义（这里是修掉一个真实 bug 的地方）：
//   - 今天：初始化当天 v1.0 档案（若最新档案不是今天）+ 上传 plan JSON
//   - 过去：只上传 plan JSON，**不初始化档案**
//   - 未来：什么都不写，纯计算 —— 这就是「明日预告」
//
// 为什么过去/未来日期都不能初始化档案：DownloadLatestArchive 是按 LastModified
// 取最新的，一旦上传了一份非今天的档案，它就会变成 latest，之后 gen-plan /
// record 全链都被污染（每天都以为要重新初始化）。
func (s *server) handlePlan(w http.ResponseWriter, r *http.Request) {
	ctx := s.ctx()

	targetDate, ok := parseDateParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "invalid date, expected YYYY-MM-DD",
		})
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "words"
	}

	today := time.Now()
	readOnly := !sameDay(targetDate, today) && targetDate.After(today)

	data, archiveFilename, err := s.storage.DownloadLatestArchive(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("download archive: %v", err),
		})
		return
	}
	arc, err := ParseArchive(string(data), s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("parse archive: %v", err),
		})
		return
	}

	// 只有「今天」才会建当天档案；过去补做、未来预告都不动档案。
	if sameDay(targetDate, today) {
		arcDate := targetDate
		if d, _, _, perr := ParseFilename(archiveFilename); perr == nil {
			arcDate = d
		}
		if !sameDay(arcDate, targetDate) {
			AddChangelogEntry(arc, targetDate, 1, 0, "新日初始化（serve）")
			newFilename := ArchiveFilename(s.lang, targetDate, 1, 0)
			if err := s.storage.UploadArchive(ctx, newFilename, []byte(WriteArchive(arc))); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false, "error": fmt.Sprintf("init today archive: %v", err),
				})
				return
			}
		}
	}

	out := map[string]interface{}{
		"success":   true,
		"date":      targetDate.Format("2006-01-02"),
		"mode":      mode,
		"read_only": readOnly,
		"dry_run":   s.dryRun,
	}

	switch mode {
	case "sentences":
		plan, err := s.buildSentencePlan(targetDate, !readOnly)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		out["sentence_count"] = len(plan.Sentences)
		out["sentences"] = plan.Sentences

	default:
		plan := BuildDuePlan(arc, s.lang, targetDate)
		if !readOnly {
			// plan JSON 供 /api/record 把序号映射回词
			if err := s.storage.UploadPlan(ctx, plan); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false, "error": fmt.Sprintf("upload plan: %v", err),
				})
				return
			}
		}
		// 词性 / 备注只在这里 join 给前端显示，不写进 plan 存档
		// （存档只服务于 record 的序号映射，多塞字段没意义）。
		s.enrichWordMeta(ctx, plan.Words)
		out["due_count"] = len(plan.Words)
		out["words"] = plan.Words
		out["by_status"] = countByStatus(plan.Words)
	}

	writeJSON(w, http.StatusOK, out)
}

// handleHard 钉子户专项。不做 IsDue 过滤，把全部正确率低于阈值的词拉出来重练。
//
//	GET /api/hard?min_accuracy=0.6&min_reviews=3
func (s *server) handleHard(w http.ResponseWriter, r *http.Request) {
	ctx := s.ctx()

	targetDate, ok := parseDateParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "invalid date, expected YYYY-MM-DD",
		})
		return
	}

	minAcc := parseFloatParam(r, "min_accuracy", DefaultHardMinAccuracy)
	minRev := intParam(r, "min_reviews", DefaultHardMinReviews)

	data, _, err := s.storage.DownloadLatestArchive(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("download archive: %v", err),
		})
		return
	}
	arc, err := ParseArchive(string(data), s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("parse archive: %v", err),
		})
		return
	}

	plan := BuildHardPlan(arc, s.lang, targetDate, minAcc, minRev)
	// 未来日期不写：避免污染 hard plan 之外，也避免无谓的写
	if !targetDate.After(time.Now()) {
		if err := s.storage.UploadHardPlan(ctx, plan); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": fmt.Sprintf("upload hard plan: %v", err),
			})
			return
		}
	}

	s.enrichWordMeta(ctx, plan.Words)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"date":         plan.Date,
		"hard_count":   len(plan.Words),
		"min_accuracy": minAcc,
		"min_reviews":  minRev,
		"words":        plan.Words,
		"dry_run":      s.dryRun,
	})
}

// buildSentencePlan 从句库挑当天的造句。upload 为 false 时只读（明日预告场景）。
//
// 「当天锁定」是这里最重要的一条：只要 COS 上已经有这天的造句 plan，就原样返回，
// 不再重算出题。否则老师写到一半刷新（或换台设备打开）题就变了，
// 草稿对不上、白写一场。换新一批的唯一途径是回写成功 —— 见 api_record.go 的
// DeleteSentencePlan。
func (s *server) buildSentencePlan(targetDate time.Time, upload bool) (*ReviewPlan, error) {
	ctx := s.ctx()

	if upload {
		if old, err := s.storage.DownloadSentencePlan(ctx, targetDate.Format("2006-01-02")); err == nil &&
			sentencePlanLocked(old, targetDate.Format("2006-01-02")) {
			return old, nil
		}
	}

	bank, err := s.storage.DownloadSentenceBank(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取句库失败（还没建库？）：%w", err)
	}
	hist, err := s.storage.DownloadSentenceHistory(ctx)
	if err != nil {
		return nil, err
	}
	wrong, err := s.storage.DownloadSentenceWrong(ctx)
	if err != nil {
		return nil, err
	}

	n := 20
	plan := &ReviewPlan{
		Date:      targetDate.Format("2006-01-02"),
		Language:  s.lang,
		Kind:      "sentences",
		Sentences: BuildSentencePlan(bank, hist, wrong, targetDate, n),
	}

	if upload {
		if err := s.storage.UploadSentencePlan(ctx, plan); err != nil {
			return nil, fmt.Errorf("上传造句 plan 失败：%w", err)
		}
	}
	return plan, nil
}

// sentencePlanLocked 判断 COS 上已存的造句 plan 能不能直接复用 —— 「不提交就不换题」。
//
// 三个条件缺一不可：
//   - 读到了对象（下载失败时 old 是 nil，必须重新出题）
//   - 日期对得上（陈年 plan 绝不能当成今天的）
//   - 里面真有句子（空 plan 等于没出过题）
func sentencePlanLocked(old *ReviewPlan, date string) bool {
	return old != nil && old.Date == date && len(old.Sentences) > 0
}

// enrichWordMeta 把词性 / 备注 join 到计划里的每个词上。
//
// 这两样存在独立的 word_meta.json 里（不在档案表格中），按完整词形
// 「假名(汉字)」匹配。读不到就整批留空 —— 纯展示增强，绝不能因为词性表
// 挂掉或还没上传，就把今天的练习题给弄没了。
func (s *server) enrichWordMeta(ctx context.Context, words []PlanWord) {
	if len(words) == 0 {
		return
	}
	meta, err := s.storage.DownloadWordMeta(ctx)
	if err != nil || meta == nil {
		return
	}
	for i := range words {
		words[i].Pos, words[i].Sub, words[i].Note = lookupWordMeta(meta, words[i].Word)
	}
}

// lookupWordMeta 按完整词形「假名(汉字)」查词性 / 备注。
// 查不到返回三个空串 —— 页面显示「—」，不影响练习。
func lookupWordMeta(meta *WordMeta, word string) (pos, sub, note string) {
	if meta == nil {
		return "", "", ""
	}
	p, ok := meta.Items[word]
	if !ok {
		return "", "", ""
	}
	return p.Pos, p.Sub, p.Note
}

// enrichReviewMeta 给回看快照 join 词性 / 备注 —— 跟 enrichWordMeta 同一份
// word_meta.json，只是对象不同（快照里存的是 ReviewItem）。
// 快照可能是 nil（那天没练），直接返回。
func (s *server) enrichReviewMeta(ctx context.Context, snap *ReviewSnapshot) {
	if snap == nil || len(snap.Items) == 0 {
		return
	}
	meta, err := s.storage.DownloadWordMeta(ctx)
	if err != nil || meta == nil {
		return
	}
	for i := range snap.Items {
		snap.Items[i].Pos, snap.Items[i].Sub, snap.Items[i].Note =
			lookupWordMeta(meta, snap.Items[i].Word)
	}
}

func countByStatus(words []PlanWord) map[string]int {
	out := map[string]int{}
	for _, w := range words {
		if w.Status == "" {
			continue
		}
		out[w.Status]++
	}
	return out
}
