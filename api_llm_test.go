// api_llm_test.go —— 大模型 / 判句 / 视觉 / 词义 / 模型链相关测试（配 api_llm.go、llm_failover.go）
//
// 腿一收尾：把原本平铺在根目录的 5 个 *_test.go 按被测域物理合并到本文件。
// 测试函数体逐字保留，仅对 import 取并集去重；被测行为、断言、测试名均不变。
// 每个分区以「原文件名」标注，便于定位；文件头来源清单列出各源顶层声明。
//
// 来源与顶层声明：
// api_judge_test.go: TestParseJudgeReply, TestJudgeCacheKeyFoldsWidth, TestJudgeCallerCarriesConfig, TestJudgePromptKeepsConservativeRules
// judge_prompt_test.go: TestFillJudgePrompt, TestValidateJudgePrompt, TestEnsureJSONRule, TestChooseJudgePrompt, TestPromptVersion, TestRenderJudgePromptWithCustomTemplate
// api_worddef_test.go: TestParseWordDefReply, TestFillWordDefPrompt, TestHasParen, TestFindWordByKana, contains, indexOf
// api_vision_test.go: TestParseVisionReply, TestCleanVisionCandidatesFilters, TestHasJapanese, TestLoadVisionConfigDisabledWithoutGateway, TestVisionHumanError, TestVisionRetryable, TestVisionChatSkipsCoolingPrimary, TestVisionChatAllModelsCooling, TestVisionChatRecoversAfterCooldown, TestVisionChatRecordsFailures, ... (共 11 个顶层声明)
// llm_failover_test.go: testLLMHealthModel, fakeLLMServer, assertLLMRequests, TestJudgeWithFallbackSwitchesOnFailure, TestLLMHealthSharedAcrossCalls, TestLLMHealthAccumulatesFailuresAcrossCalls, TestLLMChatSuccessBeforeDomainParsing
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/judge"
	"github.com/zhangyf/jrp/internal/llm"
	"github.com/zhangyf/jrp/internal/vision"
	"github.com/zhangyf/jrp/internal/worddef"
)

// ==========================================================================
// 原 api_judge_test.go
// ==========================================================================

func TestParseJudgeReply(t *testing.T) {
	cases := []struct {
		name   string
		reply  string
		want   bool
		reason string
	}{
		{"干干净净的 JSON", `{"correct": true, "reason": "意思一样"}`, true, "意思一样"},
		{"带 json 围栏", "```json\n{\"correct\": false, \"reason\": \"主体错了\"}\n```", false, "主体错了"},
		{"前后有废话", `判定如下：{"correct": true, "reason": "只是换了词序"}，以上。`, true, "只是换了词序"},
		{"字符串形式的布尔", `{"correct": "true", "reason": "x"}`, false, "x"}, // 字符串不是 bool，应报错
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := judge.ParseReply(c.reply)
			if strings.Contains(c.reply, `"correct": "true"`) {
				if err == nil {
					t.Fatalf("字符串布尔应解析失败，got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if got.Correct != c.want || got.Reason != c.reason {
				t.Errorf("got %+v，want correct=%v reason=%s", got, c.want, c.reason)
			}
		})
	}

	if _, err := judge.ParseReply("完全没有 JSON"); err == nil {
		t.Error("非 JSON 回复必须报错，不能默认判对")
	}
	if _, err := judge.ParseReply(`{"reason": "没给 correct"}`); err == nil {
		t.Error("缺 correct 字段必须报错")
	}
}

// 缓存键必须走归一化：全角「２」和半角「2」是同一句，不能问两次模型，
// 更不能给出两个不同结论。
func TestJudgeCacheKeyFoldsWidth(t *testing.T) {
	a := judge.CacheKey("v1", "2万円です", "２万円です。")
	b := judge.CacheKey("v1", "2万円です", "2万円です")
	if a != b {
		t.Errorf("归一化后应是同一个键：%q vs %q", a, b)
	}
	if judge.CacheKey("v1", "甲", "乙") == judge.CacheKey("v1", "甲", "甲") {
		t.Error("不同的输入不能撞键")
	}
	// prompt 改了必须换键：否则新规矩下还拿旧结论复用
	if judge.CacheKey("v1", "甲", "乙") == judge.CacheKey("v2", "甲", "乙") {
		t.Error("模板版本变了缓存键必须跟着变")
	}
}

