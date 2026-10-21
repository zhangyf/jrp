package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// 拍照取词：把图片直接丢给视觉大模型，让它吐「词形 + 中文意思」。
//
// 2026-10-21 从腾讯云 OCR 改成视觉模型（老师一句「我不是配了大模型吗」问得对）：
// OCR 只能把图里的字一行行抄出来，抄完还得写正则去猜哪半截是词、哪半截是中文，
// 切不准就把好词改坏；视觉模型看得懂版面 —— 这是生词表、那是例句别管 ——
// 直接给结构化结果，正则那层整个不用了。实测课本生词表截图一次就认对，
// 还自动跳过了例句和标题。
//
// ⚠️ 网关走 JRP_LLM_ENDPOINT / JRP_LLM_API_KEY（跟判句、查释义同一个），
// 但**模型名必须单独配 JRP_VISION_MODEL** —— 视觉和文本是两回事。
// 现在配的主模型 deepseek-flash / glm-5.3-flash 是**纯文本**，吃不了图片。

const (
	visionDefaultModel    = "deepseek/deepseek-v4-flash-vision-exp"
	visionDefaultFallback = "glm-5v-turbo"
	visionMaxImages       = 9 // 微信 chooseMedia 一次最多也是 9 张
	visionMaxImageBytes   = 4 << 20
	visionConcurrency     = 3
	visionDefaultTimeout  = 25 * time.Second
)

// VisionCandidate 是从图片里认出来的一个候选词。
type VisionCandidate struct {
	Word string `json:"word"`
	Def  string `json:"def"`
}

type visionConfig struct {
	Endpoint string
	APIKey   string
	Model    string
	Fallback []string
	Timeout  time.Duration
}

// loadVisionConfig 读视觉模型配置。网关或密钥没配就是不可用。
func loadVisionConfig() (visionConfig, bool) {
	c := visionConfig{
		Endpoint: strings.TrimSpace(os.Getenv("JRP_LLM_ENDPOINT")),
		APIKey:   strings.TrimSpace(os.Getenv("JRP_LLM_API_KEY")),
		Model:    strings.TrimSpace(os.Getenv("JRP_VISION_MODEL")),
		Timeout:  visionDefaultTimeout,
	}
	if c.Model == "" {
		c.Model = visionDefaultModel
	}
	fb := strings.TrimSpace(os.Getenv("JRP_VISION_FALLBACK"))
	if fb == "" {
		fb = visionDefaultFallback
	}
	c.Fallback = parseFallbackModels(fb, c.Model)
	if v := strings.TrimSpace(os.Getenv("JRP_VISION_TIMEOUT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 120 {
			c.Timeout = time.Duration(n) * time.Second
		}
	}
	return c, c.Endpoint != "" && c.APIKey != ""
}

// visionPrompt 认词用的提示词。
//
// 「只输出 JSON 数组」这句不能省 —— 省了模型就开始给你讲解它看到了什么，
// 解析直接拿不到东西。第 1 条（例句/标题不要）是实测出来的：不给这条，
// 它会把「これは わたしの ほんです。」这种课文句子也当词收进来。
const visionPrompt = `你是日语老师。图片是日语课本的一页（可能是生词表）。
请提取其中的日语单词词条和对应的中文意思。

要求：
1. 只提取词条。例句、课文正文、标题、页码、序号都不要。
2. word 保留图片里的写法（包括括号），def 给简明中文意思；图片里没有中文就留空字符串。
3. 多个义项用顿号并列。
4. 同一个词出现多次只给一条。

只输出 JSON 数组，不要解释，不要代码块：[{"word": "みず(水)", "def": "水"}]`

// parseVisionReply 从模型回话里抠出 JSON 数组。
// 模型爱在外面套 ```json、给成对象包数组、或者前后加废话，三种都得接住。
func parseVisionReply(reply string) ([]VisionCandidate, error) {
	s := stripCodeFence(reply)

	if l, r := strings.Index(s, "["), strings.LastIndex(s, "]"); l >= 0 && r > l {
		var out []VisionCandidate
		if err := json.Unmarshal([]byte(s[l:r+1]), &out); err == nil && len(out) > 0 {
			return cleanVisionCandidates(out), nil
		}
	}
	// 兜一层：模型给成 {"words":[...]} / {"items":[...]}
	var wrapped struct {
		Words []VisionCandidate `json:"words"`
		Items []VisionCandidate `json:"items"`
	}
	if l, r := strings.Index(s, "{"), strings.LastIndex(s, "}"); l >= 0 && r > l {
		if err := json.Unmarshal([]byte(s[l:r+1]), &wrapped); err == nil {
			got := wrapped.Words
			if len(got) == 0 {
				got = wrapped.Items
			}
			if len(got) > 0 {
				return cleanVisionCandidates(got), nil
			}
		}
	}
	return nil, errors.New("模型没按 JSON 数组回")
}

// stripCodeFence 去掉 ```json ... ``` 外壳。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "```")
	if i < 0 {
		return s
	}
	s = s[i+3:]
	if j := strings.Index(s, "```"); j >= 0 {
		s = s[:j]
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "json") {
		s = strings.TrimSpace(s[4:])
	}
	return s
}

