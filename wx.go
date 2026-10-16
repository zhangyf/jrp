package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 微信小程序服务端 API。只有三个调用：code2session（拿 openid）、
// 拿 access_token（画小程序码要用）、getwxacodeunlimit（出二维码图）。
//
// 没有 appid/secret 时全部直接返回「未配置」，不要让它影响日常练习。

const wxAPITimeout = 10 * time.Second

// code2Session 用小程序端 wx.login() 换来的 code 换 openid。
//
// 同一个微信在同一个小程序下 openid 恒定 —— 这就是我们的账号主键。
// 个人主体小程序拿不到手机号、头像昵称也是匿名的，但 openid 一直可用。
func code2Session(ctx context.Context, code string) (openid string, err error) {
	if !wxEnabled() {
		return "", fmt.Errorf("未配置小程序（JRP_WX_APPID / JRP_WX_APPSECRET）")
	}
	if strings.TrimSpace(code) == "" {
		return "", fmt.Errorf("code 为空")
	}

	q := url.Values{}
	q.Set("appid", wxAppID)
	q.Set("secret", wxAppSecret)
	q.Set("js_code", strings.TrimSpace(code))
	q.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.weixin.qq.com/sns/jscode2session?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: wxAPITimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var out struct {
		OpenID     string `json:"openid"`
		SessionKey string `json:"session_key"`
		ErrCode    int    `json:"errcode"`
		ErrMsg     string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("微信返回无法解析：%s", truncate(string(body), 200))
	}
	if out.OpenID == "" {
		return "", fmt.Errorf("code2session 失败（%d %s）", out.ErrCode, out.ErrMsg)
	}
	return out.OpenID, nil
}

// ---------------------------------------------------------------------------
// access_token：官方要求缓存，不能每次都换（有频率限制）。提前 5 分钟过期。
// ---------------------------------------------------------------------------

var wxToken = struct {
	sync.Mutex
	token  string
	expiry time.Time
}{}

func accessToken(ctx context.Context) (string, error) {
	if !wxEnabled() {
		return "", fmt.Errorf("未配置小程序（JRP_WX_APPID / JRP_WX_APPSECRET）")
	}
	wxToken.Lock()
	defer wxToken.Unlock()
	if wxToken.token != "" && time.Now().Before(wxToken.expiry) {
		return wxToken.token, nil
	}

	q := url.Values{}
	q.Set("grant_type", "client_credential")
	q.Set("appid", wxAppID)
	q.Set("secret", wxAppSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.weixin.qq.com/cgi-bin/token?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: wxAPITimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("微信返回无法解析：%s", truncate(string(body), 200))
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("获取 access_token 失败（%d %s）", out.ErrCode, out.ErrMsg)
	}
	wxToken.token = out.AccessToken
	wxToken.expiry = time.Now().Add(time.Duration(out.ExpiresIn)*time.Second - 5*time.Minute)
	return wxToken.token, nil
}

// miniCode 出一张小程序码。scene 里塞的是登录 ticket。
//
// 返回 (图片字节, Content-Type, error)。图是什么格式由微信说了算（现在是 JPEG），
// 别在上层写死 image/png。
//
// ⚠️ scene 硬上限 32 字符，超了微信只回一个没有说明的报错 —— ticket 是 22 字符。
// ⚠️ 小程序必须已经发布，否则报 41030 / invalid page。开发期传 envVersion="trial"
//
//	并 checkPath=false 走体验版。
func miniCode(ctx context.Context, scene, page, envVersion string) ([]byte, string, error) {
	tk, err := accessToken(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(scene) > 32 {
		return nil, "", fmt.Errorf("scene 超过 32 字符（%d），微信会直接拒绝", len(scene))
	}

	payload := map[string]interface{}{
		"scene":      scene,
		"page":       page,
		"width":      430,
		"check_path": false,
	}
	if envVersion != "" {
		payload["env_version"] = envVersion
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.weixin.qq.com/wxa/getwxacodeunlimit?access_token="+url.QueryEscape(tk),
		bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: wxAPITimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	// 成功时直接是图片二进制；失败时是 JSON（errcode/errmsg）。
	ct, err := classifyMiniCodeResp(body, resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", err
	}
	return body, ct, nil
}

// classifyMiniCodeResp 判断出码接口的返回是图还是报错，返回该用的 Content-Type。
//
// ⚠️ 判据是「是不是 JSON」，不是「是不是某种图片格式」：
// getwxacodeunlimit 实际返回的是 **JPEG**（不是文档里容易想当然的 PNG），
// 认死 PNG 头会把正常返回当成错误。微信哪天换格式也不该让我们挂。
func classifyMiniCodeResp(body []byte, respCT string) (string, error) {
	// ① JSON 一律当报错：微信出码失败也回 200，错误只在 body 里。
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &out); err == nil {
		if out.ErrCode != 0 {
			return "", fmt.Errorf("生成小程序码失败（%d %s）", out.ErrCode, out.ErrMsg)
		}
		return "", fmt.Errorf("生成小程序码返回了 JSON 但不是图片：%s", truncate(string(body), 200))
	}
	// ② 认图片魔数（多种格式都认，别认死一种）
	if ct := imageContentType(body); ct != "" {
		if c := strings.TrimSpace(respCT); c != "" {
			return c, nil // 微信自己说了是什么就是什么
		}
		return ct, nil
	}
	// ③ 既不是 JSON 也不是认识的图 —— 多半是网关错误页，别当图片发给前端
	return "", fmt.Errorf("生成小程序码返回了非图片内容：%s", truncate(string(body), 200))
}

// imageContentType 按魔数猜图片格式，认不出来返回 ""。
func imageContentType(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg" // 微信 getwxacodeunlimit 现在给的就是 JPEG
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
