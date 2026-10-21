package main

import (
	"strings"
	"testing"
	"time"
)

// 拍照取词最容易翻车的地方是「一行 OCR 文本怎么切成词 + 释义」：
// 切错会把好词改坏，所以宁可切不出来（释义留空让老师补），也不能瞎猜。
func TestSplitOCRLine(t *testing.T) {
	cases := []struct {
		name, in, word, def string
	}{
		{"全角空格两段", "みず(水)　水", "みず(水)", "水"},
		{"半角空格两段", "たべもの 食物", "たべもの", "食物"},
		{"带序号", "1. がくせい　学生", "がくせい", "学生"},
		{"带圆点项目符号", "・でんわ　电话", "でんわ", "电话"},
		{"只有词", "なまえ", "なまえ", ""},
		{"三段只取首段为词", "いえ　家　房子", "いえ", "家 房子"},
		{"空行", "   ", "", ""},
	}
	for _, c := range cases {
		w, d := splitOCRLine(c.in)
		if w != c.word || d != c.def {
			t.Errorf("%s：splitOCRLine(%q) = (%q,%q)，想要 (%q,%q)",
				c.name, c.in, w, d, c.word, c.def)
		}
	}
}

// 纯符号/纯英文的噪行要滤掉，整段课文（>24 字）也要滤掉 —— 那不是词条。
func TestOCRCandidatesFiltersNoise(t *testing.T) {
	got := ocrCandidates([]string{
		"みず(水)　水",
		"みず(水)　水", // 重复
		"--------",
		"ABC 123",
		"これはわたしの教科書です。今日は新しい言葉をたくさん習いました。",
	}, 0)
	if len(got) != 1 {
		t.Fatalf("应该只剩 1 个候选，实际 %d 个：%+v", len(got), got)
	}
	if got[0].Word != "みず(水)" || got[0].Def != "水" {
		t.Errorf("候选不对：%+v", got[0])
	}
}

func TestHasJapanese(t *testing.T) {
	if !hasJapanese("みず") || !hasJapanese("水") || !hasJapanese("ミズ") {
		t.Error("日文应该被认出来")
	}
	if hasJapanese("ABC 123 ---") {
		t.Error("纯符号不该算日文")
	}
}

// 签名是手写的，改坏了只能到线上才发现 —— 用腾讯云文档里的固定例子钉住形状。
func TestSignTC3Shape(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	got := signTC3("AKIDTEST", "SECRETTEST", "ocr", `{"ImageBase64":"x"}`, ts)
	for _, want := range []string{
		"TC3-HMAC-SHA256 Credential=AKIDTEST/",
		"/ocr/tc3_request",
		"SignedHeaders=content-type;host",
		"Signature=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("签名缺 %q：%s", want, got)
		}
	}
	// 同样的输入必须得到同样的签名（时间戳也固定了）
	if again := signTC3("AKIDTEST", "SECRETTEST", "ocr", `{"ImageBase64":"x"}`, ts); again != got {
		t.Error("同一份输入签名不一致")
	}
	// 换一个 key 必须不一样
	if other := signTC3("AKIDTEST", "OTHER", "ocr", `{"ImageBase64":"x"}`, ts); other == got {
		t.Error("换了密钥签名居然一样")
	}
}

// 没配密钥时接口要「功能关掉」而不是炸掉（跟 LLM 一个套路）。
func TestOCREnabledFollowsEnv(t *testing.T) {
	t.Setenv("JRP_OCR_SECRET_ID", "")
	t.Setenv("JRP_OCR_SECRET_KEY", "")
	if ocrEnabled() {
		t.Error("没配密钥时 ocrEnabled() 应为 false")
	}
	t.Setenv("JRP_OCR_SECRET_ID", "AKIDTEST")
	t.Setenv("JRP_OCR_SECRET_KEY", "SECRETTEST")
	if !ocrEnabled() {
		t.Error("配了密钥 ocrEnabled() 应为 true")
	}
}
