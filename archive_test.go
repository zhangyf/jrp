package main

import (
	"strings"
	"testing"
)

// 2026-09-24 事故：词表表头行出现在 |--- 之前，解析时被当"正文"塞进 RawFooter，
// 写出时原样保留 —— 每次保存 footer 多 58 行（=分组数），积到 1 万行。
// 回归测试：带垃圾 footer 的档案解析→写出→再解析，词数不变且 footer 不再增长。
func TestParseArchiveGarbageFooterShrinks(t *testing.T) {
	garbage := strings.Repeat("|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n", 10) +
		strings.Repeat("|||||||\n", 5)

	mk := func() string {
		var b strings.Builder
		b.WriteString("## 📋 版本历史\n\n")
		b.WriteString("|日期|版本|总词数|🟢已掌握|🟡基本掌握|🔴待巩固|🔄待测试|累计答错|钉子户|变化内容|\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
		b.WriteString("|09/24|v1.0|2|0|0|0|2|0|0|测试|\n")
		b.WriteString("\n\n")
		b.WriteString("# 日语单词学习进度档案\n\n")
		b.WriteString("## 📅 单词列表（按课分组）\n\n")
		b.WriteString("### 第1课 测试\n\n")
		b.WriteString("|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		b.WriteString("|すし|寿司|3|1|2|09/23|🟡基本掌握|\n")
		b.WriteString("### 第2课 测试\n\n")
		b.WriteString("|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		b.WriteString("|はし(橋)|桥|2|0|2|09/22|🟡基本掌握|\n")
		b.WriteString("\n")
		b.WriteString(garbage)
		return b.String()
	}

	// 第一轮解析+写出
	arc1, err := ParseArchive(mk(), "ja")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got := len(AllWords(arc1.Groups)); got != 2 {
		t.Fatalf("第一轮词数 = %d, want 2", got)
	}
	out1 := WriteArchive(arc1)
	if strings.Count(out1, "|日语单词|中文释义|") != 2 {
		t.Fatalf("第一轮写出后表头行数 = %d, want 2（每个分组恰好一个）",
			strings.Count(out1, "|日语单词|中文释义|"))
	}

	// 第二轮解析+写出：footer 必须稳定，不再增长
	arc2, err := ParseArchive(out1, "ja")
	if err != nil {
		t.Fatalf("第二轮解析失败：%v", err)
	}
	if got := len(AllWords(arc2.Groups)); got != 2 {
		t.Fatalf("第二轮词数 = %d, want 2（垃圾行不能被当词）", got)
	}
	out2 := WriteArchive(arc2)
	if got := strings.Count(out2, "|日语单词|中文释义|"); got != 2 {
		t.Fatalf("第二轮写出后表头行数 = %d, want 2（footer 不能每次写涨 2 行）", got)
	}
	if strings.Count(out2, "|||||||") > 0 {
		t.Fatalf("全空行不应被保留进 footer")
	}
}
