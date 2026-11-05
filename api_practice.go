// api_practice.go —— 练习主链接口（计划 / 判分 / 记录 / 回看 / 草稿 / 统计 / 离线 Excel）
//
// 腿一 R2：把原本平铺在根目录的 7 个 api_*.go / storage*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；HTTP 响应、存档、Excel 字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找 handler 先看下面的来源清单。
//
// 来源与顶层声明：
// api_plan.go: handlePlan, handleHard, buildSentencePlan, enrichWordMeta, enrichReviewMeta
// api_grade.go: handleGrade
// api_record.go: handleRecord, tomorrowPreview, parseFloatParam, intParam
// api_review.go: handleReview
// api_draft.go: handleDraft, validDraftMode
// api_stats.go: handleStats
// api_offline.go: handleExport, handleImport, BuildSentencePlanFrom, saveMultipartFile, urlEscape
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/grading"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/words"
)

// ==========================================================================
// 原 api_plan.go
// ==========================================================================

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
func (s *server) handlePlan(w http.ResponseWriter, r *http.Request, st *Storage) {
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

	// 体验版：直接给内置固定题面，一次都不碰 COS。
	// 走正常路径会在 users/trial/ 下凭空初始化一份档案，之后每次拉计划都往里写。
	if st.uid == trialUID {
		writeTrialPlan(w, mode)
		return
	}

	today := time.Now()
	readOnly := !archive.SameDay(targetDate, today) && targetDate.After(today)

	// EnsureArchive：新账号第一次进来目录里什么都没有，造一份空档案。
	// 不然 /api/plan 500「no archives found」，首页直接白屏（2026-11-02 审核被拒）。
	arc, archiveFilename, err := st.EnsureArchive(ctx, s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	// 只有「今天」才会建当天档案；过去补做、未来预告都不动档案。
	if archive.SameDay(targetDate, today) {
		arcDate := targetDate
		if d, _, _, perr := archive.ParseFilename(archiveFilename); perr == nil {
			arcDate = d
		}
		if !archive.SameDay(arcDate, targetDate) {
			archive.AddChangelogEntry(arc, targetDate, 1, 0, "新日初始化（serve）")
			cfg := LangConfigs[s.lang]
			newFilename := archive.Filename(LangConfigs[s.lang].FilePrefix, targetDate, 1, 0)
			if err := st.UploadArchive(ctx, newFilename, []byte(archive.WriteArchive(arc, cfg.Name, cfg.WordColumn))); err != nil {
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
		plan, err := s.buildSentencePlan(st, targetDate, !readOnly, arc)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		out["sentence_count"] = len(plan.Sentences)
		out["sentences"] = plan.Sentences

	default:
		duePlan := plan.BuildDuePlan(arc, s.lang, targetDate, words.HoldSet(dueHold(ctx, st)))
		if !readOnly {
			// plan JSON 供 /api/record 把序号映射回词
			if err := st.UploadPlan(ctx, duePlan); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false, "error": fmt.Sprintf("upload plan: %v", err),
				})
				return
			}
		}
		// 词性 / 备注只在这里 join 给前端显示，不写进 plan 存档
		// （存档只服务于 record 的序号映射，多塞字段没意义）。
		s.enrichWordMeta(ctx, st, duePlan.Words)
		out["due_count"] = len(duePlan.Words)
		out["words"] = duePlan.Words
		out["by_status"] = plan.CountByStatus(duePlan.Words)
	}

	writeJSON(w, http.StatusOK, out)
}

