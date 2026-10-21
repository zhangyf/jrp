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

// 模型链（2026-10-21 在服务器上带图实测两轮，跑同一张课本生词表截图）：
//
//	deepseek/deepseek-v4-flash-vision-exp  2.2~2.5s   干净 JSON   —— 主
//	hy-vision-2.0-instruct                 2.1~5.1s   干净 JSON   —— 备
//
// 四条实测结论，别再凭感觉改：
//  1. **链上的模型必须确认真的存在**。原来备胎写的是 glm-5v-turbo，2026-10-21 线上日志里
//     它回的是 402「service ID does not exist」—— 主模型一超时，备用根本接不住，
//     老师传 4 张只回来 1 张就是这么来的。
//  2. **链上不要放推理型视觉模型**。hunyuan-t1-vision-20250916 实测两次：13.1s 和 50.5s，
//     30 秒超时下第二档必挂，白等半分钟还拿不到东西。
//  3. **限流不是这个网关的瓶颈**：并发 2 连打 4 张同一张图，5.2 秒全过。
//     真正让老师那 4 张挂掉的是**图太大**——手机原图两三兆，25 秒根本算不完。
//     所以并发降到 2 是为了给大图留余量，不是为了躲限流。
//  4. **模型名必须带厂商前缀**：`deepseek-v4-flash-vision-exp`（不带 deepseek/）直接 400。
const (
	visionDefaultModel    = "deepseek/deepseek-v4-flash-vision-exp"
	visionDefaultFallback = "hy-vision-2.0-instruct"
	visionMaxImages       = 9 // 微信 chooseMedia 一次最多也是 9 张
	visionMaxImageBytes   = 4 << 20
	visionConcurrency     = 2 // 并发打太狠会被网关限流，宁可慢一点也要每张都有结果
	visionDefaultTimeout  = 30 * time.Second
	visionRetry           = 1 // 单张失败后再整链重试一次
)

// VisionCandidate 是从图片里认出来的一个候选词。
type VisionCandidate struct {
	Word string `json:"word"`
	Def  string `json:"def"`
}

// VisionImageResult 是**一张图**的认词结果。
//
// 按图返回（而不是只给一个扁平列表）是老师 2026-10-21 问出来的：
// 「既然是拍照取词，那就意味着有些词我是不认识的，你不把原始照片给我展示出来，
//
//	我怎么知道你识别出来的单词是准确的还是不准确的呢。」
//
// —— 所以前端按图分块，每块带上原图缩略图，老师对着照片核哪个词认错了。
type VisionImageResult struct {
	Index      int               `json:"index"`
	OK         bool              `json:"ok"`
	Error      string            `json:"error,omitempty"`
	Model      string            `json:"model,omitempty"`
	Candidates []VisionCandidate `json:"candidates"`
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

// errVisionTooLarge 单独拎出来，visionHumanError 才能给它一句人话。
var errVisionTooLarge = errors.New("图太大，压缩后再试")

// visionChat 带图问模型一次，按「主 → 备」顺序试，返回原文、生效模型名、是否值得重试。
//
// 第三个返回值不能省：链上最后一个模型报 402（没额度）不代表前面那个超时也该认命，
// 只拿 lastErr 判断会把「三个都超时、最后一个恰好 402」误判成不用重试。
func visionChat(cfg visionConfig, prompt, imageURL string) (string, string, bool, error) {
	order := append([]string{cfg.Model}, cfg.Fallback...)
	var lastErr error
	retryable := false
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
			return strings.TrimSpace(reply), m, false, nil
		}
		lastErr = err
		if visionRetryable(err) {
			retryable = true
		}
		llmMarkResult(m, false, now)
		log.Printf("vision: 模型 %s 失败：%v", m, err)
	}
	if tried == 0 {
		return "", "", false, errors.New("所有视觉模型都在冷却中")
	}
	if lastErr == nil {
		lastErr = errors.New("模型返回空内容")
	}
	return "", "", retryable, lastErr
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

	// results 是并发回来的，顺序不定 —— 按 idx 归位，前端才能跟它自己发的图片顺序对上
	per := make([]VisionImageResult, len(body.Images))
	for res := range results {
		per[res.idx] = VisionImageResult{
			Index:      res.idx,
			OK:         res.err == nil,
			Error:      visionHumanError(res.err),
			Model:      res.model,
			Candidates: res.cands,
		}
	}

	// 认完统一补中文 + 补汉字。
	//
	// 老师 2026-10-21 反馈「还没有中文释义」：课本照片上常常只有假名没有中文，
	// 视觉模型吐出来的 def 就是空的。补中文这件事不该等老师勾完再点一次按钮 ——
	// 认出来就该带着中文一起显示，老师才能当场判断这个词认得对不对。
	// 走的还是 lookupWordDefs（先档案后模型），跟手打录入是同一份口径。
	var need []string
	for i := range per {
		for _, c := range per[i].Candidates {
			if c.Def == "" || !hasParen(c.Word) {
				need = append(need, c.Word)
			}
		}
	}
	defModel := ""
	if len(need) > 0 {
		got, m := s.lookupWordDefs(st, need)
		defModel = m
		byInput := map[string]WordDefItem{}
		for _, it := range got {
			byInput[it.Input] = it
		}
		for i := range per {
			for ci := range per[i].Candidates {
				c := &per[i].Candidates[ci]
				it, ok := byInput[c.Word]
				if !ok {
					continue
				}
				if c.Def == "" {
					c.Def = it.Def
				}
				// 本来只有假名的，用补了汉字的词形（みず → みず(水)）
				if !hasParen(c.Word) && it.Word != "" {
					c.Word = it.Word
				}
			}
		}
	}

	// 扁平去重版，给不关心分组的调用方（以及以后可能的 web 端）
	var all []VisionCandidate
	var failed []string
	model := ""
	seen := map[string]bool{}
	for i := range per {
		if !per[i].OK {
			failed = append(failed, "第"+strconv.Itoa(i+1)+"张："+per[i].Error)
			continue
		}
		if model == "" {
			model = per[i].Model
		}
		for _, c := range per[i].Candidates {
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
		"images":     per,
		"candidates": all,
	}
	if model != "" {
		out["model"] = model
	}
	if defModel != "" {
		out["def_model"] = defModel
	}
	if len(failed) > 0 {
		out["failed"] = failed
	}
	writeJSON(w, http.StatusOK, out)
}

