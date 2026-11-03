package llm

import (
	"slices"
	"testing"
	"time"
)

// 没配密钥就不启用 —— 这是「默认关闭」的底线：老师还没定模型之前，
// 造句必须照常能练，不能因为没配就报错。
func TestLoadLLMConfigDisabledWithoutEnv(t *testing.T) {
	t.Setenv("JRP_LLM_MODEL", "")
	t.Setenv("JRP_LLM_FALLBACK", "")
	t.Setenv("JRP_LLM_TIMEOUT", "")
	t.Setenv("JRP_LLM_ENDPOINT", "")
	t.Setenv("JRP_LLM_API_KEY", "")
	_, ok := LoadConfig()
	if ok {
		t.Fatal("没配 endpoint/key 却启用了")
	}

	t.Setenv("JRP_LLM_ENDPOINT", "https://api.deepseek.com/v1/chat/completions")
	_, ok = LoadConfig()
	if ok {
		t.Fatal("只有 endpoint 没有 key 也不该启用")
	}
}

func TestLoadLLMConfigReadsEnv(t *testing.T) {
	t.Setenv("JRP_LLM_FALLBACK", "")
	t.Setenv("JRP_LLM_ENDPOINT", "https://api.deepseek.com/v1/chat/completions")
	t.Setenv("JRP_LLM_API_KEY", "sk-test")
	t.Setenv("JRP_LLM_MODEL", "deepseek-chat")
	t.Setenv("JRP_LLM_TIMEOUT", "5")
	c, ok := LoadConfig()
	if !ok {
		t.Fatal("配齐了却没启用")
	}
	if c.Model != "deepseek-chat" || c.Timeout.Seconds() != 5 {
		t.Errorf("got model=%q timeout=%v", c.Model, c.Timeout)
	}
}

// 备用链解析：去空、去重、排除主模型 —— 主模型已经在链首，重复只是白试一次。
func TestParseFallbackModels(t *testing.T) {
	got := ParseFallbackModels(" a , b ,, a ,", "primary")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("期望 [a b]，得到 %v", got)
	}
	if got := ParseFallbackModels("primary,backup", "primary"); len(got) != 1 || got[0] != "backup" {
		t.Fatalf("主模型必须被排除，得到 %v", got)
	}
	if got := ParseFallbackModels("", "primary"); len(got) != 0 {
		t.Fatalf("空串应返回空列表，得到 %v", got)
	}
}

func TestLoadConfigDefaultsAndTrim(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		apiKey   string
		model    string
		fallback string
		want     Config
		enabled  bool
	}{
		{
			name: "defaults", endpoint: "https://llm.test/chat", apiKey: "test-only-key",
			want:    Config{Endpoint: "https://llm.test/chat", APIKey: "test-only-key", Model: "deepseek-chat", Timeout: 8 * time.Second},
			enabled: true,
		},
		{
			name: "trim_all_fields", endpoint: " \thttps://llm.test/chat\n", apiKey: " \ttest-only-key\r\n", model: "　 primary \t",
			fallback: " primary, backup-b , , backup-a, backup-b, primary, backup-c, ",
			want:     Config{Endpoint: "https://llm.test/chat", APIKey: "test-only-key", Model: "primary", Fallback: []string{"backup-b", "backup-a", "backup-c"}, Timeout: 8 * time.Second},
			enabled:  true,
		},
		{
			name: "blank_model_uses_default", endpoint: "https://llm.test/chat", apiKey: "test-only-key", model: " \t\n　", fallback: "deepseek-chat, backup, backup",
			want:    Config{Endpoint: "https://llm.test/chat", APIKey: "test-only-key", Model: "deepseek-chat", Fallback: []string{"backup"}, Timeout: 8 * time.Second},
			enabled: true,
		},
		{
			name: "blank_endpoint_disables", endpoint: " \t\n　", apiKey: "test-only-key",
			want: Config{APIKey: "test-only-key", Model: "deepseek-chat", Timeout: 8 * time.Second},
		},
		{
			name: "blank_key_disables", endpoint: "https://llm.test/chat", apiKey: " \t\r\n　",
			want: Config{Endpoint: "https://llm.test/chat", Model: "deepseek-chat", Timeout: 8 * time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JRP_LLM_ENDPOINT", tc.endpoint)
			t.Setenv("JRP_LLM_API_KEY", tc.apiKey)
			t.Setenv("JRP_LLM_MODEL", tc.model)
			t.Setenv("JRP_LLM_FALLBACK", tc.fallback)
			t.Setenv("JRP_LLM_TIMEOUT", "")
			got, enabled := LoadConfig()
			if enabled != tc.enabled {
				t.Errorf("启用状态 = %v，期望 %v", enabled, tc.enabled)
			}
			if got.Endpoint != tc.want.Endpoint || got.APIKey != tc.want.APIKey || got.Model != tc.want.Model || got.Timeout != tc.want.Timeout || !slices.Equal(got.Fallback, tc.want.Fallback) {
				t.Error("配置默认值、空白清理或备用模型顺序不符合预期")
			}
		})
	}
}

func TestLoadConfigTimeoutBounds(t *testing.T) {
	t.Setenv("JRP_LLM_ENDPOINT", "https://llm.test/chat")
	t.Setenv("JRP_LLM_API_KEY", "test-only-key")
	t.Setenv("JRP_LLM_MODEL", "test-model")
	t.Setenv("JRP_LLM_FALLBACK", "")
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "minimum", raw: "1", want: time.Second},
		{name: "maximum", raw: "60", want: 60 * time.Second},
		{name: "trim", raw: " \t60\r\n", want: 60 * time.Second},
		{name: "empty", raw: "", want: 8 * time.Second},
		{name: "blank", raw: " \t\n　", want: 8 * time.Second},
		{name: "zero", raw: "0", want: 8 * time.Second},
		{name: "negative", raw: "-1", want: 8 * time.Second},
		{name: "above_maximum", raw: "61", want: 8 * time.Second},
		{name: "not_numeric", raw: "invalid", want: 8 * time.Second},
		{name: "fraction", raw: "1.5", want: 8 * time.Second},
		{name: "integer_overflow", raw: "999999999999999999999999", want: 8 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JRP_LLM_TIMEOUT", tc.raw)
			got, enabled := LoadConfig()
			if !enabled {
				t.Fatal("有效 endpoint/key 不应被 timeout 配置禁用")
			}
			if got.Timeout != tc.want {
				t.Errorf("timeout = %v，期望 %v", got.Timeout, tc.want)
			}
		})
	}
}

func TestParseFallbackModelsOrderAndWhitespace(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		primary string
		want    []string
	}{
		{name: "ordered_unique", raw: " primary, zeta, ,alpha,zeta, beta,alpha,primary, ", primary: " \tprimary\n", want: []string{"zeta", "alpha", "beta"}},
		{name: "blank_entries", raw: " , \t,\n,　, ", primary: "primary"},
		{name: "only_primary", raw: "primary, primary, ", primary: "primary"},
		{name: "blank_primary", raw: " zeta, alpha, zeta ", primary: " \t", want: []string{"zeta", "alpha"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseFallbackModels(tc.raw, tc.primary); !slices.Equal(got, tc.want) {
				t.Errorf("备用模型 = %v，期望 %v", got, tc.want)
			}
		})
	}
}
