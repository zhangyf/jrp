// api_llm.go —— 大模型三接口（判句 / 查词义 / 拍照取词，共用 llm_failover.go 冷却）
//
// 腿一 R2：把原本平铺在根目录的 3 个 api_*.go / storage*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；HTTP 响应、存档、Excel 字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找 handler 先看下面的来源清单。
//
// 来源与顶层声明：
// api_judge.go: judgeResult, judgeCaller, Judge, resolveJudgePrompt, judgeWithCache, handleJudgeSentence, handleJudgePrompt
// api_worddef.go: WordDefItem, wordDefCaller, Lookup, lookupWordDefs, handleWordDef
// api_vision.go: VisionCandidate, VisionImageResult, visionConfig, loadVisionConfig, visionResult, visionChat, handleVision, askVision, visionRetryable, visionHumanError
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/judge"
	"github.com/zhangyf/jrp/internal/llm"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/vision"
	"github.com/zhangyf/jrp/internal/worddef"
)

// ==========================================================================
// 原 api_judge.go
// ==========================================================================

// 造句的语义判定：字面比对不一致时，问大模型「这句意思成立吗」。
//
// 设计前提（2026-09-29 老师定）：
//  1. 字面一致永远直接判对，不调模型 —— 0 延迟、0 成本、不依赖外网。
//  2. 只有字面不一致才问模型；模型只判「意思成不成立」，不判语法优劣。
//  3. 模型不可用（没配密钥 / 超时 / 报错）就退回字面判定，练习绝不卡住。
//  4. 判定结果按 (原句, 老师写的) 缓存：刷新、换设备重开都得到同一个结论，
//     不能这次判对下次判错 —— 跟「不提交不换题」是同一个规矩。
//
// 密钥只存在于服务器环境变量，前端拿不到，也不会出现在任何响应里。
//
// 本文件现在只留 HTTP 薄适配（参数解析 → 调用例 → 序列化）与根包适配层：
//   - 纯规则 / 模板缓存 / 判定缓存 / 编排：internal/judge（用例层，不认识 IO）
//   - 主→备切换 / 失败冷却 / 真发 HTTP：llm_failover.go（IO 层，全局共享）
// 两个 handler 的响应 map 逐字未动，HTTP 输出字节不变。

// judgeResult 保留根包类型别名：llm_failover.go 的 judgeWithFallback
// 仍以它为返回类型；字段与 json 行为跟 internal/judge.Result 完全一致。
type judgeResult = judge.Result

// judgeCaller 把根包的判句模型链适配成用例层 Caller。
// judgeWithFallback 内含主→备切换、SkipModel/MarkResult 冷却、llm.Post，
// 是全局共享的 IO 实现（查中文释义的 llmChat 也吃这份冷却状态），不进用例层。
// 模型配置在这里持有，不进 Caller 接口 —— 用例层不认识 llm 包。
type judgeCaller struct{ cfg llm.Config }

func (c judgeCaller) Judge(prompt string) (judge.Result, string, error) {
	return judgeWithFallback(c.cfg, prompt)
}

var (
	_ judge.ConfigStore = (*Storage)(nil)
	_ judge.Caller      = judgeCaller{}
)

// resolveJudgePrompt 薄转发：模板选择 + 60 秒缓存已随用例搬进 internal/judge。
// 保留根名字，让 handleJudgePrompt 沿用。
func resolveJudgePrompt(ctx context.Context, st *Storage) (tpl, version, source string) {
	return judge.ResolvePrompt(ctx, st)
}

// judgeWithCache 薄转发：注入 *Storage（ConfigStore）与 judgeCaller（Caller）。
// 判定缓存、模板版本、fail-open 全在用例层，行为跟原实现逐字一致。
func judgeWithCache(ctx context.Context, st *Storage, cfg llm.Config, answer, chinese, input string) (judgeResult, string, string, error) {
	return judge.JudgeWithCache(ctx, st, judgeCaller{cfg: cfg}, answer, chinese, input)
}

// ---------- HTTP ----------

// POST /api/judge-sentence  { answer, chinese, input }
//
//	→ { enabled:true,  correct, reason, by:"llm"|"literal" }
//	→ { enabled:false }                       没配密钥，前端关掉这个功能
//	→ { enabled:true, correct:false, by:"literal", error }   模型挂了，按字面判
func (s *server) handleJudgeSentence(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "只用 POST",
		})
		return
	}

	cfg, enabled := llm.LoadConfig()
	if !enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": false,
			"hint": "服务端没配 JRP_LLM_ENDPOINT / JRP_LLM_API_KEY，造句按字面判定",
		})
		return
	}

	var in struct {
		Answer  string `json:"answer"`
		Chinese string `json:"chinese"`
		Input   string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "bad body: " + err.Error(),
		})
		return
	}
	in.Answer = strings.TrimSpace(in.Answer)
	in.Input = strings.TrimSpace(in.Input)
	if in.Answer == "" || in.Input == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "answer 和 input 都不能为空",
		})
		return
	}

	// 归一化后一致 —— 前端已经判对了，这里兜一下（换设备、旧前端）
	if sentence.Norm(in.Answer) == sentence.Norm(in.Input) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": true, "by": "literal",
		})
		return
	}

	res, ver, model, err := judgeWithCache(r.Context(), st, cfg, in.Answer, in.Chinese, in.Input)
	if err != nil {
		// 模型不可用不是业务错误：不返回 5xx，让前端退回字面判定继续练
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": false, "by": "literal",
			"reason": "模型不可用，按字面判定",
			"chain":  append([]string{cfg.Model}, cfg.Fallback...),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "enabled": true,
		"correct": res.Correct, "reason": res.Reason, "by": "llm",
		"pv":    ver,   // prompt 版本：存进草稿，改了 prompt 就作废旧结论
		"model": model, // 实际判这句的模型（主模型挂了会显示备用）
	})
}

