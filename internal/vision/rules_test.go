package vision

import (
	"encoding/json"
	"strings"
	"testing"
)

// 模型回话的形状比想象中野：套代码块、前后废话、给成对象包数组。
// 抠不出来 = 老师在手机上看到「没认出词条」，所以这层必须稳。
func TestParseReply(t *testing.T) {
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
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseReply(c.reply)
			if err != nil {
				t.Fatalf("解析失败：%v", err)
			}
			if len(got) != c.want {
				t.Fatalf("得到 %d 个候选，想要 %d（%+v）", len(got), c.want, got)
			}
			if got[0].Word != c.first {
				t.Errorf("首个候选 %q，想要 %q", got[0].Word, c.first)
			}
		})
	}

	if _, err := ParseReply("我看不清这张图"); err == nil {
		t.Error("没有 JSON 时应该报错，否则前端会当成「认出 0 个」")
	}
	if _, err := ParseReply("[]"); err == nil {
		t.Error("空数组也该报错，不能当成认出 0 个还返回成功")
	}
}

// 去噪是这条链路的保险丝：模型偶尔会把例句、标题也塞进数组。
func TestCleanCandidatesFilters(t *testing.T) {
	got := CleanCandidates([]Candidate{
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
	if !HasJapanese("みず") || !HasJapanese("水") || !HasJapanese("ミズ") {
		t.Error("日文应该被认出来")
	}
	if HasJapanese("ABC 123 ---") {
		t.Error("纯符号不该算日文")
	}
}

// 提取规则不能从 prompt 里丢掉 —— 都是实测踩坑换来的（例句/标题、只输出 JSON）。
func TestPromptKeepsExtractionRules(t *testing.T) {
	for _, rule := range []string{
		"只提取词条。例句、课文正文、标题、页码、序号都不要",
		"word 保留图片里的写法（包括括号）",
		"同一个词出现多次只给一条",
		"只输出 JSON 数组，不要解释，不要代码块",
	} {
		if !strings.Contains(Prompt, rule) {
			t.Errorf("prompt 规则丢了：%s", rule)
		}
	}
}

// DTO 的 JSON tag / omitempty 直接决定 HTTP 响应字节，逐字锁住。
func TestDTOJSONBytes(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"Candidate", Candidate{Word: "みず", Def: ""}, `{"word":"みず","def":""}`},
		{"ImageResult 省略空字段", ImageResult{
			Index: 2, OK: true, Candidates: []Candidate{{Word: "みず(水)", Def: "水"}},
		}, `{"index":2,"ok":true,"candidates":[{"word":"みず(水)","def":"水"}]}`},
		{"ImageResult 带错误与模型", ImageResult{
			Index: 0, OK: false, Error: "图太大，压缩后再试", Model: "vision-a", Candidates: []Candidate{},
		}, `{"index":0,"ok":false,"error":"图太大，压缩后再试","model":"vision-a","candidates":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.in)
			if err != nil {
				t.Fatalf("marshal 失败：%v", err)
			}
			if string(b) != c.want {
				t.Errorf("JSON 字节不符：\n got %s\nwant %s", b, c.want)
			}
		})
	}
}
