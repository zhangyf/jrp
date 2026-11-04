package judge

import (
	"errors"
	"strings"
	"testing"
)

// 判句 prompt 的规则不能被人误删或改残（2026-09-29 真出过）：
//
//   - 删掉「感叹词不算错」「没学过的表达也算对」→ 模型变成语法警察；
//   - 助词这条必须是「缺失判错、替换仍判对」两半都在，只留一半就走极端。
//
// 这条测试跟根包 TestJudgePromptKeepsConservativeRules 是同一个护栏，
// 从用例层再钉一遍：模板搬进本包后一个字都不许变。
func TestBuiltinPromptKeepsConservativeRules(t *testing.T) {
	p := JudgePrompt("明日、京都へ行きます", "明天去京都", "あした京都に行く")
	for _, must := range []string{
		"只判断意思是否成立",
		"汉字与假名",
		"助词缺失判错",
		"换成另一个同样成立的",
		"感叹词、语气词、开场填充词",
		"没学过的表达",
		"拿不准时按关键要素是否齐全来判断",
		"参考答案：明日、京都へ行きます",
		"学生写的：あした京都に行く",
		"省了 を",
		"へ 和 に 都成立",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("prompt 缺少规则/例句 %q", must)
		}
	}
}

func TestFillPrompt(t *testing.T) {
	p := FillPrompt(BuiltinPrompt, "明日、京都へ行きます", "明天去京都", "あした京都に行く")
	for _, must := range []string{"参考答案：明日、京都へ行きます", "中文意思：明天去京都", "学生写的：あした京都に行く"} {
		if !strings.Contains(p, must) {
			t.Errorf("渲染结果缺少 %q", must)
		}
	}
	if strings.Contains(p, "{answer}") || strings.Contains(p, "{input}") {
		t.Error("占位符没替换干净")
	}
	// 中文为空：整行删掉，不留空壳，也不留连续空行。
	p2 := FillPrompt(BuiltinPrompt, "甲", "", "乙")
	if strings.Contains(p2, "中文意思") || strings.Contains(p2, "\n\n\n") {
		t.Error("中文为空时不该出现「中文意思」行或连续空行")
	}
}

func TestValidatePrompt(t *testing.T) {
	cases := []struct {
		tpl string
		bad bool
	}{
		{"参考答案：{answer}\n学生写的：{input}", false},
		{"参考答案：{answer}", true},
		{"学生写的：{input}", true},
		{"   ", true},
		{"随便写点什么", true},
	}
	for _, c := range cases {
		err := ValidatePrompt(c.tpl)
		if c.bad && err == nil {
			t.Errorf("%q：应当判坏", c.tpl)
		}
		if !c.bad && err != nil {
			t.Errorf("%q：不该判坏（%v）", c.tpl, err)
		}
	}
}

func TestEnsureJSONRule(t *testing.T) {
	bare := "参考答案：{answer}\n学生写的：{input}"
	if !strings.Contains(EnsureJSONRule(bare), `"correct"`) {
		t.Error("没提 JSON 的模板必须补上输出格式要求")
	}
	withRule := bare + "\n只输出一行 JSON：{\"correct\": true}"
	if EnsureJSONRule(withRule) != withRule {
		t.Error("已经有 JSON 要求时不该再追加")
	}
}

func TestChoosePrompt(t *testing.T) {
	good := "你是日语老师。\n参考答案：{answer}\n学生写的：{input}"
	if tpl, src := ChoosePrompt("", nil); src != "builtin" || tpl != BuiltinPrompt {
		t.Error("COS 上没有时应该用内置模板")
	}
	if _, src := ChoosePrompt("", errors.New("网络炸了")); src != "builtin" {
		t.Error("COS 读失败时应该用内置模板")
	}
	if _, src := ChoosePrompt("     ", nil); src != "builtin" {
		t.Error("空模板应该用内置")
	}
	if _, src := ChoosePrompt("这句话对吗？{answer}", nil); src != "builtin" {
		t.Error("缺 {input} 的坏模板必须回退内置")
	}
	if tpl, src := ChoosePrompt(good, nil); src != "cos" || tpl != good {
		t.Error("正常模板应该用 COS 上的")
	}
}

func TestPromptVersion(t *testing.T) {
	a := PromptVersion(BuiltinPrompt)
	if a == "" || len(a) != 12 {
		t.Fatalf("版本格式不对：%q", a)
	}
	if a != PromptVersion(BuiltinPrompt) {
		t.Error("同一份模板版本必须稳定")
	}
	if a == PromptVersion(BuiltinPrompt+"\n7. 例外的例外。") {
		t.Error("模板改了版本必须变")
	}
}

func TestRenderPrompt(t *testing.T) {
	tpl := "判断一下。\n参考：{answer}\n中文：{chinese}\n学生：{input}"
	p := RenderPrompt(tpl, "甲", "乙", "丙")
	if !strings.Contains(p, "参考：甲") || !strings.Contains(p, "学生：丙") || !strings.Contains(p, `"correct"`) {
		t.Errorf("外置模板渲染/补 JSON 要求不对：%q", p)
	}
}

func TestParseReply(t *testing.T) {
	cases := []struct {
		reply  string
		want   bool
		reason string
	}{
		{`{"correct": true, "reason": "意思一样"}`, true, "意思一样"},
		{"```json\n{\"correct\": false, \"reason\": \"主体错了\"}\n```", false, "主体错了"},
		{`判定如下：{"correct": true, "reason": "只是换了词序"}，以上。`, true, "只是换了词序"},
	}
	for _, c := range cases {
		got, err := ParseReply(c.reply)
		if err != nil {
			t.Fatalf("解析失败：%v", err)
		}
		if got.Correct != c.want || got.Reason != c.reason {
			t.Errorf("got %+v，want correct=%v reason=%s", got, c.want, c.reason)
		}
	}
	if _, err := ParseReply("完全没有 JSON"); err == nil {
		t.Error("非 JSON 回复必须报错，不能默认判对")
	}
	if _, err := ParseReply(`{"reason": "没给 correct"}`); err == nil {
		t.Error("缺 correct 字段必须报错")
	}
	if _, err := ParseReply(`{"correct": "true", "reason": "x"}`); err == nil {
		t.Error("字符串形式的布尔应报错")
	}
}

func TestCacheKeyFoldsWidth(t *testing.T) {
	if CacheKey("v1", "2万円です", "２万円です。") != CacheKey("v1", "2万円です", "2万円です") {
		t.Error("归一化后应是同一个键")
	}
	if CacheKey("v1", "甲", "乙") == CacheKey("v1", "甲", "甲") {
		t.Error("不同的输入不能撞键")
	}
	if CacheKey("v1", "甲", "乙") == CacheKey("v2", "甲", "乙") {
		t.Error("模板版本变了缓存键必须跟着变")
	}
}