// GET /api/judge-prompt
//
//	→ { enabled, version, source: "cos"|"builtin" }
//
// 只回版本不回内容：前端拿它判断草稿里的旧结论还能不能用。
// 顺带把「有没有配模型」告诉前端，省得前端非要发一次判定才知道。
func (s *server) handleJudgePrompt(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "只用 GET",
		})
		return
	}
	_, enabled := llm.LoadConfig()
	tpl, ver, src := resolveJudgePrompt(r.Context(), st)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"enabled": enabled,
		"version": ver,
		"source":  src,
		"chars":   len(tpl),
	})
}

// ==========================================================================
// 原 api_worddef.go
// ==========================================================================

// 查中文意思 + 补汉字写法：录入新词时只用打假名，剩下的服务端补齐。
//
//	POST /api/word-def { words:["みず", "たべもの"] }
//	→ { success, enabled, items:[{input:"みず", word:"みず(水)", def:"水"}], missing:[…] }
//
// 老师 2026-10-21 反馈「这个日语词的汉字不加上吗」：光给中文没用，档案里词形是
// 「假名(汉字)」，不补汉字的话这个词录进去就没有汉字，复习时根本学不到写法。
//
// 两条来源，按顺序：
//  1. **档案里已有这个词** → 直接用它的**完整词形 + 释义**（最准、不要钱，
//     而且保证跟复习时看到的中文是同一份 —— 同一个词两处释义不一样等于白练）。
//     先按原样精确找，找不到再按假名部分找（老师打「みず」，库里是「みず(水)」）。
//  2. 剩下的才问模型，模型一并给汉字写法。
//
// ⚠️ 模型不可用（没配 JRP_LLM_*）不是错误：返回 enabled:false + 已有的那部分。
// 前端看到缺释义就把条目留在列表里让老师补，不阻断录入。

// WordDefItem 保根包名字，定义随迁到 internal/worddef（类型别名 = 同一类型，JSON 字节不变）。
// api_vision.go 也用这个名字，故保留别名不改名。
type WordDefItem = worddef.Item

// 编译期锁住接口实现关系：*Storage 读档案、wordDefCaller 问模型链。
var _ worddef.ArchiveStore = (*Storage)(nil)
var _ worddef.Caller = wordDefCaller{}

var errWordDefNoModel = errors.New("没配置大模型")

// wordDefCaller 把「模型是否可用 / 主备链 / 冷却 / 真正发请求」全留在根包 IO 层，
// 与 llm_failover.go 的 judgeWithFallback 共用 llm.SkipModel / MarkResult 冷却状态。
// 用例层只认 worddef.Caller，不认识 llm.Config / LoadConfig / llmChat。
type wordDefCaller struct{}

func (wordDefCaller) Lookup(prompt string) (string, string, error) {
	cfg, ok := llm.LoadConfig()
	if !ok {
		return "", "", errWordDefNoModel
	}
	return llmChat(cfg, prompt)
}

// lookupWordDefs 是两入口（/api/word-def 与 /api/vision）共用的薄适配：
// 注入 ctx / lang / 档案读取 / 模型调用，编排本体在 internal/worddef.Lookup。
// 抽成函数是因为两处要用，口径必须一模一样：
//   - POST /api/word-def（手打录入时补中文）
//   - POST /api/vision（拍照认完词顺手补 —— 2026-10-21 老师反馈「认出来的词没有中文释义」）
//
// 返回按入参顺序排好的结果，以及生效的模型名。模型不可用时 Def 留空，不算错误。
func (s *server) lookupWordDefs(st *Storage, words []string) ([]WordDefItem, string) {
	return worddef.Lookup(s.ctx(), st, wordDefCaller{}, st.lang, words)
}

