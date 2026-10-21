package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// 拍照取词：调腾讯云「通用印刷体识别」认图片里的日文。
//
// 选 GeneralBasicOCR 是因为它 LanguageType=jap 直接支持日语（高精度版要
// ConfigID=MulOCR 才走多语种，且更贵），老师拍的是课本生词表，基础版够了。
//
// ⚠️ 签名是手写的 TC3-HMAC-SHA256，没引 tencentcloud-sdk-go —— 只用这一个
// 接口，不值得把整棵依赖树拉进来（内网/代理环境下拉依赖经常失败）。
//
// 密钥读环境变量 JRP_OCR_SECRET_ID / JRP_OCR_SECRET_KEY（放 /etc/jrp/jrp.env），
// 没配就 ocrEnabled() == false —— 跟 LLM 一个套路：功能关掉，不报错。

const (
	ocrHost     = "ocr.tencentcloudapi.com"
	ocrEndpoint = "https://" + ocrHost + "/"
	ocrAction   = "GeneralBasicOCR"
	ocrVersion  = "2018-11-19"
	ocrRegion   = "ap-beijing"
	// 接口限制：base64 后 ≤10M；我们自己再收紧一点，手机原图太大没必要
	ocrMaxImageBytes = 6 << 20
	ocrMaxImages     = 9 // 微信 chooseMedia 一次最多也是 9 张
)

type ocrCredentials struct {
	SecretID  string
	SecretKey string
}

func ocrEnabled() bool {
	return os.Getenv("JRP_OCR_SECRET_ID") != "" && os.Getenv("JRP_OCR_SECRET_KEY") != ""
}

func ocrCreds() *ocrCredentials {
	return &ocrCredentials{
		SecretID:  os.Getenv("JRP_OCR_SECRET_ID"),
		SecretKey: os.Getenv("JRP_OCR_SECRET_KEY"),
	}
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// signTC3 生成腾讯云 API 3.0 的 Authorization 头。
func signTC3(secretID, secretKey, service, payload string, ts time.Time) string {
	date := ts.UTC().Format("2006-01-02")
	canonicalHeaders := "content-type:application/json\nhost:" + ocrHost + "\n"
	signedHeaders := "content-type;host"

	canonicalRequest := strings.Join([]string{
		"POST", "/", "",
		canonicalHeaders,
		signedHeaders,
		sha256hex([]byte(payload)),
	}, "\n")

	scope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		fmt.Sprintf("%d", ts.Unix()),
		scope,
		sha256hex([]byte(canonicalRequest)),
	}, "\n")

	k := hmacSHA256([]byte("TC3"+secretKey), date)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "tc3_request")

	return "TC3-HMAC-SHA256 Credential=" + secretID + "/" + scope +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + hex.EncodeToString(hmacSHA256(k, stringToSign))
}

// OCRCandidate 是从图片里认出来的一个候选词。
type OCRCandidate struct {
	Word       string `json:"word"`
	Def        string `json:"def"`
	Raw        string `json:"raw"`
	Confidence int    `json:"confidence"`
}

type ocrAPIResponse struct {
	Response struct {
		TextDetections []struct {
			DetectedText string `json:"DetectedText"`
			Confidence   int    `json:"Confidence"`
		} `json:"TextDetections"`
		Language  string `json:"Language"`
		RequestID string `json:"RequestId"`
		Error     *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	} `json:"Response"`
}

// tencentOCR 识别一张图，返回原始文本行。
func tencentOCR(ctx context.Context, cred *ocrCredentials, imageBase64 string) ([]string, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"ImageBase64":  imageBase64,
		"LanguageType": "jap",
	})
	if err != nil {
		return nil, err
	}

	ts := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ocrEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", ocrHost)
	req.Header.Set("X-TC-Action", ocrAction)
	req.Header.Set("X-TC-Version", ocrVersion)
	req.Header.Set("X-TC-Region", ocrRegion)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", ts.Unix()))
	req.Header.Set("Authorization", signTC3(cred.SecretID, cred.SecretKey, "ocr", string(payload), ts))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	var parsed ocrAPIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("OCR 返回解析失败：%w", err)
	}
	if parsed.Response.Error != nil {
		return nil, fmt.Errorf("OCR 报错 %s：%s", parsed.Response.Error.Code, parsed.Response.Error.Message)
	}

	lines := make([]string, 0, len(parsed.Response.TextDetections))
	for _, d := range parsed.Response.TextDetections {
		if strings.TrimSpace(d.DetectedText) != "" {
			lines = append(lines, d.DetectedText)
		}
	}
	return lines, nil
}

var ocrBulletRe = regexp.MustCompile(`^[\s　]*[0-9０-９①-⑳一二三四五六七八九十]+[.、,，)）:：]\s*`)
var ocrBulletChars = "・•●○▪◆※・"

// hasJapanese 判断字符串里有没有日文（假名或汉字）—— 用来滤掉纯符号/纯英文的噪行。
func hasJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

// splitOCRLine 把一行 OCR 文本拆成「词 + 中文释义」。
//
// 拍课本生词表时一行通常是「みず(水)　水」或「水　水」这种两段式，
// 中间是空格/全角空格/tab。拆不出来就把整行当词，释义留空让老师在手机上补
// —— 猜错的代价（把一个好词改坏）比猜不出大。
func splitOCRLine(raw string) (word, def string) {
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, ocrBulletChars+" \t　")
	s = ocrBulletRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}

	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '　'
	})
	if len(fields) >= 2 && hasJapanese(fields[0]) {
		return fields[0], strings.Join(fields[1:], " ")
	}
	return s, ""
}

// ocrCandidates 把若干张图的识别结果整理成候选词表（去重、去噪）。
func ocrCandidates(lines []string, confidence int) []OCRCandidate {
	out := make([]OCRCandidate, 0, len(lines))
	seen := map[string]bool{}
	for _, ln := range lines {
		w, d := splitOCRLine(ln)
		if w == "" || !hasJapanese(w) {
			continue
		}
		if len([]rune(w)) > 24 { // 整段课文，不是词条
			continue
		}
		key := w + "\t" + d
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, OCRCandidate{Word: w, Def: d, Raw: ln, Confidence: confidence})
	}
	return out
}
