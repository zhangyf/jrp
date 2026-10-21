package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// 拍照取词。
//
//	POST /api/ocr { images:[base64...] } → { success, enabled, candidates:[…] }
//
// 只做「认字 + 切成候选」，不写档案 —— 老师勾完要哪些再走 POST /api/words。
//
// ⚠️ 没配 JRP_OCR_SECRET_ID/KEY 时返回 enabled:false + enabled_note，不报错：
// 这样小程序那边能把按钮灰掉并给一句人话，而不是弹一个看不懂的 500。
func (s *server) handleOCR(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	if !ocrEnabled() {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":      false,
			"enabled":      false,
			"error":        "服务器没配腾讯云 OCR 密钥，拍照取词用不了",
			"enabled_note": "在 /etc/jrp/jrp.env 里加 JRP_OCR_SECRET_ID / JRP_OCR_SECRET_KEY，再 systemctl restart jrp",
		})
		return
	}

	var body struct {
		Images []string `json:"images"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "请求格式不对：" + err.Error(),
		})
		return
	}
	if len(body.Images) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "没有图片",
		})
		return
	}
	if len(body.Images) > ocrMaxImages {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "一次最多 9 张图",
		})
		return
	}

	cred := ocrCreds()
	ctx := s.ctx()

	var all []string
	var failed []string
	for i, img := range body.Images {
		// 允许带 data URL 前缀（小程序拿到的有时是 wx.getFileSystemManager 读出的纯 base64，两种都收）
		if idx := strings.Index(img, ","); idx >= 0 && strings.HasPrefix(img, "data:") {
			img = img[idx+1:]
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(img))
		if err != nil {
			failed = append(failed, "第"+strconv.Itoa(i+1)+"张不是合法 base64")
			continue
		}
		if len(raw) > ocrMaxImageBytes {
			failed = append(failed, "第"+strconv.Itoa(i+1)+"张太大（压缩后再试）")
			continue
		}
		lines, err := tencentOCR(ctx, cred, strings.TrimSpace(img))
		if err != nil {
			failed = append(failed, "第"+strconv.Itoa(i+1)+"张识别失败："+err.Error())
			continue
		}
		all = append(all, lines...)
	}

	out := map[string]interface{}{
		"success":   true,
		"enabled":   true,
		"candidates": ocrCandidates(all, 0),
		"lines":     len(all),
	}
	if len(failed) > 0 {
		out["failed"] = failed
	}
	writeJSON(w, http.StatusOK, out)
}

