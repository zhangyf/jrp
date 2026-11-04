package textutil

import "testing"

// 去围栏是判句 / 查释义 / 拍照取词三域解析模型回复的第一道，行为必须逐字锁住：
// 收敛前 worddef / vision 各一份私有副本，收敛后共用这一份，不许改行为。
func TestStripCodeFence(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"纯 JSON 原样", `[{"word":"みず"}]`, `[{"word":"みず"}]`},
		{"小写 json 围栏", "```json\n[1]\n```", "[1]"},
		{"大写 JSON 围栏", "```JSON\n[1]\n```", "[1]"},
		{"无语言标记围栏", "```\n[1]\n```", "[1]"},
		{"围栏外前后空白", "  ```json\n[1]\n```  ", "[1]"},
		{"围栏后跟废话", "```json\n[1]\n```\n希望能帮到你。", "[1]"},
		{"只有开围栏", "```json\n[1]", "[1]"},
		{"前后废话无围栏", "好的：\n[1]\n希望有帮助。", "好的：\n[1]\n希望有帮助。"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripCodeFence(c.in); got != c.want {
				t.Errorf("StripCodeFence(%q) = %q，想要 %q", c.in, got, c.want)
			}
		})
	}
}
