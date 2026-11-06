package listening

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 金标准：2026-11-06 老师上传的「JLPT N5 20261023」里 ASR 识别错的几句，
// 精校后应该是这些正确文本。拿它验证纯函数层端到端。
func TestParseSRTThenRefine(t *testing.T) {
	src := "1\n00:00:04,020 --> 00:00:07,140\n日本語能力試験。\n\n" +
		"2\n00:00:07,140 --> 00:00:10,340\n懲戒ベルゴ。\n\n" +
		"3\n00:00:11,020 --> 00:00:14,520\nこれからベ号の。\n\n" +
		"4\n00:00:14,520 --> 00:00:18,500\n懲戒試験を始めます。\n"

	cues := ParseSRT(src)
	if len(cues) != 4 {
		t.Fatalf("解析出 %d 句，要 4 句", len(cues))
	}

	// 模拟模型只点名第 2、4 句要改（第 1、3 句模型认为不用改，不回）。
	reply := "2: 聴解。\n4: 聴解試験を始めます。\n"
	changes := ParseRefineReply(reply)
	if len(changes) != 2 {
		t.Fatalf("解析出 %d 处修改，要 2 处：%v", len(changes), changes)
	}

	applied := ApplyRefinements(cues, changes)
	if applied != 2 {
		t.Fatalf("应用了 %d 处，要 2 处", applied)
	}

	// 改的句子文本变了，时间轴不动。
	if cues[1].Text != "聴解。" {
		t.Errorf("第2句文本没改对：%q", cues[1].Text)
	}
	if cues[3].Text != "聴解試験を始めます。" {
		t.Errorf("第4句文本没改对：%q", cues[3].Text)
	}
	if cues[1].Start != 7.140 || cues[1].End != 10.340 {
		t.Errorf("第2句时间轴被改了：start=%v end=%v", cues[1].Start, cues[1].End)
	}
	// 没点名的句子原样保留。
	if cues[0].Text != "日本語能力試験。" || cues[2].Text != "これからベ号の。" {
		t.Errorf("未点名的句子被改动了：%q / %q", cues[0].Text, cues[2].Text)
	}
}

func TestParseRefineReply全角数字与全角冒号(t *testing.T) {
	got := ParseRefineReply("１２：青いのはあります。\n13: 泡はあります。\n")
	if got[12] != "青いのはあります。" {
		t.Errorf("全角编号/冒号没解析对：%v", got)
	}
	if got[13] != "泡はあります。" {
		t.Errorf("半角编号没解析对：%v", got)
	}
}

func TestParseRefineReply忽略噪声(t *testing.T) {
	// 模型偶尔会输出代码块、解释、"无"——这些都要跳过，不能当成句子改。
	reply := "```json\n1: 正确的句子。\n这是我的解释。\n无\n```\n"
	got := ParseRefineReply(reply)
	if len(got) != 1 || got[1] != "正确的句子。" {
		t.Errorf("噪声没过滤干净：%v", got)
	}
}

func TestApplyRefinements越界编号忽略(t *testing.T) {
	cues := []Cue{{Start: 1, End: 2, Text: "あ"}}
	// 编号 5 越界（只有 1 句），0 是非法编号，都该被忽略。
	applied := ApplyRefinements(cues, map[int]string{5: "x", 0: "y", 1: "い"})
	if applied != 1 {
		t.Fatalf("应只应用 1 处，实际 %d", applied)
	}
	if cues[0].Text != "い" {
		t.Errorf("合法编号没应用：%q", cues[0].Text)
	}
}

func TestApplyRefinements相同文本不计入(t *testing.T) {
	cues := []Cue{{Start: 1, End: 2, Text: "あ"}}
	// 模型回的跟原文一样 → 不算改动，也不覆盖。
	applied := ApplyRefinements(cues, map[int]string{1: "あ"})
	if applied != 0 {
		t.Fatalf("相同文本不应计入改动，实际 %d", applied)
	}
}

func TestRenderSRTRoundTrip(t *testing.T) {
	cues := []Cue{
		{Start: 4.02, End: 7.14, Text: "日本語能力試験。"},
		{Start: 7.14, End: 10.34, Text: "聴解。"},
	}
	out := RenderSRT(cues)
	// 拼回去再解析，cue 数和文本、时间都得对得上。
	again := ParseSRT(out)
	if len(again) != 2 {
		t.Fatalf("round-trip 后 %d 句，要 2 句", len(again))
	}
	if again[0].Text != "日本語能力試験。" || again[1].Text != "聴解。" {
		t.Errorf("文本 round-trip 不一致：%q / %q", again[0].Text, again[1].Text)
	}
	if again[0].Start != 4.02 || again[1].End != 10.34 {
		t.Errorf("时间轴 round-trip 不一致：%v / %v", again[0].Start, again[1].End)
	}
	if !strings.Contains(out, "00:00:04,020 --> 00:00:07,140") {
		t.Errorf("时间码格式不对：\n%s", out)
	}
}