// handleHard 钉子户专项。不做 IsDue 过滤，把全部正确率低于阈值的词拉出来重练。
//
//	GET /api/hard?min_accuracy=0.6&min_reviews=3
func (s *server) handleHard(w http.ResponseWriter, r *http.Request, st *Storage) {
	// 体验版：内置固定题面，不碰 COS（理由同 handlePlan）。
	if st.uid == trialUID {
		writeTrialPlan(w, "hard")
		return
	}

	ctx := s.ctx()

	targetDate, ok := parseDateParam(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "invalid date, expected YYYY-MM-DD",
		})
		return
	}

	minAcc := parseFloatParam(r, "min_accuracy", review.DefaultHardMinAccuracy)
	minRev := intParam(r, "min_reviews", review.DefaultHardMinReviews)

	arc, err := st.LoadArchiveForRead(ctx, s.lang) // 新账号没档案 → 空池子，回 0 个钉子户
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	plan := plan.BuildHardPlan(arc, s.lang, targetDate, minAcc, minRev)
	// 未来日期不写：避免污染 hard plan 之外，也避免无谓的写
	if !targetDate.After(time.Now()) {
		if err := st.UploadHardPlan(ctx, plan); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": fmt.Sprintf("upload hard plan: %v", err),
			})
			return
		}
	}

	s.enrichWordMeta(ctx, st, plan.Words)

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

// 每天造句的构成：N 句课本原文（句库轮转）+ M 句回炉变形句。
//
// 回炉句是 2026-10-09 老师定的：档案里有一批正确率≥80%、但 30 天以上没复习
// 的词，按艾宾浩斯间隔还没到期，实际已经忘了。加进单词复习等于重背词表（不要），
// 所以走造句——拿原型句换掉一个词，助词序列不动，写整句时自然要写出那个词。
// 详见 sentence_review.go 的头部注释。
//
// 配额 2026-10-13 起不再是硬编码：默认 20 + 20，可在 COS 的
// configs/plan_config.json 里改，60 秒内生效，不用重新编译部署。
// 改它的命令：jrp --lang ja plan-config --set sentence_review_count=24
// 读取与兜底逻辑见 plan_config.go（读不到 / 配坏了 → 内置默认 20+20）。
//
// 值为什么是 20：句库 290 条，按原来的 12 句/天要 24 天才轮完一遍，
// 老师嫌慢（实际练过的只有 37 个词）；20 句/天 → 约 15 天一轮。

// buildSentencePlan 从句库挑当天的造句。upload 为 false 时只读（明日预告场景）。
//
// arc 用来过滤回炉句：锚点词最近复习过（闲置 < 30 天）就跳过，不占名额。
// 传 nil 时不做这层过滤（宁可多练）。
//
// 「当天锁定」是这里最重要的一条：只要 COS 上已经有这天的造句 plan，就原样返回，
// 不再重算出题。否则老师写到一半刷新（或换台设备打开）题就变了，
// 草稿对不上、白写一场。换新一批的唯一途径是回写成功 —— 见 api_record.go 的
// DeleteSentencePlan。
func (s *server) buildSentencePlan(st *Storage, targetDate time.Time, upload bool, arc *Archive) (*ReviewPlan, error) {
	ctx := s.ctx()

	if upload {
		if old, err := st.DownloadSentencePlan(ctx, targetDate.Format("2006-01-02")); err == nil &&
			plan.SentencePlanLocked(old, targetDate.Format("2006-01-02")) {
			return old, nil
		}
	}

	res, err := plan.ComposeSentences(ctx, st, sentenceMeta{st}, plan.Params{
		Today: targetDate,
		Arc:   arc,
		// BaseCount 留 0：网页端基础句数跟配置 SentenceBaseCount 走。
	})
	if err != nil {
		var le *plan.LoadError
		if errors.As(err, &le) && le.Stage == plan.StageBank {
			return nil, fmt.Errorf("读取句库失败（还没建库？）：%w", le.Err)
		}
		return nil, err
	}

	rp := &ReviewPlan{
		Date:      targetDate.Format("2006-01-02"),
		Language:  s.lang,
		Kind:      "sentences",
		Sentences: res.Sentences,
	}

	if upload {
		if err := st.UploadSentencePlan(ctx, rp); err != nil {
			return nil, fmt.Errorf("上传造句 plan 失败：%w", err)
		}
	}
	return rp, nil
}

