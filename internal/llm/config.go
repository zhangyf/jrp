package llm

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 大模型配置，全部来自环境变量。
type Config struct {
	Endpoint string        // OpenAI 兼容的 chat/completions 地址
	APIKey   string        // Bearer token
	Model    string        // 主模型
	Fallback []string      // 备用模型，主模型失败时按顺序接上（见 llm_failover.go）
	Timeout  time.Duration // 单个模型的超时（切换链条里每个都各自计时）
}

const defaultTimeout = 8 * time.Second

// LoadConfig 读环境变量。Endpoint 或 APIKey 任一为空就是不启用。
//
//	JRP_LLM_ENDPOINT  https://api.deepseek.com/v1/chat/completions
//	JRP_LLM_API_KEY   sk-...
//	JRP_LLM_MODEL     主模型（可选，默认 deepseek-chat）
//	JRP_LLM_FALLBACK  备用模型，逗号分隔（可选，主模型挂了依次接上）
//	JRP_LLM_TIMEOUT   秒（可选，默认 8，上限 60）—— 链条里每个模型各自计时
func LoadConfig() (Config, bool) {
	c := Config{
		Endpoint: strings.TrimSpace(os.Getenv("JRP_LLM_ENDPOINT")),
		APIKey:   strings.TrimSpace(os.Getenv("JRP_LLM_API_KEY")),
		Model:    strings.TrimSpace(os.Getenv("JRP_LLM_MODEL")),
		Timeout:  defaultTimeout,
	}
	if c.Model == "" {
		c.Model = "deepseek-chat"
	}
	c.Fallback = ParseFallbackModels(os.Getenv("JRP_LLM_FALLBACK"), c.Model)
	if v := strings.TrimSpace(os.Getenv("JRP_LLM_TIMEOUT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 60 {
			c.Timeout = time.Duration(n) * time.Second
		}
	}
	return c, c.Endpoint != "" && c.APIKey != ""
}

// ParseFallbackModels 解析备用模型列表。纯函数，可单测。
func ParseFallbackModels(raw, primary string) []string {
	seen := map[string]bool{}
	if p := strings.TrimSpace(primary); p != "" {
		seen[p] = true
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}
