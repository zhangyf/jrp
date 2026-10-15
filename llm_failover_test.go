package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 备用链解析：去空、去重、排除主模型 —— 主模型已经在链首，重复只是白试一次。
func TestParseFallbackModels(t *testing.T) {
	got := parseFallbackModels(" a , b ,, a ,", "primary")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("期望 [a b]，得到 %v", got)
	}
	if got := parseFallbackModels("primary,backup", "primary"); len(got) != 1 || got[0] != "backup" {
		t.Fatalf("主模型必须被排除，得到 %v", got)
	}
	if got := parseFallbackModels("", "primary"); len(got) != 0 {
		t.Fatalf("空串应返回空列表，得到 %v", got)
	}
}

// 冷却：失败 1 次照常用，连续 2 次进冷却，过了 5 分钟自动恢复试探。
func TestLLMModelCooling(t *testing.T) {
	now := time.Now()
	if llmModelCooling(1, now.Add(llmCooldown), now) {
		t.Fatal("只失败 1 次不该被冷却")
	}
	if !llmModelCooling(llmFailThreshold, now.Add(llmCooldown), now) {
		t.Fatal("连续失败到阈值必须进冷却")
	}
	if llmModelCooling(llmFailThreshold, now.Add(-time.Second), now) {
		t.Fatal("冷却时间过了就该恢复")
	}
}

func resetLLMHealth() {
	llmHealth.Lock()
	llmHealth.m = map[string]*llmModelHealth{}
	llmHealth.Unlock()
}

// fakeLLMServer 起一个假网关，按请求里的 model 决定怎么回。
// asked 记下每次问的是谁，用来验证「主挂了真的换了备用」。
func fakeLLMServer(asked *[]string, mode map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*asked = append(*asked, body.Model)
		switch mode[body.Model] {
		case "http500":
			http.Error(w, "gateway down", http.StatusBadGateway)
		case "garbage":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"这个嘛，我觉得差不多"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"correct\": true, \"reason\": \"意思一致\"}"}}]}`))
		}
	}))
}

func TestJudgeWithFallbackSwitchesOnFailure(t *testing.T) {
	resetLLMHealth()

	t.Run("主模型 5xx → 换备用", func(t *testing.T) {
		var asked []string
		srv := fakeLLMServer(&asked, map[string]string{"primary": "http500"})
		defer srv.Close()

		cfg := llmConfig{Endpoint: srv.URL, Model: "primary", Fallback: []string{"backup"}, Timeout: 3 * time.Second}
		resetLLMHealth()
		res, model, err := judgeWithFallback(cfg, "prompt")
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if model != "backup" || !res.Correct {
			t.Fatalf("期望切到 backup 且判对，得到 model=%s res=%+v", model, res)
		}
		if strings.Join(asked, ",") != "primary,backup" {
			t.Fatalf("期望先问 primary 再问 backup，实际问了 %v", asked)
		}
	})

	t.Run("主模型回废话（解析不出 JSON）→ 换备用", func(t *testing.T) {
		var asked []string
		srv := fakeLLMServer(&asked, map[string]string{"primary": "garbage"})
		defer srv.Close()

		cfg := llmConfig{Endpoint: srv.URL, Model: "primary", Fallback: []string{"backup"}, Timeout: 3 * time.Second}
		resetLLMHealth()
		_, model, err := judgeWithFallback(cfg, "prompt")
		if err != nil || model != "backup" {
			t.Fatalf("模型胡说也必须换人，得到 model=%s err=%v", model, err)
		}
	})

	t.Run("全挂 → 返回 error，交给调用方退回字面判定", func(t *testing.T) {
		var asked []string
		srv := fakeLLMServer(&asked, map[string]string{"primary": "http500", "backup": "http500"})
		defer srv.Close()

		cfg := llmConfig{Endpoint: srv.URL, Model: "primary", Fallback: []string{"backup"}, Timeout: 3 * time.Second}
		resetLLMHealth()
		if _, _, err := judgeWithFallback(cfg, "prompt"); err == nil {
			t.Fatal("全挂必须返回 error")
		}
	})

	t.Run("连续失败进冷却，之后直接跳过主模型", func(t *testing.T) {
		var asked1, asked2 []string
		srv := fakeLLMServer(&asked1, map[string]string{"primary": "http500"})
		defer srv.Close()
		cfg := llmConfig{Endpoint: srv.URL, Model: "primary", Fallback: []string{"backup"}, Timeout: 3 * time.Second}
		resetLLMHealth()
		_, _, _ = judgeWithFallback(cfg, "p1")
		_, _, _ = judgeWithFallback(cfg, "p2") // 第 2 次失败 → primary 进冷却

		srv2 := fakeLLMServer(&asked2, map[string]string{"primary": "http500"})
		defer srv2.Close()
		cfg.Endpoint = srv2.URL
		if _, model, err := judgeWithFallback(cfg, "p3"); err != nil || model != "backup" {
			t.Fatalf("冷却中应直接用备用，得到 model=%s err=%v", model, err)
		}
		if len(asked2) != 1 || asked2[0] != "backup" {
			t.Fatalf("冷却中的主模型不该再被问，实际问了 %v", asked2)
		}
	})
}
