package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	Model    string        // 模型名
	Timeout  time.Duration // 单次判定的超时
}

const llmDefaultTimeout = 8 * time.Second

// loadLLMConfig 读环境变量。Endpoint 或 APIKey 任一为空就是不启用。
//
//	JRP_LLM_ENDPOINT  https://api.deepseek.com/v1/chat/completions
//	JRP_LLM_API_KEY   sk-...
//	JRP_LLM_MODEL     deepseek-chat（可选，默认这个）
//	JRP_LLM_TIMEOUT   秒（可选，默认 8，上限 60）
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
	if v := strings.TrimSpace(os.Getenv("JRP_LLM_TIMEOUT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 60 {
			c.Timeout = time.Duration(n) * time.Second
		}
	}
	return c, c.Endpoint != "" && c.APIKey != ""
}

// ---------- prompt ----------

// judgePrompt 判定用词。规则写死在这里：只判意思，不判措辞；
// 拿不准判对。老师的原则是「已学语法不该被标成陷阱」，模型更不该自作主张。
func judgePrompt(answer, chinese, input string) string {
	var b strings.Builder
	b.WriteString("你是日语老师。判断学生写的句子是否表达了参考答案的意思。\n\n")
	b.WriteString("参考答案：" + answer + "\n")
	if strings.TrimSpace(chinese) != "" {
		b.WriteString("中文意思：" + chinese + "\n")
	}
	b.WriteString("学生写的：" + input + "\n\n")
	b.WriteString("判定规则：\n")
	b.WriteString("1. 只判断意思是否成立，不评价语法是否地道、措辞是否优雅。\n")
	b.WriteString("2. 以下差异一律视为正确：汉字与假名的写法不同、全角半角、标点符号、空格、")
	b.WriteString("助词的省略或添加（を/が/は 等）、词序微调、同义词替换、敬体简体差异。\n")
	b.WriteString("3. 学生用了没学过的表达，只要意思成立，仍判正确。\n")
	b.WriteString("4. 只有关键要素错了才判错误：主体、动作、对象、时态、肯定否定。\n")
	b.WriteString("5. 拿不准就判正确。\n\n")
	b.WriteString(`只输出一行 JSON，不要解释：{"correct": true 或 false, "reason": "一句话中文说明，20 字以内"}`)
	return b.String()
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

func callLLM(cfg llmConfig, prompt string) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"model":       cfg.Model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
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

func judgeCacheKey(answer, input string) string {
	return normSentence(answer) + "\x00" + normSentence(input)
}

func judgeWithCache(cfg llmConfig, answer, chinese, input string) (judgeResult, error) {
	key := judgeCacheKey(answer, input)
	judgeCache.Lock()
	if r, ok := judgeCache.m[key]; ok {
		judgeCache.Unlock()
		return r, nil
	}
	judgeCache.Unlock()

	reply, err := callLLM(cfg, judgePrompt(answer, chinese, input))
	if err != nil {
		return judgeResult{}, err
	}
	r, err := parseJudgeReply(reply)
	if err != nil {
		return judgeResult{}, err
	}
	judgeCache.Lock()
	judgeCache.m[key] = r
	judgeCache.Unlock()
	return r, nil
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

	res, err := judgeWithCache(cfg, in.Answer, in.Chinese, in.Input)
	if err != nil {
		// 模型不可用不是业务错误：不返回 5xx，让前端退回字面判定继续练
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "enabled": true, "correct": false, "by": "literal",
			"reason": "模型不可用，按字面判定",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "enabled": true,
		"correct": res.Correct, "reason": res.Reason, "by": "llm",
	})
}
