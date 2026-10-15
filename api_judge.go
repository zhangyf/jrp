package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

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

// llmConfig 大模型配置，全部来自环境变量。
type llmConfig struct {
	Endpoint string        // OpenAI 兼容的 chat/completions 地址
	APIKey   string        // Bearer token
	Model    string        // 主模型
	Fallback []string      // 备用模型，主模型失败时按顺序接上（见 llm_failover.go）
	Timeout  time.Duration // 单个模型的超时（切换链条里每个都各自计时）
}

const llmDefaultTimeout = 8 * time.Second

// loadLLMConfig 读环境变量。Endpoint 或 APIKey 任一为空就是不启用。
//
//	JRP_LLM_ENDPOINT  https://api.deepseek.com/v1/chat/completions
//	JRP_LLM_API_KEY   sk-...
//	JRP_LLM_MODEL     主模型（可选，默认 deepseek-chat）
//	JRP_LLM_FALLBACK  备用模型，逗号分隔（可选，主模型挂了依次接上）
//	JRP_LLM_TIMEOUT   秒（可选，默认 8，上限 60）—— 链条里每个模型各自计时
func loadLLMConfig() (llmConfig, bool) {
	c := llmConfig{
		Endpoint: strings.TrimSpace(os.Getenv("JRP_LLM_ENDPOINT")),
		APIKey:   strings.TrimSpace(os.Getenv("JRP_LLM_API_KEY")),
		Model:    strings.TrimSpace(os.Getenv("JRP_LLM_MODEL")),
		Timeout:  llmDefaultTimeout,
	}
	if c.Model == "" {
		c.Model = "deepseek-chat"
	}
	c.Fallback = parseFallbackModels(os.Getenv("JRP_LLM_FALLBACK"), c.Model)
	if v := strings.TrimSpace(os.Getenv("JRP_LLM_TIMEOUT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 60 {
			c.Timeout = time.Duration(n) * time.Second
		}
	}
	return c, c.Endpoint != "" && c.APIKey != ""
}

// ---------- prompt ----------

// builtinJudgePrompt 内置判定模板。COS 上没有 / 模板坏了 / 读不到时用这份。
//
// 2026-10-12 收紧过一次（老师要求「不能太松」）：
//   - 助词**缺失**从「算对」改成「判错」—— 练造句就是练助词，该有的 を/が/に/へ/で
//     没写，等于没练到。但助词**换成另一个同样成立**的（へ↔に）仍判对，别冤枉。
//   - 「拿不准就判正确」改成「拿不准就看要素齐不齐」：要素齐全只是写法不同 → 判对；
//     要素缺失（尤其助词缺失导致格关系不明）→ 判错。
//     没写成「拿不准就判错」，是因为那样会把 へ↔に 这类同样成立的替换一起拖下水。
// 第 4 条（感叹词/语气词不算错）和第 5 条（没学过的表达也算对）不能删 ——
// 删了模型就变成语法警察，会判一堆老师本来会的句子是错的。
// TestJudgePromptKeepsConservativeRules 盯着这几条不许删。
//
// 整段 prompt 可外置到 COS（configs/judge_prompt.txt），这份是兜底。
// 占位符：{answer} 参考答案、{chinese} 中文意思、{input} 学生写的。
const builtinJudgePrompt = `你是日语老师。判断学生写的句子是否表达了参考答案的意思。

参考答案：{answer}
中文意思：{chinese}
学生写的：{input}

判定规则：
1. 只判断意思是否成立，不评价语法是否地道、措辞是否优雅。
2. 以下差异视为正确：汉字与假名的写法不同、全角半角、标点符号、空格、词序微调、同义词替换、敬体简体差异。
3. 助词缺失判错：该有的 を/が/に/へ/で 没写，导致「谁对谁做了什么」说不清的，判错。助词换成另一个同样成立的（へ↔に 等）仍判对；多加了但不改变意思的助词也算对。
4. 感叹词、语气词、开场填充词（ええ/えっ/えっと/あの/うーん/あれ 等）互相不同不算错 —— 它们不承载句子的核心意思。唯一例外：把肯定答成否定（はい↔いいえ、うん↔ううん）才算错。
5. 学生用了没学过的表达，只要意思成立，仍判正确。
6. 拿不准时按关键要素是否齐全来判断：主体、动作、对象、时态、肯定否定都齐全、只是写法不同 → 判对；要素缺失（尤其是助词缺失，导致格关系说不清）→ 判错。

例：参考答案「李さんは 中国人です。」学生写「李さん 中国人です。」→ 错误（省了 は）。
例：参考答案「ご飯を 食べました」学生写「ごはん 食べた」→ 错误（省了 を；写成假名和简体本身没问题）。
例：参考答案「駅へ 行きます」学生写「駅に 行きます」→ 正确（へ 和 に 都成立）。
例：参考答案「えっと、銀座ですか？」学生写「ええ、銀座ですか。」→ 正确（开场词不同，核心问句一样）。
例：参考答案「はい、そうです」学生写「いいえ、違います」→ 错误（肯定变否定）。

只输出一行 JSON，不要解释：{"correct": true 或 false, "reason": "一句话中文说明，20 字以内"}`

