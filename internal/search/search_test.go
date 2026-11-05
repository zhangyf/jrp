package search

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ている", "ている"},
		{"くろい（黒い）", "くろい(黒い)"}, // 全角括号折成半角
		{"ＡＢＣ１２", "abc12"},     // 全角字母数字折半角 + 小写
		{"みず 水", "みず水"},        // 半角空白去掉
		{"は　が", "はが"},          // 全角空格 U+3000 去掉
		{"甲、乙", "甲、乙"},         // 标点保留（与判分 Norm 的关键差别）
		{"  ＫＵＲＯＩ  ", "kuroi"},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestScoreRanking(t *testing.T) {
	// 搜「ている」：整词 > 前缀 > 包含，前缀优先于纯包含。
	exact := Score("ている", []string{"ている"})
	prefix := Score("ている", []string{"ている形の用法"})
	contains := Score("ている", []string{"〜ている 的两种用法"})
	if !((exact > prefix) && (prefix > contains) && contains > 0) {
		t.Fatalf("前缀排序不对: exact=%d prefix=%d contains=%d", exact, prefix, contains)
	}
	// 「ています」字面搜不到「ている」—— 这类靠语义检索，字面就是不该命中。
	if got := Score("ている", []string{"〜ています"}); got >= 0 {
		t.Errorf("ている 不该字面命中 ています，got %d", got)
	}
}

func TestScoreWidthKanji(t *testing.T) {
	// 真例子：老师打全角「くろい（黒い）」，词表里是半角「くろい(黒い)」。
	if got := Score("くろい（黒い）", []string{"くろい(黒い)"}); got < 0 {
		t.Errorf("全角/半角括号应互相命中, got %d", got)
	}
	if got := Score("みず", []string{"水", "みず"}); got != 100 {
		t.Errorf("应完全相等命中みず, got %d", got)
	}
}

func TestScoreMultiFields(t *testing.T) {
	// 多字段取最高分：假名整词命中应压过释义里的包含命中。
	got := Score("みず", []string{"饮用水等", "みず"})
	if got != 100 {
		t.Errorf("多字段应取最高分 100, got %d", got)
	}
	if Score("", []string{"みず"}) != -1 {
		t.Error("空 query 应不匹配")
	}
	if Score("て", []string{}) != -1 {
		t.Error("无字段应不匹配")
	}
}

func TestLessonFromURI(t *testing.T) {
	ok := "cos://openclaw-backup-tx-1251036673/language-review/ja/knowledge/标准日本语初级上册_第16课知识点.md"
	if n, yes := LessonFromURI(ok); !yes || n != 16 {
		t.Errorf("应解出第16课, got (%d,%v)", n, yes)
	}
	if got := TitleFromURI(ok); got != "第16课" {
		t.Errorf("标题应为第16课, got %q", got)
	}
	// 汇编/速查：解不出课号，标题退化为文件名。
	bad := "cos://b/language-review/ja/knowledge/课本原文汇编_第3单元（基本课文）.md"
	if _, yes := LessonFromURI(bad); yes {
		t.Error("汇编文档不该解出课号")
	}
	if got := TitleFromURI(bad); got != "课本原文汇编_第3单元（基本课文）" {
		t.Errorf("标题应退化为文件名, got %q", got)
	}
}

func TestCleanDocText(t *testing.T) {
	raw := "# 标准日本语初级上册 第16课 知识点\n" +
		"## 〜ています 的两种用法\n" +
		"| 用法 | 说明 |\n|---|---|\n" +
		"| 结果状态 | 动作结束后 {Image_0} 的状态 |\n"
	got := CleanDocText(raw, 0)
	if got == "" {
		t.Fatal("清洗后不该为空")
	}
	for _, banned := range []string{"标准日本语初级上册", "|---", "{Image_0}", "#", "|"} {
		if strings.Contains(got, banned) {
			t.Errorf("清洗结果仍含 %q: %q", banned, got)
		}
	}
	if !strings.Contains(got, "〜ています") || !strings.Contains(got, "结果状态") {
		t.Errorf("正文该保留: %q", got)
	}
	// 截断按 rune 不是字节，且补省略号。
	trunc := CleanDocText(strings.Repeat("水", 300), 40)
	if n := len([]rune(trunc)); n != 41 {
		t.Errorf("截断应为 40 rune + 省略号 = 41, got %d", n)
	}
}

func TestMatchText(t *testing.T) {
	md := "前文一行\n## 助词的用法\nは 和 が 的区别在这里\n后文一行\n"
	snip, ok := MatchText(md, "が 的区别")
	if !ok {
		t.Fatal("字面该命中")
	}
	if !strings.Contains(snip, "助词的用法") {
		t.Errorf("应带上一行上下文, got %q", snip)
	}
	if _, ok := MatchText(md, "ている"); ok {
		t.Error("不该凭空命中")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("ているみず", 3); got != "ている" {
		t.Errorf("中文/假名按 rune 截断应为 ている, got %q", got)
	}
	if got := TruncateRunes("abcdef", 3); got != "abc" {
		t.Errorf("截断应为 abc, got %q", got)
	}
	if got := TruncateRunes("水みず", 0); got != "水みず" {
		t.Errorf("max=0 不截断, got %q", got)
	}
}
