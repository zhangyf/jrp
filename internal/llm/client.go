package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// buildRequest 造请求体。抽出来是为了能单测 —— 「stream:false」这条
// 一旦被删，网关可能改走 SSE，判定会全线失败（2026-09-29）。
func buildRequest(cfg Config, prompt string) ([]byte, error) {
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

func Call(cfg Config, prompt string) (string, error) {
	body, err := buildRequest(cfg, prompt)
	if err != nil {
		return "", err
	}
	return Post(cfg.Endpoint, cfg.APIKey, body, cfg.Timeout)
}

// Post 发一次请求，把 choices[0].message.content 取出来。
//
// 判句、查中文释义、拍照取词都走这里 —— 超时、错误码、空内容的处理只有一份。
// body 由调用方拼（视觉要带图片，content 是数组不是字符串，拼不出来统一形状）。
func Post(endpoint, apiKey string, body []byte, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
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
