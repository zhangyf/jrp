package main

import "testing"

// 模型爱在 JSON 外面套 ```json 和前后废话，抠 JSON 这层必须稳。
func TestParseWordDefReply(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string // 期望 みず(水) 的释义
	}{
		{"纯 JSON", `{"みず(水)": "水"}`, "水"},
		{"代码块", "```json\n{\"みず(水)\": \"水\"}\n```", "水"},
		{"前后废话", "好的，结果如下：\n{\"みず(水)\": \"水\"}\n希望有帮助。", "水"},
		{"多个词", `{"みず(水)": "水", "たべもの": "食物"}`, "水"},
	}
	for _, c := range cases {
		got, err := parseWordDefReply(c.reply)
		if err != nil {
			t.Errorf("%s：解析失败：%v", c.name, err)
			continue
		}
		if got["みず(水)"] != c.want {
			t.Errorf("%s：みず(水) = %q，想要 %q", c.name, got["みず(水)"], c.want)
		}
	}

	if _, err := parseWordDefReply("我没有找到答案"); err == nil {
		t.Error("没有 JSON 时应该报错")
	}
}

func TestFillWordDefPrompt(t *testing.T) {
	p := fillWordDefPrompt(wordDefPrompt, []string{"みず(水)", "たべもの"})
	if !contains(p, "- みず(水)") || !contains(p, "- たべもの") {
		t.Error("词条没进 prompt：" + p)
	}
	if contains(p, "{words}") {
		t.Error("占位符没替换")
	}
	// prompt 里的规则不能丢 —— 少一条模型就开始给例句和词性
	if !contains(p, "顿号并列") {
		t.Error("释义格式规则丢了")
	}
}

func TestMissingWords(t *testing.T) {
	got := missingWords([]string{"a", "b", "c"}, map[string]string{"a": "1"})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("missing = %v，想要 [b c]", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
