package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 请求体必须显式 stream:false —— tokenhub 这类网关不写就按 SSE 返回，
// 判定会全线拿不到内容。
func TestBuildLLMRequestNonStreaming(t *testing.T) {
	cfg := Config{Endpoint: "https://tokenhub.tencentmaas.com/v1/chat/completions",
		APIKey: "sk-x", Model: "deepseek-v4-flash-0731"}
	b, err := buildRequest(cfg, "判定一下")
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if v, ok := m["stream"]; !ok || v != false {
		t.Errorf("stream 必须是 false，got %#v（缺了它网关会走 SSE）", m["stream"])
	}
	if m["model"] != "deepseek-v4-flash-0731" {
		t.Errorf("model 不对: %#v", m["model"])
	}
	msgs, ok := m["messages"].([]interface{})
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages 应为 1 条，got %#v", m["messages"])
	}
	msg := msgs[0].(map[string]interface{})
	if msg["role"] != "user" || !strings.Contains(msg["content"].(string), "判定一下") {
		t.Errorf("messages[0] 内容不对: %#v", msg)
	}
	// 密钥绝不能出现在请求体里
	if strings.Contains(string(b), cfg.APIKey) {
		t.Errorf("请求体里不该出现密钥: %s", b)
	}
}

func TestCallRequestAndFirstChoice(t *testing.T) {
	cfg := Config{
		Endpoint: "https://llm.test/v1/chat/completions",
		APIKey:   "test-only-api-key",
		Model:    "test-model",
		Timeout:  time.Second,
	}
	prompt := " \t原始提示：\"你好\"\n第二行 <原样>  "
	want := " \n首个回答\t "
	calls := 0
	setTestTransport(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || req.URL.String() != cfg.Endpoint {
			t.Errorf("请求方法或地址不符：%s %s", req.Method, req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer "+cfg.APIKey {
			t.Error("Authorization 应为调用方提供的 Bearer 密钥")
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q，期望 application/json", got)
		}
		defer req.Body.Close()
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("读取请求体失败：%v", err)
		}
		if bytes.Contains(raw, []byte(cfg.APIKey)) {
			t.Error("请求体不应包含 API 密钥")
		}
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("请求体不是合法 JSON：%v", err)
		}
		if body["model"] != cfg.Model || body["temperature"] != float64(0) || body["stream"] != false {
			t.Errorf("请求必须显式包含 model、temperature=0 和 stream=false：%v", body)
		}
		messages, ok := body["messages"].([]interface{})
		if !ok || len(messages) != 1 {
			t.Fatalf("messages 应有一条消息：%v", body["messages"])
		}
		message, ok := messages[0].(map[string]interface{})
		if !ok || message["role"] != "user" || message["content"] != prompt {
			t.Errorf("消息必须保留原始 prompt：%v", messages[0])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"message":{"content":" \n首个回答\t "}},{"message":{"content":"不能返回第二个回答"}}]}`)),
		}, nil
	})

	got, err := Call(cfg, prompt)
	if err != nil || got != want {
		t.Fatalf("Call = (%q, %v)，期望 (%q, nil)", got, err, want)
	}
	if calls != 1 {
		t.Errorf("请求次数 = %d，期望 1", calls)
	}
}

func TestPostPreservesCallerBody(t *testing.T) {
	const original = `{
  "model": "vision-test", "temperature": 0.7, "stream": false,
  "messages": [{"role": "user", "content": [
    {"type": "text", "text": "只透传测试数据"},
    {"type": "image_url", "image_url": {"url": "https://images.invalid/test-only.png"}}
  ]}]
}
`
	body := []byte(original)
	calls := 0
	setTestTransport(t, func(req *http.Request) (*http.Response, error) {
		calls++
		defer req.Body.Close()
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("读取请求体失败：%v", err)
		}
		if !bytes.Equal(raw, []byte(original)) {
			t.Errorf("Post 改写了调用方的视觉请求体：%q", raw)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"透传成功"}}]}`)),
		}, nil
	})

	got, err := Post("https://llm.test/v1/chat/completions", "test-only-key", body, time.Second)
	if err != nil || got != "透传成功" {
		t.Fatalf("Post = (%q, %v)，期望透传成功", got, err)
	}
	if calls != 1 {
		t.Errorf("请求次数 = %d，期望 1；图片地址不应被访问", calls)
	}
}

func TestPostResponseHandling(t *testing.T) {
	valid := `{"choices":[{"message":{"content":"成功"}}]}`
	cases := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr string
	}{
		{name: "below_200", status: 199, body: "失败", wantErr: "模型返回 199：失败"},
		{name: "success_299", status: 299, body: valid, want: "成功"},
		{name: "boundary_300", status: 300, body: "失败", wantErr: "模型返回 300：失败"},
		{name: "bad_request_400", status: 400, body: "非 JSON 错误体", wantErr: "模型返回 400：非 JSON 错误体"},
		{name: "rate_limit_429", status: 429, body: strings.Repeat("错", 120) + "不应出现", wantErr: "模型返回 429：" + strings.Repeat("错", 120)},
		{name: "server_error_500", status: 500, body: strings.Repeat("x", 121), wantErr: "模型返回 500：" + strings.Repeat("x", 120)},
		{name: "invalid_json", status: 200, body: "not JSON", wantErr: "解析模型响应失败："},
		{name: "missing_choices", status: 200, body: `{}`, wantErr: "模型返回空内容"},
		{name: "empty_choices", status: 200, body: `{"choices":[]}`, wantErr: "模型返回空内容"},
		{name: "blank_content", status: 200, body: `{"choices":[{"message":{"content":" \t\n　"}}]}`, wantErr: "模型返回空内容"},
		{name: "empty_first_choice", status: 200, body: `{"choices":[{"message":{"content":""}},{"message":{"content":"后一个非空"}}]}`, wantErr: "模型返回空内容"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedResponseBody{ReadCloser: io.NopCloser(strings.NewReader(tc.body))}
			setTestTransport(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body}, nil
			})
			got, err := Post("https://llm.test/chat", "test-only-key", []byte(`{}`), time.Second)
			if body.closeCalls != 1 {
				t.Errorf("响应体 Close 次数 = %d，期望 1", body.closeCalls)
			}
			if got != tc.want {
				t.Errorf("返回内容 = %q，期望 %q", got, tc.want)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("不应报错：%v", err)
				}
				return
			}
			if tc.name == "invalid_json" {
				var syntaxErr *json.SyntaxError
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) || !errors.As(err, &syntaxErr) {
					t.Fatalf("应返回包装的 JSON 解析错误，得到 %v", err)
				}
			} else if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("错误 = %v，期望 %q", err, tc.wantErr)
			}
		})
	}
}

