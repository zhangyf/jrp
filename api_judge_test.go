package main

import (
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/judge"
	"github.com/zhangyf/jrp/internal/llm"
)

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