// judgeCaller 持有模型配置、把它整条主备链交给 judgeWithFallback。
// cfg 从 Caller 接口挪进适配器后，必须证明配置没丢：主模型 5xx 时能切到备用，
// 且返回的结果/模型名跟直接调 judgeWithFallback 完全一致。
func TestJudgeCallerCarriesConfig(t *testing.T) {
	primary := testLLMHealthModel(t, "primary")
	backup := testLLMHealthModel(t, "backup")
	srv, asked := fakeLLMServer(t, map[string]string{primary: "http500"})
	cfg := llm.Config{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}

	var caller judge.Caller = judgeCaller{cfg: cfg}
	res, model, err := caller.Judge("prompt")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if model != backup || !res.Correct || res.Reason != "意思一致" {
		t.Fatalf("适配器应完整复用主备链，得到 model=%s res=%+v", model, res)
	}
	assertLLMRequests(t, asked, primary, backup)
}

// prompt 里的规则不能被人误删或改残：
//   - 删掉「感叹词不算错」「没学过的表达也算对」→ 模型变成语法警察，
//     老师会被判一堆本来会的句子是错的（2026-09-29 真出过）。
//   - 助词这条必须是「缺失判错、替换仍判对」两半都在 ——
//     只留一半就走极端：要么太松（省了を也算对，等于没练到），
//     要么太严（へ↔に 都成立也被判错，冤枉）。
func TestJudgePromptKeepsConservativeRules(t *testing.T) {
	p := judge.JudgePrompt("明日、京都へ行きます", "明天去京都", "あした京都に行く")
	for _, must := range []string{
		"只判断意思是否成立",
		"汉字与假名",
		"助词缺失判错",     // 不能退回「助词省略也算对」的老版本
		"换成另一个同样成立的", // 助词替换仍判对，不许一刀切全错
		"感叹词、语气词、开场填充词",
		"没学过的表达",
		"拿不准时按关键要素是否齐全来判断", // 不是「拿不准就判正确」也不是「拿不准就判错」
		"参考答案：明日、京都へ行きます",
		"学生写的：あした京都に行く",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("prompt 缺少规则/内容 %q", must)
		}
	}
	// 例句必须同时有「省助词判错」和「助词替换判对」两个方向
	for _, must := range []string{"省了 を", "へ 和 に 都成立"} {
		if !strings.Contains(p, must) {
			t.Errorf("prompt 缺少例句 %q", must)
		}
	}
}

// ==========================================================================
// 原 judge_prompt_test.go
// ==========================================================================

// 内置模板渲染：占位符换成实际句子，中文那行按中文在不在决定留不留。
func TestFillJudgePrompt(t *testing.T) {
	p := judge.FillPrompt(judge.BuiltinPrompt, "明日、京都へ行きます", "明天去京都", "あした京都に行く")
	for _, must := range []string{
		"参考答案：明日、京都へ行きます",
		"中文意思：明天去京都",
		"学生写的：あした京都に行く",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("渲染结果缺少 %q", must)
		}
	}
	if strings.Contains(p, "{answer}") || strings.Contains(p, "{input}") {
		t.Error("占位符没替换干净")
	}

	// 中文为空：整行删掉，不留「中文意思：」空壳，也不留连续空行
	p2 := judge.FillPrompt(judge.BuiltinPrompt, "甲", "", "乙")
	if strings.Contains(p2, "中文意思") {
		t.Error("中文为空时不该出现「中文意思」行")
	}
	if strings.Contains(p2, "\n\n\n") {
		t.Error("不该留下连续空行")
	}
}

// 护栏一：模板缺占位符就判坏。缺了 {input}，模型根本看不到学生写的什么。
func TestValidateJudgePrompt(t *testing.T) {
	cases := []struct {
		name string
		tpl  string
		bad  bool
	}{
		{"正常", "参考答案：{answer}\n学生写的：{input}", false},
		{"缺 input", "参考答案：{answer}", true},
		{"缺 answer", "学生写的：{input}", true},
		{"空", "   ", true},
		{"一个占位符都没有", "随便写点什么", true},
	}
	for _, c := range cases {
		err := judge.ValidatePrompt(c.tpl)
		if c.bad && err == nil {
			t.Errorf("%s：应当判坏", c.name)
		}
		if !c.bad && err != nil {
			t.Errorf("%s：不该判坏（%v）", c.name, err)
		}
	}
}