// judgePrompt 用内置模板渲染判定词。
func judgePrompt(answer, chinese, input string) string {
	return fillJudgePrompt(builtinJudgePrompt, answer, chinese, input)
}

// fillJudgePrompt 把占位符换成实际句子。
// 中文意思为空时，模板里含 {chinese} 的整行直接删掉 —— 免得留一行「中文意思：」空壳。
func fillJudgePrompt(tpl, answer, chinese, input string) string {
	hasChinese := strings.TrimSpace(chinese) != ""
	var kept []string
	for _, ln := range strings.Split(tpl, "\n") {
		if !hasChinese && strings.Contains(ln, "{chinese}") {
			continue
		}
		kept = append(kept, ln)
	}
	s := strings.Join(kept, "\n")
	s = strings.ReplaceAll(s, "{answer}", answer)
	s = strings.ReplaceAll(s, "{chinese}", chinese)
	s = strings.ReplaceAll(s, "{input}", input)
	// 删掉空行后可能留下的连续换行
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// validateJudgePrompt 校验外置模板。护栏之一：缺 {answer} 或 {input} 一律判坏，
// 否则填完的 prompt 里根本没有要判的句子，模型只能瞎猜。
func validateJudgePrompt(tpl string) error {
	if strings.TrimSpace(tpl) == "" {
		return fmt.Errorf("模板为空")
	}
	var miss []string
	if !strings.Contains(tpl, "{answer}") {
		miss = append(miss, "{answer}")
	}
	if !strings.Contains(tpl, "{input}") {
		miss = append(miss, "{input}")
	}
	if len(miss) > 0 {
		return fmt.Errorf("模板缺少占位符 %s", strings.Join(miss, "、"))
	}
	return nil
}

// ensureJSONRule 护栏之二：模板里没提 JSON 输出就补上。
// 少了这行模型就开始自由发挥，parseJudgeReply 抠不出结论，判分全线失灵。
func ensureJSONRule(p string) string {
	if strings.Contains(strings.ToLower(p), "correct") {
		return p
	}
	return p + "\n\n只输出一行 JSON，不要解释：{\"correct\": true 或 false, \"reason\": \"一句话中文说明，20 字以内\"}"
}

// promptVersion 模板版本：内容 sha256 前 12 位。
// 改一个字版本就变，草稿里的旧结论据此作废重判 —— 不能拿旧规矩的结论当新规矩的。
func promptVersion(tpl string) string {
	sum := sha256.Sum256([]byte(tpl))
	return hex.EncodeToString(sum[:])[:12]
}

// ---------- 模板装载（带缓存） ----------

const judgePromptTTL = 60 * time.Second

var judgePromptCache = struct {
	sync.Mutex
	at      time.Time
	tpl     string
	version string
	source  string // "cos" 或 "builtin"
}{}

// chooseJudgePrompt 在「COS 上读到的」和「内置的」之间做选择。抽成纯函数是为了能单测 ——
// 护栏全在这里：读不到、空的、缺占位符，一律回退内置，练习绝不因为一份坏文件卡住。
func chooseJudgePrompt(cosText string, err error) (tpl, source string) {
	if err != nil || strings.TrimSpace(cosText) == "" {
		return builtinJudgePrompt, "builtin"
	}
	if err := validateJudgePrompt(cosText); err != nil {
		log.Printf("judge-prompt: COS 上的模板不可用（%v），回退内置模板", err)
		return builtinJudgePrompt, "builtin"
	}
	return cosText, "cos"
}

// resolveJudgePrompt 拿当前生效的模板：COS 优先，坏了就用内置。
// 缓存 60 秒 —— 判定在每次输入后都可能触发，不能每句都打一次 COS。
func resolveJudgePrompt(ctx context.Context, st *Storage) (tpl, version, source string) {
	judgePromptCache.Lock()
	if time.Since(judgePromptCache.at) < judgePromptTTL && judgePromptCache.tpl != "" {
		t, v, src := judgePromptCache.tpl, judgePromptCache.version, judgePromptCache.source
		judgePromptCache.Unlock()
		return t, v, src
	}
	judgePromptCache.Unlock()

	tpl, source = chooseJudgePrompt(st.DownloadJudgePrompt(ctx))
	version = promptVersion(tpl)

	judgePromptCache.Lock()
	judgePromptCache.at, judgePromptCache.tpl = time.Now(), tpl
	judgePromptCache.version, judgePromptCache.source = version, source
	judgePromptCache.Unlock()
	return tpl, version, source
}

// renderJudgePrompt 渲染出最终发给模型的文本。
func renderJudgePrompt(tpl, answer, chinese, input string) string {
	return ensureJSONRule(fillJudgePrompt(tpl, answer, chinese, input))
}

// ---------- 解析 ----------

type judgeResult struct {
	Correct bool
	Reason  string
}

// parseJudgeReply 从模型回复里抠出 JSON。模型常常在 JSON 前后加废话
// （```json 围栏、说明文字），所以找第一个 { 到最后一个 } 之间那段来解析。
func parseJudgeReply(reply string) (judgeResult, error) {
	s := strings.TrimSpace(reply)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	var out struct {
		Correct *bool  `json:"correct"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return judgeResult{}, fmt.Errorf("解析模型回复失败：%w（原文：%.80s）", err, reply)
	}
	if out.Correct == nil {
		return judgeResult{}, fmt.Errorf("模型没给 correct 字段：%.80s", reply)
	}
	return judgeResult{Correct: *out.Correct, Reason: strings.TrimSpace(out.Reason)}, nil
}

// ---------- 调用 ----------

// buildLLMRequest 造请求体。抽出来是为了能单测 —— 「stream:false」这条
// 一旦被删，网关可能改走 SSE，判定会全线失败（2026-09-29）。
func buildLLMRequest(cfg llmConfig, prompt string) ([]byte, error) {
	// 显式 stream:false —— 有些网关（腾讯云 tokenhub 这类）不写这条就按 SSE
	// 流式返回，choices[0].message.content 会拿不到东西，判定直接失败。
	return json.Marshal(map[string]interface{}{
		"model":       cfg.Model,
		"temperature": 0,
		"stream":      false,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
}

func callLLM(cfg llmConfig, prompt string) (string, error) {
	body, err := buildLLMRequest(cfg, prompt)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("模型返回 %d：%.120s", resp.StatusCode, string(raw))
	}
	var apiResp struct {
		Choices []struct {
			Message struct{ Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		return "", fmt.Errorf("解析模型响应失败：%w", err)
	}
	if len(apiResp.Choices) == 0 || strings.TrimSpace(apiResp.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("模型返回空内容")
	}
	return apiResp.Choices[0].Message.Content, nil
}

// ---------- 缓存 ----------

// 判定缓存：同一 (原句, 学生写的) 只问一次模型。刷新页面、换设备重开
// 都必须得到同一个结论 —— 否则就是「这次判对下次判错」，比没有模型更糟。
var judgeCache = struct {
	sync.Mutex
	m map[string]judgeResult
}{m: map[string]judgeResult{}}

// judgeCacheKey 缓存键带模板版本：prompt 一改，旧的判定结论自然失效，
// 不会出现「规矩换了但还拿旧结论糊弄」的情况。
func judgeCacheKey(version, answer, input string) string {
	return version + "\x00" + normSentence(answer) + "\x00" + normSentence(input)
}

// judgeWithCache 判定一句，顺带返回这次用的模板版本（前端存进草稿，
// 下次恢复时对不上就作废重判）。
func judgeWithCache(ctx context.Context, st *Storage, cfg llmConfig, answer, chinese, input string) (judgeResult, string, string, error) {
	tpl, version, _ := resolveJudgePrompt(ctx, st)
	key := judgeCacheKey(version, answer, input)
	judgeCache.Lock()
	if r, ok := judgeCache.m[key]; ok {
		judgeCache.Unlock()
		return r, version, "", nil
	}
	judgeCache.Unlock()

	// 主模型失败会自动切备用 —— 返回实际用的是哪个模型，写进响应便于排查。
	r, model, err := judgeWithFallback(cfg, renderJudgePrompt(tpl, answer, chinese, input))
	if err != nil {
		return judgeResult{}, version, "", err
	}
	judgeCache.Lock()
	judgeCache.m[key] = r
	judgeCache.Unlock()
	return r, version, model, nil
}

// ---------- HTTP ----------

// POST /api/judge-sentence  { answer, chinese, input }
//
//	→ { enabled:true,  correct, reason, by:"llm"|"literal" }
//	→ { enabled:false }                       没配密钥，前端关掉这个功能
//	→ { enabled:true, correct:false, by:"literal", error }   模型挂了，按字面判
func (s *server) handleJudgeSentence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "只用 POST",
		})
		return
	}

	cfg, enabled := loadLLMConfig()
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
	if normSentence(in.Answer) == normSentence(in.Input) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": true, "by": "literal",
		})
		return
	}

	res, ver, model, err := judgeWithCache(r.Context(), s.storage, cfg, in.Answer, in.Chinese, in.Input)
	if err != nil {
		// 模型不可用不是业务错误：不返回 5xx，让前端退回字面判定继续练
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": false, "by": "literal",
			"reason": "模型不可用，按字面判定",
			"chain": append([]string{cfg.Model}, cfg.Fallback...),
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
func (s *server) handleJudgePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "只用 GET",
		})
		return
	}
	_, enabled := loadLLMConfig()
	tpl, ver, src := resolveJudgePrompt(r.Context(), s.storage)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"enabled": enabled,
		"version": ver,
		"source":  src,
		"chars":   len(tpl),
	})
}