// askVision 处理一张图：拼 data URL → 问模型 → 解析成候选。失败整链重试一次。
func askVision(cfg visionConfig, idx int, img string) visionResult {
	res := visionResult{idx: idx}
	img = strings.TrimSpace(img)
	if i := strings.Index(img, ","); i >= 0 && strings.HasPrefix(img, "data:") {
		img = img[i+1:]
	}
	// 粗略估一下解码后的字节数（base64 每 4 字符 3 字节）
	if len(img)*3/4 > visionMaxImageBytes {
		res.err = errVisionTooLarge
		return res
	}

	for attempt := 0; attempt <= visionRetry; attempt++ {
		reply, model, retryable, err := visionChat(cfg, visionPrompt, "data:image/jpeg;base64,"+img)
		if err != nil {
			res.err = err
			if attempt == visionRetry || !retryable {
				break
			}
			// 退避一下再试：限流/抖动这类，立刻重试还是撞同一堵墙
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		cands, perr := parseVisionReply(reply)
		if perr != nil {
			res.err = perr
			if attempt == visionRetry {
				break
			}
			continue
		}
		res.cands = cands
		res.model = model
		res.err = nil
		return res
	}
	return res
}

// visionRetryable 这个错误值不值得再试一次。
//
// 402（这个模型没额度）/ 400（模型名不对）再试一百次也是一样的结果，
// 白白让老师多等几十秒；超时、5xx、网络错才是抖出来的，值得重试。
func visionRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, bad := range []string{"402", "400", "401", "does not exist", "图太大"} {
		if strings.Contains(msg, bad) {
			return false
		}
	}
	return true
}

// visionHumanError 把模型网关那串英文翻成人话。
//
// 网关的原始报错长这样，直接甩给老师等于没说：
//
//	模型返回 402：{"error":{"type":"permission_error","code":"401007",
//	  "message":"No free trial quota is available for the service..."}}
func visionHumanError(err error) string {
	if err == nil {
		return ""
	}
	if err == errVisionTooLarge {
		return "图太大，压缩后再试"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "does not exist"):
		return "模型名不对（已记下，换备用模型）"
	case strings.Contains(msg, "402"), strings.Contains(msg, "401007"):
		return "这个视觉模型没额度了"
	case strings.Contains(msg, "429"):
		return "请求太频繁，歇一下再试"
	case strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "timeout"):
		return "识别超时，图可能太大，重拍一张清楚点的"
	case strings.Contains(msg, "5"):
		if i := strings.Index(msg, "模型返回 "); i >= 0 && len(msg) > i+9 {
			code := msg[i+len("模型返回 ") : i+len("模型返回 ")+3]
			if strings.HasPrefix(code, "5") {
				return "模型服务开小差了，再试一次"
			}
		}
	}
	if len([]rune(msg)) > 40 {
		return string([]rune(msg)[:40]) + "…"
	}
	return msg
}
