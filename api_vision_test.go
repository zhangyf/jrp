package main

import (
	"errors"
	"strings"
	"testing"
)

// 模型回话的形状比想象中野：套代码块、前后废话、给成对象包数组。
// 抠不出来 = 老师在手机上看到「没认出词条」，所以这层必须稳。
func TestParseVisionReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  int // 期望候选数
		first string
	}{
		{"纯数组", `[{"word":"みず(水)","def":"水"},{"word":"たべもの","def":"食物"}]`, 2, "みず(水)"},
		{"代码块", "```json\n[{\"word\":\"みず(水)\",\"def\":\"水\"}]\n```", 1, "みず(水)"},
		{"前后废话", "好的，结果如下：\n[{\"word\":\"みず(水)\",\"def\":\"水\"}]\n希望能帮到你。", 1, "みず(水)"},
		{"对象包数组", `{"words":[{"word":"みず(水)","def":"水"}]}`, 1, "みず(水)"},
		{"items 字段", `{"items":[{"word":"がくせい","def":"学生"}]}`, 1, "がくせい"},
	}
	for _, c := range cases {
		got, err := parseVisionReply(c.reply)
		if err != nil {
			t.Errorf("%s：解析失败：%v", c.name, err)
			continue
		}
		if len(got) != c.want {
			t.Errorf("%s：得到 %d 个候选，想要 %d（%+v）", c.name, len(got), c.want, got)
			continue
		}
		if got[0].Word != c.first {
			t.Errorf("%s：首个候选 %q，想要 %q", c.name, got[0].Word, c.first)
		}
	}

	if _, err := parseVisionReply("我看不清这张图"); err == nil {
		t.Error("没有 JSON 时应该报错，否则前端会当成「认出 0 个」")
	}
}

// 去噪是这条链路的保险丝：模型偶尔会把例句、标题也塞进数组。
func TestCleanVisionCandidatesFilters(t *testing.T) {
	got := cleanVisionCandidates([]VisionCandidate{
		{Word: "みず(水)", Def: "水"},
		{Word: "みず(水)", Def: "水"},   // 重复
		{Word: "", Def: "空的"},       // 空词
		{Word: "--------", Def: ""}, // 纯符号
		{Word: "これはわたしのほんです。今日は新しい言葉を習いました", Def: ""}, // 整句
	})
	if len(got) != 1 {
		t.Fatalf("应该只剩 1 个，实际 %d：%+v", len(got), got)
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

// 视觉模型不配就是不可用，但不能当成服务器错误 —— 小程序要显示成人话。
func TestLoadVisionConfigDisabledWithoutGateway(t *testing.T) {
	t.Setenv("JRP_LLM_ENDPOINT", "")
	t.Setenv("JRP_LLM_API_KEY", "")
	if _, ok := loadVisionConfig(); ok {
		t.Error("没配网关时 loadVisionConfig 应返回不可用")
	}

	t.Setenv("JRP_LLM_ENDPOINT", "https://example.invalid/v1/chat/completions")
	t.Setenv("JRP_LLM_API_KEY", "sk-test")
	t.Setenv("JRP_VISION_MODEL", "")
	cfg, ok := loadVisionConfig()
	if !ok {
		t.Fatal("配了网关就该可用")
	}
	if cfg.Model != visionDefaultModel {
		t.Errorf("没配 JRP_VISION_MODEL 时应该落到默认 %s，实际 %s", visionDefaultModel, cfg.Model)
	}
	// 默认备胎必须也是视觉模型，不能是文本模型（文本模型吃不了图）
	if got := strings.Join(cfg.Fallback, ","); got != visionDefaultFallback {
		t.Errorf("默认备胎应为 %s，实际 %s", visionDefaultFallback, got)
	}
}

// 报错得说人话：网关那串 permission_error / does not exist 甩给老师等于没说。
func TestVisionHumanError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errVisionTooLarge, "图太大，压缩后再试"},
		{errors.New(`模型返回 402：{"error":{"code":"401007","message":"No free trial quota"}}`), "这个视觉模型没额度了"},
		{errors.New(`模型返回 400：The model or service ID xxx does not exist.`), "模型名不对（已记下，换备用模型）"},
		{errors.New(`Post "x": context deadline exceeded`), "识别超时，图可能太大，重拍一张清楚点的"},
	}
	for _, c := range cases {
		if got := visionHumanError(c.err); got != c.want {
			t.Errorf("visionHumanError(%v) = %q，想要 %q", c.err, got, c.want)
		}
	}
}

// 402/400 是确定性失败，重试只是白等；超时和 5xx 才值得再试一次。
func TestVisionRetryable(t *testing.T) {
	if visionRetryable(errors.New("模型返回 402：no quota")) {
		t.Error("402 不该重试")
	}
	if visionRetryable(errors.New("模型返回 400：does not exist")) {
		t.Error("400 不该重试")
	}
	if visionRetryable(errVisionTooLarge) {
		t.Error("图太大不该重试")
	}
	if !visionRetryable(errors.New("context deadline exceeded")) {
		t.Error("超时应该重试")
	}
	if !visionRetryable(errors.New("模型返回 502：gateway down")) {
		t.Error("5xx 应该重试")
	}
}
