package main

import (
	"errors"
	"strings"
	"testing"
)

// 内置模板渲染：占位符换成实际句子，中文那行按中文在不在决定留不留。
func TestFillJudgePrompt(t *testing.T) {
	p := fillJudgePrompt(builtinJudgePrompt, "明日、京都へ行きます", "明天去京都", "あした京都に行く")
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
	p2 := fillJudgePrompt(builtinJudgePrompt, "甲", "", "乙")
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
		err := validateJudgePrompt(c.tpl)
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
	got := ensureJSONRule(bare)
	if !strings.Contains(got, `"correct"`) {
		t.Error("没提 JSON 的模板必须补上输出格式要求")
	}
	// 已经写了输出要求的，不许再追加一遍
	withRule := bare + "\n只输出一行 JSON：{\"correct\": true}"
	if ensureJSONRule(withRule) != withRule {
		t.Error("已经有 JSON 要求时不该再追加")
	}
}

// 选择逻辑：COS 的坏了就回退内置，任何情况下都不能给一份没法用的模板。
func TestChooseJudgePrompt(t *testing.T) {
	good := "你是日语老师。\n参考答案：{answer}\n学生写的：{input}"

	tpl, src := chooseJudgePrompt("", nil)
	if src != "builtin" || tpl != builtinJudgePrompt {
		t.Error("COS 上没有时应该用内置模板")
	}
	tpl, src = chooseJudgePrompt("", errors.New("网络炸了"))
	if src != "builtin" {
		t.Error("COS 读失败时应该用内置模板")
	}
	tpl, src = chooseJudgePrompt("     ", nil)
	if src != "builtin" {
		t.Error("空模板应该用内置")
	}
	tpl, src = chooseJudgePrompt("这句话对吗？{answer}", nil)
	if src != "builtin" {
		t.Error("缺 {input} 的坏模板必须回退内置")
	}
	tpl, src = chooseJudgePrompt(good, nil)
	if src != "cos" || tpl != good {
		t.Error("正常模板应该用 COS 上的")
	}
}

// 版本：内容改一个字版本就得变，草稿里的旧结论据此作废重判。
func TestPromptVersion(t *testing.T) {
	a := promptVersion(builtinJudgePrompt)
	if a == "" || len(a) != 12 {
		t.Fatalf("版本格式不对：%q", a)
	}
	if a != promptVersion(builtinJudgePrompt) {
		t.Error("同一份模板版本必须稳定")
	}
	if a == promptVersion(builtinJudgePrompt+"\n7. 例外的例外。") {
		t.Error("模板改了版本必须变")
	}
}

// 端到端：外置模板走完整链路渲染出来的 prompt，占位符换掉、JSON 要求补上。
func TestRenderJudgePromptWithCustomTemplate(t *testing.T) {
	tpl := "判断一下。\n参考：{answer}\n中文：{chinese}\n学生：{input}"
	p := renderJudgePrompt(tpl, "甲", "乙", "丙")
	if !strings.Contains(p, "参考：甲") || !strings.Contains(p, "学生：丙") {
		t.Errorf("占位符没渲染：%q", p)
	}
	if !strings.Contains(p, `"correct"`) {
		t.Error("外置模板也必须带上 JSON 输出要求")
	}
}