// hasJapanese 判断字符串里有没有日文（假名或汉字）—— 滤掉模型偶尔夹带的中文标题之类。
func hasJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

// cleanVisionCandidates 去空、去噪、去重。
func cleanVisionCandidates(in []VisionCandidate) []VisionCandidate {
	out := make([]VisionCandidate, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		w := strings.TrimSpace(c.Word)
		if w == "" || !hasJapanese(w) {
			continue
		}
		if len([]rune(w)) > 24 { // 整句话，不是词条
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, VisionCandidate{Word: w, Def: strings.TrimSpace(c.Def)})
	}
	return out
}

type visionResult struct {
	idx   int
	cands []VisionCandidate
	model string
	err   error
}

// visionChat 带图问模型一次，按「主 → 备」顺序试，返回原文和生效模型名。
func visionChat(cfg visionConfig, prompt, imageURL string) (string, string, error) {
	order := append([]string{cfg.Model}, cfg.Fallback...)
	var lastErr error
	tried := 0
	for _, m := range order {
		now := time.Now()
		if llmSkipModel(m, now) {
			continue
		}
		tried++
		body, err := json.Marshal(map[string]interface{}{
			"model":       m,
			"temperature": 0,
			"stream":      false,
			"messages": []map[string]interface{}{
				{"role": "user", "content": []map[string]interface{}{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]string{"url": imageURL}},
				}},
			},
		})
		if err != nil {
			lastErr = err
			continue
		}
		reply, err := postLLM(cfg.Endpoint, cfg.APIKey, body, cfg.Timeout)
		if err == nil && strings.TrimSpace(reply) != "" {
			llmMarkResult(m, true, now)
			return strings.TrimSpace(reply), m, nil
		}
		lastErr = err
		llmMarkResult(m, false, now)
		log.Printf("vision: 模型 %s 失败：%v", m, err)
	}
	if tried == 0 {
		return "", "", errors.New("所有视觉模型都在冷却中")
	}
	if lastErr == nil {
		lastErr = errors.New("模型返回空内容")
	}
	return "", "", lastErr
}

// handleVision —— 拍照取词。
//
//	POST /api/vision { images:[base64...] } → { success, enabled, candidates:[…] }
//
// 只做「认词」，不写档案 —— 老师勾完要哪些再走 POST /api/words。
// 多张图并发问（最多 3 个并发），某张失败单独报出来，不影响其它张。
//
// ⚠️ 网关没配时返回 enabled:false + enabled_note，不报错：小程序把入口提示成人话，
// 而不是弹一个看不懂的 500。
func (s *server) handleVision(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	cfg, ok := loadVisionConfig()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success":      false,
			"enabled":      false,
			"error":        "服务器没配大模型网关，拍照取词用不了",
			"enabled_note": "在 /etc/jrp/jrp.env 里配 JRP_LLM_ENDPOINT / JRP_LLM_API_KEY，再 systemctl restart jrp",
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
	if len(body.Images) > visionMaxImages {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "一次最多 9 张图",
		})
		return
	}

	jobs := make(chan int)
	results := make(chan visionResult, len(body.Images))
	var wg sync.WaitGroup

	workers := visionConcurrency
	if len(body.Images) < workers {
		workers = len(body.Images)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results <- askVision(cfg, i, body.Images[i])
			}
		}()
	}
	go func() {
		for i := range body.Images {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var all []VisionCandidate
	var failed []string
	model := ""
	seen := map[string]bool{}
	for res := range results {
		if res.err != nil {
			failed = append(failed, "第"+strconv.Itoa(res.idx+1)+"张："+res.err.Error())
			continue
		}
		if model == "" {
			model = res.model
		}
		for _, c := range res.cands {
			if seen[c.Word] {
				continue
			}
			seen[c.Word] = true
			all = append(all, c)
		}
	}

	out := map[string]interface{}{
		"success":    true,
		"enabled":    true,
		"candidates": all,
	}
	if model != "" {
		out["model"] = model
	}
	if len(failed) > 0 {
		out["failed"] = failed
	}
	writeJSON(w, http.StatusOK, out)
}

// askVision 处理一张图：拼 data URL → 问模型 → 解析成候选。
func askVision(cfg visionConfig, idx int, img string) visionResult {
	res := visionResult{idx: idx}
	img = strings.TrimSpace(img)
	if i := strings.Index(img, ","); i >= 0 && strings.HasPrefix(img, "data:") {
		img = img[i+1:]
	}
	// 粗略估一下解码后的字节数（base64 每 4 字符 3 字节）
	if len(img)*3/4 > visionMaxImageBytes {
		res.err = errors.New("图太大，压缩后再试")
		return res
	}

	reply, model, err := visionChat(cfg, visionPrompt, "data:image/jpeg;base64,"+img)
	if err != nil {
		res.err = err
		return res
	}
	cands, err := parseVisionReply(reply)
	if err != nil {
		res.err = err
		return res
	}
	res.cands = cands
	res.model = model
	return res
}