// SentencePlanLocked 已迁 internal/plan（P2）。

// enrichWordMeta 把词性 / 备注 / 圆级 join 到计划里的每个词上。
//
// 这三样存在独立的 word_meta.json 里（不在档案表格中），按完整词形
// 「假名(汉字)」匹配。读不到就整批留空 —— 纯展示增强，绝不能因为词性表
// 挂掉或还没上传，就把今天的练习题给弄没了。
func (s *server) enrichWordMeta(ctx context.Context, st *Storage, planWords []PlanWord) {
	if len(planWords) == 0 {
		return
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil || meta == nil {
		return
	}
	for i := range planWords {
		planWords[i].Pos, planWords[i].Sub, planWords[i].Note, planWords[i].Circle =
			words.LookupWordMeta(meta, planWords[i].Word)
	}
}

// enrichReviewMeta 给回看快照 join 词性 / 备注 —— 跟 enrichWordMeta 同一份
// word_meta.json，只是对象不同（快照里存的是 ReviewItem）。
// 快照可能是 nil（那天没练），直接返回。
func (s *server) enrichReviewMeta(ctx context.Context, st *Storage, snap *ReviewSnapshot) {
	if snap == nil || len(snap.Items) == 0 {
		return
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil || meta == nil {
		return
	}
	for i := range snap.Items {
		snap.Items[i].Pos, snap.Items[i].Sub, snap.Items[i].Note, snap.Items[i].Circle =
			words.LookupWordMeta(meta, snap.Items[i].Word)
	}
}

// CountByStatus 已迁 internal/plan（P2）。

// ==========================================================================
// 原 api_grade.go
// ==========================================================================

// handleGrade 判分接口（给小程序用）。
//
//	POST /api/grade
//	{ "mode":"either", "items":[{"word":"すし(寿司)","answer":"寿司"}] }
//	→ { "success":true, "mode":"either", "results":[true] }
//
// web 端在浏览器里用 web/grade.js 本地判；小程序没法复用那份代码，
// 与其在小程序里再抄一份（两份实现迟早漂移），不如让它调这里 ——
// 判分口径复用 Go 的 grading.GradeAnswer，全站只有一份实现。
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

	mode := grading.ParseGradeMode(input.Mode)
	results := make([]bool, len(input.Items))
	for i, it := range input.Items {
		// 空答案不可能是对的，别白跑一遍判分（也避免空串被当合法写法）
		if it.Answer == "" || it.Word == "" {
			results[i] = false
			continue
		}
		results[i] = grading.GradeAnswer(it.Answer, it.Word, mode)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"mode":    string(mode),
		"results": results,
		"dry_run": s.dryRun,
	})
}