func TestBuildRefineInput带编号不带时间(t *testing.T) {
	cues := []Cue{
		{Start: 4.02, End: 7.14, Text: "あ"},
		{Start: 7.14, End: 10.34, Text: "い"},
	}
	in := BuildRefineInput(cues, 0, 2)
	if !strings.Contains(in, "[1] あ") || !strings.Contains(in, "[2] い") {
		t.Errorf("编号格式不对：\n%s", in)
	}
	// 时间轴绝不能出现在喂给模型的输入里（模型不该看到、也不该改）。
	if strings.Contains(in, "00:00") || strings.Contains(in, "-->") {
		t.Errorf("输入里泄漏了时间轴：\n%s", in)
	}
}

func TestBuildRefineInput分批切片(t *testing.T) {
	cues := make([]Cue, 5)
	for i := range cues {
		cues[i] = Cue{Start: float64(i), End: float64(i + 1), Text: string(rune('あ' + i))}
	}
	// 取第 3~5 句（下标 2..5），编号应从 3 开始。
	in := BuildRefineInput(cues, 2, 5)
	if !strings.Contains(in, "[3]") || !strings.Contains(in, "[5]") {
		t.Errorf("分批切片编号没接上：\n%s", in)
	}
	if strings.Contains(in, "[1]") || strings.Contains(in, "[2]") {
		t.Errorf("分批把前面的句子也带进来了：\n%s", in)
	}
}

func TestFormatClock边界(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "00:00:00,000"},
		{4.02, "00:00:04,020"},
		{65, "00:01:05,000"},
		{3661.5, "01:01:01,500"},
		{-1, "00:00:00,000"}, // 负数夹到 0
	}
	for _, c := range cases {
		if got := formatClock(c.in); got != c.want {
			t.Errorf("formatClock(%v) = %q，要 %q", c.in, got, c.want)
		}
	}
}

// ---- prompt 外置：Validate / Choose / Version / Resolve ----

func TestValidateRefinePrompt(t *testing.T) {
	// 好的：非空 + 含「编号」
	if err := ValidateRefinePrompt("你是精校助手。\n输出格式：编号: 文本"); err != nil {
		t.Errorf("合法模板被拒：%v", err)
	}
	// 空的
	if err := ValidateRefinePrompt("   \n\t "); err == nil {
		t.Error("空模板应该被拒")
	}
	// 缺输出格式说明（没有「编号」）
	if err := ValidateRefinePrompt("你是精校助手，请改正错误。"); err == nil {
		t.Error("缺「编号」说明的模板应该被拒，否则模型回的格式解析不了")
	}
}

func TestChooseRefinePrompt(t *testing.T) {
	// COS 读到了合法模板 → 用 COS 的
	tpl, src := ChooseRefinePrompt("合法模板，编号: 文本", nil)
	if src != "cos" || tpl != "合法模板，编号: 文本" {
		t.Errorf("应该用 COS 模板，got src=%s tpl=%q", src, tpl)
	}
	// 读失败 → 回退内置
	tpl, src = ChooseRefinePrompt("", errors.New("boom"))
	if src != "builtin" || tpl != RefinePrompt {
		t.Errorf("读失败应回退内置，got src=%s", src)
	}
	// 空串 → 回退内置
	_, src = ChooseRefinePrompt("", nil)
	if src != "builtin" {
		t.Errorf("空串应回退内置，got src=%s", src)
	}
	// 校验不过 → 回退内置（模板里不含「编号」二字）
	_, src = ChooseRefinePrompt("请改正错误并输出结果", nil)
	if src != "builtin" {
		t.Errorf("校验不过应回退内置，got src=%s", src)
	}
}

func TestRefinePromptVersion(t *testing.T) {
	v1 := RefinePromptVersion("模板A")
	v2 := RefinePromptVersion("模板B")
	v3 := RefinePromptVersion("模板A")
	if len(v1) != 12 {
		t.Errorf("版本应是 sha256 前 12 位，got len=%d", len(v1))
	}
	if v1 != v3 {
		t.Error("同一内容版本应稳定")
	}
	if v1 == v2 {
		t.Error("不同内容版本应不同")
	}
}

// fakeRefineStore 假的 COS 读，用来测 60 秒缓存装载。
type fakeRefineStore struct {
	text string
	err  error
}

func (f *fakeRefineStore) DownloadRefinePrompt(context.Context) (string, error) {
	return f.text, f.err
}

func TestResolveRefinePrompt(t *testing.T) {
	// 缓存是包级全局，先测 COS 命中，再测回退（顺序影响缓存，所以拆开断言来源正确即可）。
	s := &fakeRefineStore{text: "COS 上的模板，编号: 文本"}
	tpl, version, source := ResolveRefinePrompt(context.Background(), s)
	if source != "cos" || !strings.Contains(tpl, "COS 上的模板") || version == "" {
		t.Errorf("应命中 COS 模板，got source=%s tpl=%q version=%q", source, tpl, version)
	}
}
