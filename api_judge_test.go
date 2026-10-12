package main

import (
	"encoding/json"
	"strings"
	"testing"
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
			got, err := parseJudgeReply(c.reply)
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

	if _, err := parseJudgeReply("完全没有 JSON"); err == nil {
		t.Error("非 JSON 回复必须报错，不能默认判对")
	}
	if _, err := parseJudgeReply(`{"reason": "没给 correct"}`); err == nil {
		t.Error("缺 correct 字段必须报错")
	}
}

// 没配密钥就不启用 —— 这是「默认关闭」的底线：老师还没定模型之前，
// 造句必须照常能练，不能因为没配就报错。
func TestLoadLLMConfigDisabledWithoutEnv(t *testing.T) {
	t.Setenv("JRP_LLM_ENDPOINT", "")
	t.Setenv("JRP_LLM_API_KEY", "")
	_, ok := loadLLMConfig()
	if ok {
		t.Fatal("没配 endpoint/key 却启用了")
	}

	t.Setenv("JRP_LLM_ENDPOINT", "https://api.deepseek.com/v1/chat/completions")
	_, ok = loadLLMConfig()
	if ok {
		t.Fatal("只有 endpoint 没有 key 也不该启用")
	}
}

func TestLoadLLMConfigReadsEnv(t *testing.T) {
	t.Setenv("JRP_LLM_ENDPOINT", "https://api.deepseek.com/v1/chat/completions")
	t.Setenv("JRP_LLM_API_KEY", "sk-test")
	t.Setenv("JRP_LLM_MODEL", "deepseek-chat")
	t.Setenv("JRP_LLM_TIMEOUT", "5")
	c, ok := loadLLMConfig()
	if !ok {
		t.Fatal("配齐了却没启用")
	}
	if c.Model != "deepseek-chat" || c.Timeout.Seconds() != 5 {
		t.Errorf("got model=%q timeout=%v", c.Model, c.Timeout)
	}
}

// 缓存键必须走归一化：全角「２」和半角「2」是同一句，不能问两次模型，
// 更不能给出两个不同结论。
func TestJudgeCacheKeyFoldsWidth(t *testing.T) {
	a := judgeCacheKey("v1", "2万円です", "２万円です。")
	b := judgeCacheKey("v1", "2万円です", "2万円です")
	if a != b {
		t.Errorf("归一化后应是同一个键：%q vs %q", a, b)
	}
	if judgeCacheKey("v1", "甲", "乙") == judgeCacheKey("v1", "甲", "甲") {
		t.Error("不同的输入不能撞键")
	}
	// prompt 改了必须换键：否则新规矩下还拿旧结论复用
	if judgeCacheKey("v1", "甲", "乙") == judgeCacheKey("v2", "甲", "乙") {
		t.Error("模板版本变了缓存键必须跟着变")
	}
}

// prompt 里的规则不能被人误删或改残：
//   - 删掉「感叹词不算错」「没学过的表达也算对」→ 模型变成语法警察，
//     老师会被判一堆本来会的句子是错的（2026-09-29 真出过）。
//   - 助词这条必须是「缺失判错、替换仍判对」两半都在 ——
//     只留一半就走极端：要么太松（省了を也算对，等于没练到），
//     要么太严（へ↔に 都成立也被判错，冤枉）。
func TestJudgePromptKeepsConservativeRules(t *testing.T) {
	p := judgePrompt("明日、京都へ行きます", "明天去京都", "あした京都に行く")
	for _, must := range []string{
		"只判断意思是否成立",
		"汉字与假名",
		"助词缺失判错",                 // 不能退回「助词省略也算对」的老版本
		"换成另一个同样成立的",            // 助词替换仍判对，不许一刀切全错
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

// 请求体必须显式 stream:false —— tokenhub 这类网关不写就按 SSE 返回，
// 判定会全线拿不到内容。
func TestBuildLLMRequestNonStreaming(t *testing.T) {
	cfg := llmConfig{Endpoint: "https://tokenhub.tencentmaas.com/v1/chat/completions",
		APIKey: "sk-x", Model: "deepseek-v4-flash-0731"}
	b, err := buildLLMRequest(cfg, "判定一下")
	if err != nil {
		t.Fatalf("buildLLMRequest: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if v, ok := m["stream"]; !ok || v != false {
		t.Errorf("stream 必须是 false，got %#v（缺了它网关会走 SSE）", m["stream"])
	}
	if m["model"] != "deepseek-v4-flash-0731" {
		t.Errorf("model 不对: %#v", m["model"])
	}
	msgs, ok := m["messages"].([]interface{})
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages 应为 1 条，got %#v", m["messages"])
	}
	msg := msgs[0].(map[string]interface{})
	if msg["role"] != "user" || !strings.Contains(msg["content"].(string), "判定一下") {
		t.Errorf("messages[0] 内容不对: %#v", msg)
	}
	// 密钥绝不能出现在请求体里
	if strings.Contains(string(b), cfg.APIKey) {
		t.Errorf("请求体里不该出现密钥: %s", b)
	}
}