// ==========================================================================
// 原 api_record.go
// ==========================================================================

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
func (s *server) handleRecord(w http.ResponseWriter, r *http.Request, st *Storage) {
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
	// ⚠️ PlanDate 会拼进 COS key（快照 / 造句计划），必须严格校验。
	planDate, ok := safeDate(input.PlanDate)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "plan_date 格式不对，要 YYYY-MM-DD",
		})
		return
	}
	input.PlanDate = planDate

	out := map[string]interface{}{"success": true, "dry_run": s.dryRun}

	// --- 单词 ---
	if len(input.WordResults) > 0 {
		res, err := ApplyRecord(s.ctx(), st, s.lang, input)
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
			if err := st.UploadReview(s.ctx(), snap); err != nil {
				out["review_saved"] = false
				out["review_error"] = err.Error()
			} else {
				out["review_saved"] = true
			}
		}
	}

	// --- 造句 ---
	if len(input.SentenceResults) > 0 {
		hist, err := st.DownloadSentenceHistory(s.ctx())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "读取造句历史失败：" + err.Error(),
			})
			return
		}
		wrong, err := st.DownloadSentenceWrong(s.ctx())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "读取错句失败：" + err.Error(),
			})
			return
		}
		wrong, hist = sentence.ApplySentenceState(wrong, hist, input.SentenceResults, input.PlanDate)
		if err := st.UploadSentenceWrong(s.ctx(), wrong); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "写错句失败：" + err.Error(),
			})
			return
		}
		if err := st.UploadSentenceHistory(s.ctx(), hist); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "写造句历史失败：" + err.Error(),
			})
			return
		}
		// 造句换题时机是「全部提交之后」，不是「提交之后」：
		// 部分提交时把没交的几句留在当天 plan 里，刷新页面还是这批（草稿回填答案），
		// 老师当天分几次写都行；全部交完才删 plan，下一次 GET 才出全新的一批。
		// 2026-10-08 之前是只要回写就删 plan，部分提交后刷新，没写的句子直接没了。
		remaining := 0
		sentPlan, perr := st.DownloadSentencePlan(s.ctx(), input.PlanDate)
		if perr == nil && sentPlan != nil {
			remaining = plan.PruneSentencePlan(sentPlan, input.SentenceResults)
			if remaining > 0 {
				// 写失败不报错 —— 最坏结果是下一批还是这几句，不会丢数据。
				_ = st.UploadSentencePlan(s.ctx(), sentPlan)
			} else {
				_ = st.DeleteSentencePlan(s.ctx(), input.PlanDate)
			}
		} else {
			// 读不到 plan（本就不存在/网络问题）→ 维持旧行为，删掉算了。
			_ = st.DeleteSentencePlan(s.ctx(), input.PlanDate)
		}

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
			"remaining":       remaining,      // 没交的句数（>0 = 刷新后还在）
			"next_on_refresh": remaining == 0, // 全部交完才会换下一批
		}
	}

	// --- 明日预告 ---
	out["tomorrow"] = s.tomorrowPreview(st)

	writeJSON(w, http.StatusOK, out)
}

