package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/llm"
	"github.com/zhangyf/jrp/internal/worddef"
)

// 固定过去时刻用于检查失败计数是否达到阈值；未来时刻用于注入仍有效的冷却。
var (
	llmHealthTestTime  = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	llmCoolingTestTime = time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
)

const testLLMImageURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j9oAAAAAASUVORK5CYII="

func testLLMHealthModel(t *testing.T, suffix string) string {
	t.Helper()
	model := t.Name() + "/" + suffix
	llm.MarkResult(model, true, llmHealthTestTime)
	t.Cleanup(func() { llm.MarkResult(model, true, llmHealthTestTime) })
	return model
}

// fakeLLMServer 按 model 回应；请求记录只通过加锁快照读取。
func fakeLLMServer(t *testing.T, mode map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	responses := make(map[string]string, len(mode))
	for model, response := range mode {
		responses[model] = response
	}
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "请求不是 JSON", http.StatusBadRequest)
			return
		}
		mu.Lock()
		asked = append(asked, body.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch responses[body.Model] {
		case "http500":
			http.Error(w, "gateway down", http.StatusBadGateway)
		case "http400":
			http.Error(w, "model does not exist", http.StatusBadRequest)
		case "garbage":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"这个嘛，我觉得差不多"}}]}`))
		case "empty":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" \t "}}]}`))
		case "invalid":
			_, _ = w.Write([]byte(`{`))
		case "vision":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"word\":\"みず(水)\",\"def\":\"水\"}]"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"correct\": true, \"reason\": \"意思一致\"}"}}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

func assertLLMRequests(t *testing.T, snapshot func() []string, want ...string) {
	t.Helper()
	if got := snapshot(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("请求顺序不符：得到 %v，期望 %v", got, want)
	}
}