func (s *server) handleWordDef(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var body struct {
		Words []string `json:"words"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "请求格式不对：" + err.Error(),
		})
		return
	}

	// 去重、去空，保序
	seen := map[string]bool{}
	var words []string
	for _, w := range body.Words {
		w = strings.TrimSpace(w)
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	if len(words) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "没有要查的词",
		})
		return
	}
	if len(words) > 60 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "一次最多查 60 个",
		})
		return
	}

	items, model := s.lookupWordDefs(st, words)

	var missing []string
	for _, it := range items {
		if it.Def == "" {
			missing = append(missing, it.Input)
		}
	}

	out := map[string]interface{}{
		"success": true,
		"enabled": model != "",
		"items":   items,
		"missing": missing,
	}
	if model != "" {
		out["model"] = model
	}
	writeJSON(w, http.StatusOK, out)
}

// ==========================================================================
// 原 api_vision.go
// ==========================================================================

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
	// visionTotalBudget 是一次请求的**总时间预算**，到点就把还没结果的图标成超时返回。
	//
	// 2026-10-21 踩的坑：4 张图跑满 100 多秒，nginx 那边 60 秒就掐断，
	// 老师看到的是一个光秃秃的 504 —— 前面几张明明认出来了也一起没了。
	// 有了预算，最坏情况也是「返回 200 + 已认出的那几张 + 剩下标超时可重认」，
	// 比整个 504 强得多。
	//
	// ⚠️ 三个数必须挨着排：预算 120s < 小程序 request 超时 180s < nginx proxy_read_timeout 300s。
	// 哪一层先断，老师看到的就是哪一层的报错。
	visionTotalBudget = 120 * time.Second
)

// VisionCandidate / VisionImageResult 保根包名字，定义随迁到 internal/vision。
// 类型别名 = 同一类型，HTTP 响应 JSON 字节不变（tag / omitempty 逐字保留）。
type VisionCandidate = vision.Candidate
type VisionImageResult = vision.ImageResult

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
	c.Fallback = llm.ParseFallbackModels(fb, c.Model)
	if v := strings.TrimSpace(os.Getenv("JRP_VISION_TIMEOUT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 120 {
			c.Timeout = time.Duration(n) * time.Second
		}
	}
	return c, c.Endpoint != "" && c.APIKey != ""
}

// 认词结果的纯解析规则（ParseReply / CleanCandidates / HasJapanese）随 P4 vision 域
// 迁到 internal/vision；两域共用的去围栏规则 StripCodeFence 在 P5 收敛到 internal/textutil。
// 本文件只保留 IO / 编排 / 配额 / 冷却 / 限流。

type visionResult struct {
	idx   int
	cands []VisionCandidate
	model string
	err   error
}

// errVisionTooLarge 单独拎出来，visionHumanError 才能给它一句人话。
var (
	errVisionTooLarge = errors.New("图太大，压缩后再试")
	errVisionTimeout  = errors.New("整批超时，先拿已认出的，剩下的点「重认」")
)

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
		if llm.SkipModel(m, now) {
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
		reply, err := llm.Post(cfg.Endpoint, cfg.APIKey, body, cfg.Timeout)
		if err == nil && strings.TrimSpace(reply) != "" {
			llm.MarkResult(m, true, now)
			return strings.TrimSpace(reply), m, false, nil
		}
		lastErr = err
		if visionRetryable(err) {
			retryable = true
		}
		llm.MarkResult(m, false, now)
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

	// pending 每天只能识别 2 张（2026-10-28 老师定）：他有 7 天完整权限，
	// 而每张图都是真金白银调视觉模型。体验版走不到这儿（/api/vision 不在白名单）。
	if u, ok := findUserByUID(cachedUsers(r.Context(), s.storage), st.uid); ok && u.Role == rolePending {
		okN, used, left := takeQuota(r.Context(), st, quotaNameVision, len(body.Images), pendingVisionDaily)
		if !okN {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{
				"success": false,
				"error":   "还没审核通过：体验期内每天最多识别 " + strconv.Itoa(pendingVisionDaily) + " 张图",
				"used":    used, "left": left,
				"pending": true, "quota": "vision_daily",
			})
			return
		}
	}

	jobs := make(chan int)
	results := make(chan visionResult, len(body.Images))
	var wg sync.WaitGroup

	workers := visionConcurrency
	if len(body.Images) < workers {
		workers = len(body.Images)
	}
	deadline := time.Now().Add(visionTotalBudget)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if time.Now().After(deadline) {
					results <- visionResult{idx: i, err: errVisionTimeout}
					continue
				}
				results <- askVision(cfg, i, body.Images[i], deadline)
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
			if c.Def == "" || !worddef.HasParen(c.Word) {
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
				if !worddef.HasParen(c.Word) && it.Word != "" {
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
//
// deadline 是一次请求的总预算：每次发问前看一眼，超了就别再给这张图添时间了，
// 把剩下的预算留给其它还没问的图 —— 一张图吃满预算、其余全军覆没，比四张各拿一半更糟。
func askVision(cfg visionConfig, idx int, img string, deadline time.Time) visionResult {
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
		if !deadline.IsZero() && time.Now().After(deadline) {
			if res.err == nil {
				res.err = errVisionTimeout
			}
			return res
		}
		reply, model, retryable, err := visionChat(cfg, vision.Prompt, "data:image/jpeg;base64,"+img)
		if err != nil {
			res.err = err
			if attempt == visionRetry || !retryable {
				break
			}
			// 退避一下再试：限流/抖动这类，立刻重试还是撞同一堵墙
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
			continue
		}
		cands, perr := vision.ParseReply(reply)
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
	if err == errVisionTimeout {
		return "整批超时，点「重认这张」单独再来"
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
