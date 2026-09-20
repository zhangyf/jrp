package main

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// 离线闭环：导出 Excel 拿去离线练，练完把填好的文件传回来自动解析。

// handleExport 导出练习 Excel。
//
//	GET /api/export?mode=daily|hard|sentences&date=YYYY-MM-DD&grade=kana|kanji|full|either
//
// 文件里会藏一个 _meta sheet 记录 plan_date / language / mode，回填时靠它定位
// 这是哪天的练习（离线可能隔天才回填，不能以上传当天为准）。
func (s *server) handleExport(w http.ResponseWriter, r *http.Request) {
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
		plan := BuildHardPlan(arc, s.lang, targetDate, DefaultHardMinAccuracy, DefaultHardMinReviews)
		filename = fmt.Sprintf("hard_words_%s.xlsx", dateStr)
		genErr = GenerateHardExcelWithMeta(plan, tmp.Name(), &ExcelMeta{
			PlanDate: dateStr, Language: s.lang, Mode: "hard", Kind: "hard",
		})

	case "sentences":
		sp, err := s.buildSentencePlan(targetDate, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		sp.Sentences = BuildSentencePlanFrom(s.storage, targetDate, 50)
		if sp.Sentences == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false, "error": "句库读取失败",
			})
			return
		}
		filename = fmt.Sprintf("review_%s_sentences.xlsx", dateStr)
		genErr = GenerateExcelWithMeta(sp, tmp.Name(), &ExcelMeta{
			PlanDate: dateStr, Language: s.lang, Mode: "sentences", Kind: "sentences",
		})

	default: // daily：到期词 + 20 句造句
		plan := BuildDuePlan(arc, s.lang, targetDate)
		plan.Sentences = BuildSentencePlanFrom(s.storage, targetDate, 20)
		filename = fmt.Sprintf("review_%s.xlsx", dateStr)
		genErr = GenerateExcelWithMeta(plan, tmp.Name(), &ExcelMeta{
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
func (s *server) handleImport(w http.ResponseWriter, r *http.Request) {
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

	prev, err := ParseExerciseFile(tmp.Name(), ParseGradeMode(r.URL.Query().Get("grade")))
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

// BuildSentencePlanFrom 从 COS 句库挑 n 句。纯读，不写 history。
func BuildSentencePlanFrom(st *Storage, today time.Time, n int) []PlanSentence {
	ctx := bgctx()
	bank, err := st.DownloadSentenceBank(ctx)
	if err != nil {
		return nil
	}
	hist, err := st.DownloadSentenceHistory(ctx)
	if err != nil {
		return nil
	}
	wrong, err := st.DownloadSentenceWrong(ctx)
	if err != nil {
		return nil
	}
	return BuildSentencePlan(bank, hist, wrong, today, n)
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