func TestPostRequestErrors(t *testing.T) {
	networkErr := errors.New("测试网络故障")
	cases := []struct {
		name      string
		endpoint  string
		wantCalls int
	}{
		{name: "invalid_endpoint", endpoint: "://invalid", wantCalls: 0},
		{name: "network_error", endpoint: "https://llm.test/chat", wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			setTestTransport(t, func(*http.Request) (*http.Response, error) {
				calls++
				return nil, networkErr
			})
			got, err := Post(tc.endpoint, "test-only-key", []byte(`{}`), time.Second)
			if err == nil || got != "" {
				t.Fatalf("失败时应返回空内容和错误，得到 (%q, %v)", got, err)
			}
			if calls != tc.wantCalls {
				t.Errorf("请求次数 = %d，期望 %d", calls, tc.wantCalls)
			}
			if tc.wantCalls == 1 && !errors.Is(err, networkErr) {
				t.Errorf("应保留网络错误，得到 %v", err)
			}
		})
	}
}

func TestCallTimeoutCancelsRequest(t *testing.T) {
	observed := make(chan context.Context, 1)
	setTestTransport(t, func(req *http.Request) (*http.Response, error) {
		select {
		case observed <- req.Context():
		default:
			return nil, errors.New("超时测试不应重复发请求")
		}
		// 即使生产代码丢失超时，也有独立上限，不让测试挂住。
		guard := time.NewTimer(2 * time.Second)
		defer guard.Stop()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-guard.C:
			return nil, errors.New("请求 context 未按时取消")
		}
	})
	got, err := Call(Config{
		Endpoint: "https://llm.test/chat",
		APIKey:   "test-only-key",
		Model:    "test-model",
		Timeout:  10 * time.Millisecond,
	}, "超时测试")
	if got != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应返回 context 超时，得到 (%q, %v)", got, err)
	}
	select {
	case ctx := <-observed:
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("请求 context 未因超时结束：%v", ctx.Err())
		}
	default:
		t.Fatal("未观察到请求 context")
	}
}

func TestPostReadErrorClosesBody(t *testing.T) {
	readErr := errors.New("测试响应读取故障")
	reader, writer := io.Pipe()
	if err := writer.CloseWithError(readErr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	body := &trackedResponseBody{ReadCloser: reader}
	setTestTransport(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})
	got, err := Post("https://llm.test/chat", "test-only-key", []byte(`{}`), time.Second)
	if got != "" || !errors.Is(err, readErr) {
		t.Errorf("应保留响应读取错误，得到 (%q, %v)", got, err)
	}
	if body.closeCalls != 1 {
		t.Errorf("读取失败后 Close 次数 = %d，期望 1", body.closeCalls)
	}
}

func TestPostResponseReadLimit(t *testing.T) {
	const limit = 1 << 20
	valid := `{"choices":[{"message":{"content":"成功"}}]}`
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "ignore_garbage_after_limit",
			body: valid + strings.Repeat(" ", limit-len(valid)) + "不可解析的尾部垃圾",
		},
		{
			name:    "truncate_oversized_content",
			body:    `{"choices":[{"message":{"content":"` + strings.Repeat("x", limit+1) + `"}}]}`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedResponseBody{ReadCloser: io.NopCloser(strings.NewReader(tc.body))}
			setTestTransport(t, func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})
			got, err := Post("https://llm.test/chat", "test-only-key", []byte(`{}`), time.Second)
			if body.bytesRead != limit {
				t.Errorf("实际读取 %d 字节，期望恰好 %d", body.bytesRead, limit)
			}
			if body.closeCalls != 1 {
				t.Errorf("响应体 Close 次数 = %d，期望 1", body.closeCalls)
			}
			if tc.wantErr {
				var syntaxErr *json.SyntaxError
				if got != "" || err == nil || !strings.HasPrefix(err.Error(), "解析模型响应失败：") || !errors.As(err, &syntaxErr) {
					t.Fatalf("截断 JSON 应解析失败，得到内容长度 %d、错误 %v", len(got), err)
				}
			} else if err != nil || got != "成功" {
				t.Fatalf("只解析前 1 MiB 应成功，得到 (%q, %v)", got, err)
			}
		})
	}
}

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func setTestTransport(t *testing.T, transport testRoundTripper) {
	t.Helper()
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = previous })
}

type trackedResponseBody struct {
	io.ReadCloser
	bytesRead  int
	closeCalls int
}

func (b *trackedResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytesRead += n
	return n, err
}

func (b *trackedResponseBody) Close() error {
	b.closeCalls++
	return b.ReadCloser.Close()
}