// tomorrowPreview 算明天到期多少词。只读，不写任何 COS 对象。
func (s *server) tomorrowPreview(st *Storage) map[string]interface{} {
	ctx := s.ctx()
	tomorrow := time.Now().AddDate(0, 0, 1)

	data, _, err := st.DownloadLatestArchive(ctx)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	arc, err := archive.ParseArchive(string(data), s.lang)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	previewPlan := plan.BuildDuePlan(arc, s.lang, tomorrow, words.HoldSet(dueHold(ctx, st)))
	return map[string]interface{}{
		"date":      tomorrow.Format("2006-01-02"),
		"due_count": len(previewPlan.Words),
		"by_status": plan.CountByStatus(previewPlan.Words),
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

// PruneSentencePlan 已迁 internal/plan（P2）。

// ==========================================================================
// 原 api_review.go
// ==========================================================================

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

// ==========================================================================
// 原 api_draft.go
// ==========================================================================

// handleDraft —— 未回写练习的草稿存取。
//
//	GET    /api/draft?mode=words&date=2026-09-20  → { success, draft: {...} | null }
//	POST   /api/draft   { date, mode, grade_mode, items:[{number,answer,unknown,manual}] }
//	DELETE /api/draft?mode=words&date=2026-09-20  → { success }
//
// 存在的意义：写一半关掉标签页，换来的是「119 个词要重写一遍」。
// 草稿按「日期 + 模式」分键，回写成功后立即清除。
func (s *server) handleDraft(w http.ResponseWriter, r *http.Request, st *Storage) {
	// 体验版：草稿是落档的东西，试用不要 —— 直接回空，不碰 COS。
	if st.uid == trialUID {
		writeJSON(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}

	// ⚠️ date 会拼进 COS key，必须严格校验（不是 YYYY-MM-DD 直接 400）。
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
	if !validDraftMode(mode) {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "未知草稿模式：" + mode,
		})
		return
	}

	switch r.Method {
	case http.MethodGet:
		d, err := st.DownloadDraft(s.ctx(), date, mode)
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
		// body 里的 date 同样拼进 COS key，一样要校验。
		bodyDate, ok := safeDate(d.Date)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false, "error": "日期格式不对，要 YYYY-MM-DD",
			})
			return
		}
		d.Date = bodyDate
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
			_ = st.DeleteDraft(s.ctx(), d.Date, d.Mode)
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"success": true, "saved": false, "dry_run": s.dryRun,
			})
			return
		}
		if err := st.UploadDraft(s.ctx(), &d); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "saved": true, "saved_at": d.SavedAt, "dry_run": s.dryRun,
		})

	case http.MethodDelete:
		if err := st.DeleteDraft(s.ctx(), date, mode); err != nil {
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

// ==========================================================================
// 原 api_stats.go
// ==========================================================================

// handleStats 学习进度统计，复用 CLI 的 ComputeStats。
//
//	GET /api/stats?days=30
func (s *server) handleStats(w http.ResponseWriter, r *http.Request, st *Storage) {
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}

	// 体验版：内置假数据，一次都不碰 COS（真路径会去读档案和快照）。
	if st.uid == trialUID {
		writeTrialStats(w, days)
		return
	}

	res, err := ComputeStats(s.ctx(), st, s.lang, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	// 听力进度：塞进 jlpt 段，前端在 JLPT 进度卡里按等级画进度条。
	// 目标值从 COS 配置读（60 秒缓存），没配用内置默认（N5=1500 / N4=3000 分钟）。
	// 非日语 / 没听过都是空数组，前端按「没有听力数据」处理。
	if res.Jlpt != nil {
		goal, _ := resolveListeningGoal(s.ctx(), st)
		res.Jlpt.Listening = sortedListeningLevels(listeningTotals(s.ctx(), st), goal)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"language":  s.lang,
		"days":      days,
		"snapshots": res.Snapshots,
		"changes":   res.Changes,
		"detail":    res.Detail,
		"jlpt":      res.Jlpt,
	})
}

// ==========================================================================
// 原 api_offline.go
// ==========================================================================

// 离线闭环：导出 Excel 拿去离线练，练完把填好的文件传回来自动解析。