// 护栏二：模板没提 JSON 就自动补输出要求。
// 少了这段模型自由发挥，parseJudgeReply 抠不出结论，判分全线失灵。
func TestEnsureJSONRule(t *testing.T) {
	bare := "参考答案：{answer}\n学生写的：{input}"
	got := judge.EnsureJSONRule(bare)
	if !strings.Contains(got, `"correct"`) {
		t.Error("没提 JSON 的模板必须补上输出格式要求")
	}
	// 已经写了输出要求的，不许再追加一遍
	withRule := bare + "\n只输出一行 JSON：{\"correct\": true}"
	if judge.EnsureJSONRule(withRule) != withRule {
		t.Error("已经有 JSON 要求时不该再追加")
	}
}

// 选择逻辑：COS 的坏了就回退内置，任何情况下都不能给一份没法用的模板。
func TestChooseJudgePrompt(t *testing.T) {
	good := "你是日语老师。\n参考答案：{answer}\n学生写的：{input}"

	tpl, src := judge.ChoosePrompt("", nil)
	if src != "builtin" || tpl != judge.BuiltinPrompt {
		t.Error("COS 上没有时应该用内置模板")
	}
	tpl, src = judge.ChoosePrompt("", errors.New("网络炸了"))
	if src != "builtin" {
		t.Error("COS 读失败时应该用内置模板")
	}
	tpl, src = judge.ChoosePrompt("     ", nil)
	if src != "builtin" {
		t.Error("空模板应该用内置")
	}
	tpl, src = judge.ChoosePrompt("这句话对吗？{answer}", nil)
	if src != "builtin" {
		t.Error("缺 {input} 的坏模板必须回退内置")
	}
	tpl, src = judge.ChoosePrompt(good, nil)
	if src != "cos" || tpl != good {
		t.Error("正常模板应该用 COS 上的")
	}
}

// 版本：内容改一个字版本就得变，草稿里的旧结论据此作废重判。
func TestPromptVersion(t *testing.T) {
	a := judge.PromptVersion(judge.BuiltinPrompt)
	if a == "" || len(a) != 12 {
		t.Fatalf("版本格式不对：%q", a)
	}
	if a != judge.PromptVersion(judge.BuiltinPrompt) {
		t.Error("同一份模板版本必须稳定")
	}
	if a == judge.PromptVersion(judge.BuiltinPrompt+"\n7. 例外的例外。") {
		t.Error("模板改了版本必须变")
	}
}

// 端到端：外置模板走完整链路渲染出来的 prompt，占位符换掉、JSON 要求补上。
func TestRenderJudgePromptWithCustomTemplate(t *testing.T) {
	tpl := "判断一下。\n参考：{answer}\n中文：{chinese}\n学生：{input}"
	p := judge.RenderPrompt(tpl, "甲", "乙", "丙")
	if !strings.Contains(p, "参考：甲") || !strings.Contains(p, "学生：丙") {
		t.Errorf("占位符没渲染：%q", p)
	}
	if !strings.Contains(p, `"correct"`) {
		t.Error("外置模板也必须带上 JSON 输出要求")
	}
}

// ==========================================================================
// 原 api_worddef_test.go
// ==========================================================================

// 模型爱在 JSON 外面套 ```json 和前后废话，抠 JSON 这层必须稳。
func TestParseWordDefReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		word  string
		kanji string
		def   string
	}{
		{"纯 JSON", `[{"word":"みず","kanji":"水","def":"水"}]`, "みず", "水", "水"},
		{"代码块", "```json\n[{\"word\":\"みず\",\"kanji\":\"水\",\"def\":\"水\"}]\n```", "みず", "水", "水"},
		{"前后废话", "好的：\n[{\"word\":\"みず\",\"kanji\":\"水\",\"def\":\"水\"}]\n希望有帮助。", "みず", "水", "水"},
		{"没汉字的词", `[{"word":"さん","kanji":"","def":"称呼"}]`, "さん", "", "称呼"},
	}
	for _, c := range cases {
		got, err := worddef.ParseReply(c.reply)
		if err != nil {
			t.Errorf("%s：解析失败：%v", c.name, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s：得到 %d 条，想要 1 条", c.name, len(got))
			continue
		}
		if got[0].Word != c.word || got[0].Kanji != c.kanji || got[0].Def != c.def {
			t.Errorf("%s：%+v，想要 word=%q kanji=%q def=%q", c.name, got[0], c.word, c.kanji, c.def)
		}
	}

	if _, err := worddef.ParseReply("我没有找到答案"); err == nil {
		t.Error("没有 JSON 时应该报错")
	}
}