func TestJudgeWithFallbackSwitchesOnFailure(t *testing.T) {
	t.Run("主模型 5xx → 换备用", func(t *testing.T) {
		primary := testLLMHealthModel(t, "primary")
		backup := testLLMHealthModel(t, "backup")
		srv, asked := fakeLLMServer(t, map[string]string{primary: "http500"})
		cfg := llm.Config{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
		res, model, err := judgeWithFallback(cfg, "prompt")
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if model != backup || !res.Correct {
			t.Fatalf("期望切到 backup 且判对，得到 model=%s res=%+v", model, res)
		}
		assertLLMRequests(t, asked, primary, backup)
	})

	t.Run("主模型回废话（解析不出 JSON）→ 换备用", func(t *testing.T) {
		primary := testLLMHealthModel(t, "primary")
		backup := testLLMHealthModel(t, "backup")
		srv, asked := fakeLLMServer(t, map[string]string{primary: "garbage"})
		cfg := llm.Config{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
		_, model, err := judgeWithFallback(cfg, "prompt")
		if err != nil || model != backup {
			t.Fatalf("模型胡说也必须换人，得到 model=%s err=%v", model, err)
		}
		assertLLMRequests(t, asked, primary, backup)
	})

	t.Run("全挂 → 返回 error，交给调用方退回字面判定", func(t *testing.T) {
		primary := testLLMHealthModel(t, "primary")
		backup := testLLMHealthModel(t, "backup")
		srv, asked := fakeLLMServer(t, map[string]string{primary: "http500", backup: "http500"})
		cfg := llm.Config{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
		if _, _, err := judgeWithFallback(cfg, "prompt"); err == nil {
			t.Fatal("全挂必须返回 error")
		}
		assertLLMRequests(t, asked, primary, backup)
	})

	t.Run("连续失败进冷却，之后直接跳过主模型", func(t *testing.T) {
		primary := testLLMHealthModel(t, "primary")
		backup := testLLMHealthModel(t, "backup")
		srv, asked1 := fakeLLMServer(t, map[string]string{primary: "http500"})
		cfg := llm.Config{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
		_, _, _ = judgeWithFallback(cfg, "p1")
		_, _, _ = judgeWithFallback(cfg, "p2") // 第 2 次失败进入冷却。
		assertLLMRequests(t, asked1, primary, backup, primary, backup)

		srv2, asked2 := fakeLLMServer(t, map[string]string{primary: "http500"})
		cfg.Endpoint = srv2.URL
		if _, model, err := judgeWithFallback(cfg, "p3"); err != nil || model != backup {
			t.Fatalf("冷却中应直接用备用，得到 model=%s err=%v", model, err)
		}
		assertLLMRequests(t, asked2, backup)
	})
}

func TestLLMHealthSharedAcrossCalls(t *testing.T) {
	callers := []struct {
		name string
		call func(llm.Config) (string, error)
	}{
		{"判句", func(cfg llm.Config) (string, error) {
			_, model, err := judgeWithFallback(cfg, "prompt")
			return model, err
		}},
		{"词义", func(cfg llm.Config) (string, error) {
			_, model, err := llmChat(cfg, "prompt")
			return model, err
		}},
		{"视觉", func(cfg llm.Config) (string, error) {
			_, model, _, err := visionChat(visionConfig{Endpoint: cfg.Endpoint, Model: cfg.Model, Fallback: cfg.Fallback, Timeout: cfg.Timeout}, "prompt", testLLMImageURL)
			return model, err
		}},
	}
	for _, source := range callers {
		for _, target := range callers {
			if source.name == target.name {
				continue
			}
			t.Run(source.name+"到"+target.name, func(t *testing.T) {
				primary := testLLMHealthModel(t, "primary")
				backup := testLLMHealthModel(t, "backup")
				srv1, asked1 := fakeLLMServer(t, map[string]string{primary: "http500"})
				srv2, asked2 := fakeLLMServer(t, nil)
				cfg := llm.Config{Endpoint: srv1.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
				for i := 0; i < 2; i++ {
					if model, err := source.call(cfg); err != nil || model != backup {
						t.Fatalf("来源调用应回退备用：model=%q，err=%v", model, err)
					}
					if got := llm.SkipModel(primary, llmHealthTestTime); got != (i == 1) {
						t.Fatalf("第 %d 次失败后冷却状态不符：%v", i+1, got)
					}
				}
				assertLLMRequests(t, asked1, primary, backup, primary, backup)
				cfg.Endpoint = srv2.URL
				if model, err := target.call(cfg); err != nil || model != backup {
					t.Fatalf("不同 endpoint 的另一调用仍应共享冷却：model=%q，err=%v", model, err)
				}
				assertLLMRequests(t, asked2, backup)
			})
		}
	}
}

func TestLLMHealthAccumulatesFailuresAcrossCalls(t *testing.T) {
	primary := testLLMHealthModel(t, "primary")
	backup := testLLMHealthModel(t, "backup")
	judgeServer, judgeAsked := fakeLLMServer(t, map[string]string{primary: "garbage"})
	textServer, textAsked := fakeLLMServer(t, map[string]string{primary: "http500"})
	visionServer, visionAsked := fakeLLMServer(t, map[string]string{backup: "vision"})
	cfg := llm.Config{Endpoint: judgeServer.URL, Model: primary, Timeout: 3 * time.Second}
	if _, _, err := judgeWithFallback(cfg, "prompt"); err == nil {
		t.Fatal("判句的非空但不可解析 JSON 回答必须记作失败")
	}
	if llm.SkipModel(primary, llmHealthTestTime) {
		t.Fatal("第一次判句解析失败还不应冷却")
	}
	cfg.Endpoint = textServer.URL
	if _, _, err := llmChat(cfg, "prompt"); err == nil {
		t.Fatal("词义调用的 5xx 必须返回失败")
	}
	if !llm.SkipModel(primary, llmHealthTestTime) {
		t.Fatal("判句解析失败与另一 endpoint 的词义失败必须累计至冷却阈值")
	}
	_, model, retryable, err := visionChat(visionConfig{Endpoint: visionServer.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}, "prompt", testLLMImageURL)
	if err != nil || model != backup || retryable {
		t.Fatalf("视觉调用应跳过共享冷却模型并成功：model=%q，retryable=%v，err=%v", model, retryable, err)
	}
	assertLLMRequests(t, judgeAsked, primary)
	assertLLMRequests(t, textAsked, primary)
	assertLLMRequests(t, visionAsked, backup)
}

func TestLLMChatSuccessBeforeDomainParsing(t *testing.T) {
	cases := []struct {
		name  string
		call  func(llm.Config) (string, string, error)
		parse func(string) error
	}{
		{"词义", func(cfg llm.Config) (string, string, error) {
			return llmChat(cfg, "prompt")
		}, func(reply string) error {
			_, err := worddef.ParseReply(reply)
			return err
		}},
		{"视觉", func(cfg llm.Config) (string, string, error) {
			reply, model, _, err := visionChat(visionConfig{Endpoint: cfg.Endpoint, Model: cfg.Model, Timeout: cfg.Timeout}, "prompt", testLLMImageURL)
			return reply, model, err
		}, func(reply string) error {
			_, err := parseVisionReply(reply)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			primary := testLLMHealthModel(t, "primary")
			judgeServer, judgeAsked := fakeLLMServer(t, map[string]string{primary: "garbage"})
			rawServer, rawAsked := fakeLLMServer(t, map[string]string{primary: "garbage"})
			cfg := llm.Config{Endpoint: judgeServer.URL, Model: primary, Timeout: 3 * time.Second}
			if _, _, err := judgeWithFallback(cfg, "prompt"); err == nil {
				t.Fatal("相同原文用于判句应解析失败")
			}
			cfg.Endpoint = rawServer.URL
			reply, model, err := tc.call(cfg)
			if err != nil || model != primary || reply != "这个嘛，我觉得差不多" {
				t.Fatalf("非空原文应先按调用成功返回：reply=%q，model=%q，err=%v", reply, model, err)
			}
			if err := tc.parse(reply); err == nil {
				t.Fatal("领域解析应失败，以保护与判句记错时机的差异")
			}
			if llm.SkipModel(primary, llmHealthTestTime) {
				t.Fatal("非空原文调用成功后，领域解析失败不应重新记错")
			}
			cfg.Endpoint = judgeServer.URL
			for i := 0; i < 2; i++ {
				if _, _, err := judgeWithFallback(cfg, "prompt"); err == nil {
					t.Fatal("后续判句的相同原文仍应解析失败")
				}
				if got := llm.SkipModel(primary, llmHealthTestTime); got != (i == 1) {
					t.Fatalf("原文成功应清零共享计数，之后第 %d 次判句失败冷却=%v", i+1, got)
				}
			}
			assertLLMRequests(t, judgeAsked, primary, primary, primary)
			assertLLMRequests(t, rawAsked, primary)
		})
	}
}