// handleExport 导出练习 Excel。
//
//	GET /api/export?mode=daily|hard|sentences&date=YYYY-MM-DD&grade=kana|kanji|full|either
//
// 文件里会藏一个 _meta sheet 记录 plan_date / language / mode，回填时靠它定位
// 这是哪天的练习（离线可能隔天才回填，不能以上传当天为准）。
func (s *server) handleExport(w http.ResponseWriter, r *http.Request, st *Storage) {
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
		mode = "daily"
	}
	dateStr := targetDate.Format("2006-01-02")

	data, _, err := st.DownloadLatestArchive(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("download archive: %v", err),
		})
		return
	}
	arc, err := archive.ParseArchive(string(data), s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("parse archive: %v", err),
		})
		return
	}

	var (
		filename string
		genErr   error
	)
	tmp, err := os.CreateTemp("", "jrp-export-*.xlsx")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	switch mode {
	case "hard":
		plan := plan.BuildHardPlan(arc, s.lang, targetDate, review.DefaultHardMinAccuracy, review.DefaultHardMinReviews)
		filename = fmt.Sprintf("hard_words_%s.xlsx", dateStr)
		genErr = offline.GenerateHardExcelWithMeta(plan, tmp.Name(), &offline.ExcelMeta{
			PlanDate: dateStr, Language: s.lang, Mode: "hard", Kind: "hard",
		})

	case "sentences":
		sp, err := s.buildSentencePlan(st, targetDate, false, arc)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		sp.Sentences = BuildSentencePlanFrom(st, targetDate, 50, arc)
		if sp.Sentences == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "句库读取失败",
			})
			return
		}
		filename = fmt.Sprintf("review_%s_sentences.xlsx", dateStr)
		genErr = offline.GenerateExcelWithMeta(sp, tmp.Name(), &offline.ExcelMeta{
			PlanDate: dateStr, Language: s.lang, Mode: "sentences", Kind: "sentences",
		})

	default: // daily：到期词 + 20 句造句
		plan := plan.BuildDuePlan(arc, s.lang, targetDate, words.HoldSet(dueHold(ctx, st)))
		plan.Sentences = BuildSentencePlanFrom(st, targetDate, 20, arc)
		filename = fmt.Sprintf("review_%s.xlsx", dateStr)
		genErr = offline.GenerateExcelWithMeta(plan, tmp.Name(), &offline.ExcelMeta{
			PlanDate: dateStr, Language: s.lang, Mode: "daily",
		})
	}
	if genErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "生成 Excel 失败：" + genErr.Error(),
		})
		return
	}

	body, err := os.ReadFile(tmp.Name())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
			filename, urlEscape(filename)))
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// handleImport 解析老师填好的离线 Excel，**只返回预览，不落库**。
//
//	POST /api/import  (multipart, field: file)
//
// 不落库是有意的：长句手写的自动判分不可靠，必须让老师在页面上过一遍再提交。
// 所以这里返回「你写的 / 正确答案 / 自动判定」，前端渲染成可勾选的表，
// 确认后再调 /api/record。省掉服务端会话状态。
func (s *server) handleImport(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "解析上传失败：" + err.Error(),
		})
		return
	}
	fh, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "缺少 file 字段：" + err.Error(),
		})
		return
	}
	defer fh.Close()

	if filepath.Ext(hdr.Filename) != ".xlsx" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "只支持 .xlsx（请上传从网站导出的那个文件）",
		})
		return
	}

	tmp, err := os.CreateTemp("", "jrp-import-*.xlsx")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	if err := saveMultipartFile(fh, tmp.Name()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "保存上传文件失败：" + err.Error(),
		})
		return
	}

	prev, err := offline.ParseExerciseFile(tmp.Name(), grading.ParseGradeMode(r.URL.Query().Get("grade")))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	if prev.PlanDate == "" {
		prev.PlanDate = time.Now().Format("2006-01-02")
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":        true,
		"filename":       hdr.Filename,
		"plan_date":      prev.PlanDate,
		"language":       prev.Language,
		"mode":           prev.Mode,
		"grade_mode":     prev.GradeMode,
		"word_count":     len(prev.Words),
		"sentence_count": len(prev.Sentences),
		"skipped_blank":  prev.SkippedBlank,
		"words":          prev.Words,
		"sentences":      prev.Sentences,
	})
}

// BuildSentencePlanFrom 从 COS 句库挑 n 句，再追加回炉变形句。纯读，不写 history。
//
// 离线导出 Excel 走这条路，所以它也必须带加餐 —— 老师在飞机上/离线时练的那份
// 要是少了回炉句，等于白加这个功能。
func BuildSentencePlanFrom(st *Storage, today time.Time, n int, arc *Archive) []PlanSentence {
	ctx := bgctx()
	// 回炉配额跟着网页端走同一份配置 —— 离线 Excel 少了回炉句等于白加这个功能。
	res, err := plan.ComposeSentences(ctx, st, sentenceMeta{st}, plan.Params{
		Today:     today,
		Arc:       arc,
		BaseCount: n,
	})
	if err != nil {
		return nil
	}
	return res.Sentences
}

// saveMultipartFile 把上传的文件落到磁盘。
func saveMultipartFile(src multipart.File, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, src)
	return err
}

func urlEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			out = append(out, c)
			continue
		}
		out = append(out, '%')
		out = append(out, "0123456789ABCDEF"[c>>4])
		out = append(out, "0123456789ABCDEF"[c&0xF])
	}
	return string(out)
}