func TestFillWordDefPrompt(t *testing.T) {
	p := worddef.FillPrompt(worddef.Prompt, []string{"みず", "たべもの"})
	if !contains(p, "- みず") || !contains(p, "- たべもの") {
		t.Error("词条没进 prompt：" + p)
	}
	if contains(p, "{words}") {
		t.Error("占位符没替换")
	}
	// 要汉字这一条不能从 prompt 里丢掉 —— 老师 2026-10-21 就是发现没补汉字
	if !contains(p, "kanji 填这个词的汉字写法") {
		t.Error("要汉字的规则丢了")
	}
}

// 老师自己写了括号（みず(水) 或 さん（敬称））就别再补一层括号。
func TestHasParen(t *testing.T) {
	if !worddef.HasParen("みず(水)") || !worddef.HasParen("さん（敬称）") {
		t.Error("带括号的词形应该被认出来")
	}
	if worddef.HasParen("みず") {
		t.Error("没括号的不该被认成有")
	}
}

// 老师打「みず」，库里存的是「みず(水)」—— 要能按假名部分找到，把汉字补回来。
func TestFindWordByKana(t *testing.T) {
	groups := []WordGroup{{Title: "g", Words: []Word{
		{Word: "みず(水)", Definition: "水"},
		{Word: "たべもの", Definition: "食物"},
	}}}
	if w := worddef.FindWordByKana(groups, "みず"); w == nil || w.Word != "みず(水)" {
		t.Errorf("按假名没找到 みず(水)：%+v", w)
	}
	if w := worddef.FindWordByKana(groups, "たべもの"); w == nil || w.Word != "たべもの" {
		t.Errorf("没汉字的词也要能找到：%+v", w)
	}
	if w := worddef.FindWordByKana(groups, "でんわ"); w != nil {
		t.Error("库里没有的不该找到")
	}
}

func contains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ==========================================================================
// 原 api_vision_test.go
// ==========================================================================

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
		got, err := vision.ParseReply(c.reply)
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

	if _, err := vision.ParseReply("我看不清这张图"); err == nil {
		t.Error("没有 JSON 时应该报错，否则前端会当成「认出 0 个」")
	}
}

