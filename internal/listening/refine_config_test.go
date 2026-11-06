package listening

import (
	"strings"
	"testing"
)

func mkCues(texts ...string) []Cue {
	out := make([]Cue, len(texts))
	for i, t := range texts {
		out[i] = Cue{Start: float64(i), End: float64(i + 1), Text: t}
	}
	return out
}

func TestIsQuestionStart(t *testing.T) {
	yes := []string{"問題一。", "問題１", "問題 2。", "一番。", "二番", "３番", "十番です", "一ばん", "二バン"}
	for _, s := range yes {
		if !IsQuestionStart(s) {
			t.Errorf("%q 应该识别为题目开头", s)
		}
	}
	// 「問題用紙」「問題要旨」后面没有数字，不能被当题号 —— 这是最容易误判的坑。
	no := []string{"問題用紙を配ります。", "問題要旨を挙げてください。", "日本語能力試験。",
		"これは一番いいものです。", "質問を聞いてください。", ""}
	for _, s := range no {
		if IsQuestionStart(s) {
			t.Errorf("%q 不该识别为题目开头", s)
		}
	}
}

// 按题目切：一段 = 一道题（题号开头到下一题号之前）。
func TestSplitPartsByQuestion(t *testing.T) {
	cues := mkCues(
		"日本語能力試験。", // 0 前言
		"聴解です。",     // 1
		"問題一。",      // 2 → 新段（大题）
		"一番。",       // 3 → 新段（小题）
		"男の人と女の人が話しています。",
		"質問を聞いてください。",
		"二番。", // 6 → 新段
		"女の人はどこへ行きますか。",
	)
	cfg := BuiltinRefineConfig()
	parts := SplitParts(cues, cfg)
	if len(parts) != 4 {
		t.Fatalf("应切出 4 段，实际 %d：%+v", len(parts), parts)
	}
	want := [][2]int{{0, 2}, {2, 3}, {3, 6}, {6, 8}}
	for i, w := range want {
		if parts[i].Start != w[0] || parts[i].End != w[1] {
			t.Errorf("第 %d 段 = [%d,%d)，要 [%d,%d)", i, parts[i].Start, parts[i].End, w[0], w[1])
		}
	}
	if parts[2].Label != "一番。" {
		t.Errorf("段标签应取首句，got %q", parts[2].Label)
	}
}

// 识别不出题号（非 JLPT 素材）：退回按句数硬切。
func TestSplitPartsFallbackByCount(t *testing.T) {
	texts := make([]string, 10)
	for i := range texts {
		texts[i] = "普通の文です。"
	}
	cfg := BuiltinRefineConfig()
	cfg.MaxCuesPerPart = 3
	parts := SplitParts(mkCues(texts...), cfg)
	// 10 句 / 3 = 3+3+3+1
	if len(parts) != 4 {
		t.Fatalf("应切 4 段，实际 %d", len(parts))
	}
	if parts[0].End != 3 || parts[3].Start != 9 || parts[3].End != 10 {
		t.Errorf("句数兜底切分不对：%+v", parts)
	}
}

// by_question=false：即使有题号也按句数切。
func TestSplitPartsDisabled(t *testing.T) {
	byQ := false
	cfg := BuiltinRefineConfig()
	cfg.ByQuestion = &byQ
	cfg.MaxCuesPerPart = 2
	parts := SplitParts(mkCues("一番。", "あ。", "二番。", "い。"), cfg)
	if len(parts) != 2 {
		t.Fatalf("应切 2 段，实际 %d：%+v", len(parts), parts)
	}
}

// 单题过长时仍要按上限硬切，防止一段大到一个请求跑不完。
func TestSplitPartsCapsLongQuestion(t *testing.T) {
	texts := []string{"一番。"}
	for i := 0; i < 8; i++ {
		texts = append(texts, "長い会話です。")
	}
	cfg := BuiltinRefineConfig()
	cfg.MaxCuesPerPart = 4
	parts := SplitParts(mkCues(texts...), cfg)
	if len(parts) < 3 {
		t.Errorf("长题应被上限切开，实际 %d 段：%+v", len(parts), parts)
	}
	for _, p := range parts {
		if p.End-p.Start > 4 {
			t.Errorf("段 [%d,%d) 超过上限 4", p.Start, p.End)
		}
	}
}

func TestParseAndChooseRefineConfig(t *testing.T) {
	// 空 → 内置
	cfg, src := ChooseRefineConfig(nil, nil)
	if src != "builtin" || cfg.MaxCuesPerPart != DefaultMaxCuesPerPart {
		t.Errorf("空配置应回退内置，got src=%s cfg=%+v", src, cfg)
	}
	// 合法 JSON → COS
	cfg, src = ChooseRefineConfig([]byte(`{"by_question":false,"max_cues_per_part":30,"timeout_seconds":200}`), nil)
	if src != "cos" || cfg.MaxCuesPerPart != 30 || cfg.TimeoutSeconds != 200 || *cfg.ByQuestion {
		t.Errorf("合法配置应生效，got src=%s cfg=%+v", src, cfg)
	}
	// 非法 JSON → 内置
	_, src = ChooseRefineConfig([]byte(`{bad`), nil)
	if src != "builtin" {
		t.Errorf("坏 JSON 应回退内置，got %s", src)
	}
	// 越界 → 内置
	_, src = ChooseRefineConfig([]byte(`{"max_cues_per_part":9999}`), nil)
	if src != "builtin" {
		t.Errorf("越界应回退内置，got %s", src)
	}
}

func TestValidateRefineConfig(t *testing.T) {
	if err := ValidateRefineConfig(BuiltinRefineConfig()); err != nil {
		t.Errorf("内置默认不该被拒：%v", err)
	}
	if err := ValidateRefineConfig(RefineConfig{MaxCuesPerPart: 9999}); err == nil {
		t.Error("max_cues_per_part=9999 应被拒")
	}
	if err := ValidateRefineConfig(RefineConfig{TimeoutSeconds: 9999}); err == nil {
		t.Error("timeout_seconds=9999 应被拒")
	}
}

// 归一化把越界夹回合法区间，不报错（后台任务不该因为配置写错整批失败）。
func TestNormalizeRefineConfigClamps(t *testing.T) {
	c := NormalizeRefineConfig(RefineConfig{MaxCuesPerPart: 1, TimeoutSeconds: 9})
	if c.MaxCuesPerPart != 1 || c.TimeoutSeconds != 30 {
		t.Errorf("应夹到 1/30，got %d/%d", c.MaxCuesPerPart, c.TimeoutSeconds)
	}
}

// 内置 prompt 必须保留「编号:」格式说明（ParseRefineReply 依赖它）。
func TestBuiltinRefinePromptHasFormat(t *testing.T) {
	if err := ValidateRefinePrompt(RefinePrompt); err != nil {
		t.Errorf("内置 prompt 校验不过：%v", err)
	}
	if !strings.Contains(RefinePrompt, "一道题") {
		t.Error("内置 prompt 应说明「一段 = 一道题」，否则按题目切分的上下文用不上")
	}
}