// 去噪是这条链路的保险丝：模型偶尔会把例句、标题也塞进数组。
func TestCleanVisionCandidatesFilters(t *testing.T) {
	got := vision.CleanCandidates([]VisionCandidate{
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
	if !vision.HasJapanese("みず") || !vision.HasJapanese("水") || !vision.HasJapanese("ミズ") {
		t.Error("日文应该被认出来")
	}
	if vision.HasJapanese("ABC 123 ---") {
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

func TestVisionChatSkipsCoolingPrimary(t *testing.T) {
	primary := testLLMHealthModel(t, "primary")
	backup := testLLMHealthModel(t, "backup")
	llm.MarkResult(primary, false, llmCoolingTestTime)
	llm.MarkResult(primary, false, llmCoolingTestTime)
	srv, asked := fakeLLMServer(t, map[string]string{primary: "vision", backup: "vision"})
	cfg := visionConfig{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
	reply, model, retryable, err := visionChat(cfg, "prompt", testLLMImageURL)
	if err != nil || reply != `[{"word":"みず(水)","def":"水"}]` || model != backup || retryable {
		t.Fatalf("应跳过冷却主模型并使用备用：reply=%q，model=%q，retryable=%v，err=%v", reply, model, retryable, err)
	}
	assertLLMRequests(t, asked, backup)
	if !llm.SkipModel(primary, llmCoolingTestTime) {
		t.Fatal("跳过主模型不得清零它的冷却状态")
	}
}

// 当前实现没有 force 分支；全链冷却时返回错误，不应擅自访问网关。
func TestVisionChatAllModelsCooling(t *testing.T) {
	primary := testLLMHealthModel(t, "primary")
	backup := testLLMHealthModel(t, "backup")
	for _, model := range []string{primary, backup} {
		llm.MarkResult(model, false, llmCoolingTestTime)
		llm.MarkResult(model, false, llmCoolingTestTime)
	}
	srv, asked := fakeLLMServer(t, map[string]string{primary: "vision", backup: "vision"})
	cfg := visionConfig{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
	reply, model, retryable, err := visionChat(cfg, "prompt", testLLMImageURL)
	if err == nil || err.Error() != "所有视觉模型都在冷却中" || reply != "" || model != "" || retryable {
		t.Fatalf("全链冷却应不发请求并返回不可重试的冷却错误：reply=%q，model=%q，retryable=%v，err=%v", reply, model, retryable, err)
	}
	assertLLMRequests(t, asked)
	for _, model := range []string{primary, backup} {
		if !llm.SkipModel(model, llmCoolingTestTime) {
			t.Fatalf("未调用的模型冷却状态不得被重置：%s", model)
		}
	}
}

func TestVisionChatRecoversAfterCooldown(t *testing.T) {
	primary := testLLMHealthModel(t, "primary")
	backup := testLLMHealthModel(t, "backup")
	llm.MarkResult(primary, false, llmHealthTestTime)
	llm.MarkResult(primary, false, llmHealthTestTime)
	if !llm.SkipModel(primary, llmHealthTestTime) || llm.SkipModel(primary, llmHealthTestTime.Add(2*time.Minute)) {
		t.Fatal("测试前置条件：主模型曾进入冷却，且在 2 分钟后到期")
	}
	srv, asked := fakeLLMServer(t, map[string]string{primary: "vision", backup: "vision"})
	cfg := visionConfig{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
	reply, model, retryable, err := visionChat(cfg, "prompt", testLLMImageURL)
	if err != nil || reply == "" || model != primary || retryable {
		t.Fatalf("冷却过期应重新尝试并恢复主模型：reply=%q，model=%q，retryable=%v，err=%v", reply, model, retryable, err)
	}
	if llm.SkipModel(primary, llmHealthTestTime) {
		t.Fatal("视觉调用成功必须清除旧冷却状态")
	}
	llm.MarkResult(primary, false, llmHealthTestTime)
	if llm.SkipModel(primary, llmHealthTestTime) {
		t.Fatal("成功后的单次失败不应立即再次冷却，失败计数必须已经清零")
	}
	assertLLMRequests(t, asked, primary)
}

func TestVisionChatRecordsFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		retryable bool
	}{
		{"网关5xx", "http500", true},
		{"模型400", "http400", false},
		{"空白原文", "empty", true},
		{"网关响应JSON损坏", "invalid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary := testLLMHealthModel(t, "primary")
			srv, asked := fakeLLMServer(t, map[string]string{primary: tc.mode})
			cfg := visionConfig{Endpoint: srv.URL, Model: primary, Timeout: 3 * time.Second}
			for i := 0; i < 2; i++ {
				reply, model, retryable, err := visionChat(cfg, "prompt", testLLMImageURL)
				if err == nil || reply != "" || model != "" || retryable != tc.retryable {
					t.Fatalf("失败返回值不符：reply=%q，model=%q，retryable=%v，err=%v", reply, model, retryable, err)
				}
				if got := llm.SkipModel(primary, llmHealthTestTime); got != (i == 1) {
					t.Fatalf("第 %d 次视觉失败后冷却状态=%v", i+1, got)
				}
			}
			_, _, retryable, err := visionChat(cfg, "prompt", testLLMImageURL)
			if err == nil || err.Error() != "所有视觉模型都在冷却中" || retryable {
				t.Fatalf("后续调用应因冷却停止，retryable=%v，err=%v", retryable, err)
			}
			assertLLMRequests(t, asked, primary, primary)
		})
	}
}

func TestVisionChatFallbackRecordsFailureAndSuccess(t *testing.T) {
	primary := testLLMHealthModel(t, "primary")
	backup := testLLMHealthModel(t, "backup")
	llm.MarkResult(primary, false, llmHealthTestTime)
	llm.MarkResult(backup, false, llmHealthTestTime)
	srv, asked := fakeLLMServer(t, map[string]string{primary: "http500", backup: "vision"})
	cfg := visionConfig{Endpoint: srv.URL, Model: primary, Fallback: []string{backup}, Timeout: 3 * time.Second}
	reply, model, retryable, err := visionChat(cfg, "prompt", testLLMImageURL)
	if err != nil || reply == "" || model != backup || retryable {
		t.Fatalf("备用成功应返回成功且不要求重试，即使之前主模型 5xx：reply=%q，model=%q，retryable=%v，err=%v", reply, model, retryable, err)
	}
	if !llm.SkipModel(primary, llmHealthTestTime) {
		t.Fatal("失败主模型应记录第二次失败并进入冷却")
	}
	llm.MarkResult(backup, false, llmHealthTestTime)
	if llm.SkipModel(backup, llmHealthTestTime) {
		t.Fatal("备用成功应清零之前的失败，后续一次失败不能冷却")
	}
	assertLLMRequests(t, asked, primary, backup)
}

// ==========================================================================
// 原 llm_failover_test.go
// ==========================================================================

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
			_, err := vision.ParseReply(reply)
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
