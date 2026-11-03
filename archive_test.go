package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/review"
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
	if got := len(review.AllWords(arc1.Groups)); got != 2 {
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
	if got := len(review.AllWords(arc2.Groups)); got != 2 {
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

func TestReviewTypeAliasesJSON(t *testing.T) {
	// 切片双向直接赋值，编译期保护类型别名；不约束迁包后的类型元数据。
	var words []Word
	var reviewWords []review.Word = words
	words = reviewWords
	var groups []WordGroup
	var reviewGroups []review.WordGroup = groups
	groups = reviewGroups

	word := Word{
		Word: "はし(橋)", Definition: "桥", ReviewCount: 6, ErrorCount: 1,
		ConsecutiveCorrect: 3, LastReview: "01/02", Status: "\U0001f7e1基本掌握", Group: "第2课",
	}
	cases := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{
			name: "单词全字段", value: word,
			expected: `{"Word":"はし(橋)","Definition":"桥","ReviewCount":6,"ErrorCount":1,"ConsecutiveCorrect":3,"LastReview":"01/02","Status":"` + "\U0001f7e1基本掌握" + `","Group":"第2课"}`,
		},
		{
			name: "单词零值", value: Word{},
			expected: `{"Word":"","Definition":"","ReviewCount":0,"ErrorCount":0,"ConsecutiveCorrect":0,"LastReview":"","Status":"","Group":""}`,
		},
		{
			name: "分组零值及nil切片", value: WordGroup{},
			expected: `{"Title":"","Words":null}`,
		},
		{
			name: "分组空切片", value: WordGroup{Title: "空组", Words: []Word{}},
			expected: `{"Title":"空组","Words":[]}`,
		},
		{
			name: "分组嵌套单词", value: WordGroup{Title: "第2课", Words: []Word{word}},
			expected: `{"Title":"第2课","Words":[{"Word":"はし(橋)","Definition":"桥","ReviewCount":6,"ErrorCount":1,"ConsecutiveCorrect":3,"LastReview":"01/02","Status":"` + "\U0001f7e1基本掌握" + `","Group":"第2课"}]}`,
		},
	}
	for _, tc := range cases {
		data, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatalf("%s序列化失败：%v", tc.name, err)
		}
		if string(data) != tc.expected {
			t.Errorf("%s JSON = %s，预期 %s", tc.name, data, tc.expected)
		}
	}
}

func TestArchiveReviewRoundTrip(t *testing.T) {
	const source = "# 日语单词学习进度档案\n\n" +
		"## \U0001f4c5 单词列表（按课分组）\n\n" +
		"### 第2课 水与季节\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|みず(水)|水|4|0|4|01/01|\U0001f7e1基本掌握|\n" +
		"|なつ(夏)|夏天|0|0|0||\U0001f504待测试|\n\n" +
		"### 第1课 交通\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|はし(橋)|桥|3|1|2|12/31|\U0001f7e1基本掌握|\n"
	arc, err := ParseArchive(source, "ja")
	if err != nil {
		t.Fatal(err)
	}
	expectedBefore := []WordGroup{
		{Title: "第2课 水与季节", Words: []Word{
			{Word: "みず(水)", Definition: "水", ReviewCount: 4, ErrorCount: 0, ConsecutiveCorrect: 4, LastReview: "01/01", Status: "\U0001f7e1基本掌握", Group: "第2课 水与季节"},
			{Word: "なつ(夏)", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: "\U0001f504待测试", Group: "第2课 水与季节"},
		}},
		{Title: "第1课 交通", Words: []Word{
			{Word: "はし(橋)", Definition: "桥", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "12/31", Status: "\U0001f7e1基本掌握", Group: "第1课 交通"},
		}},
	}
	if !reflect.DeepEqual(arc.Groups, expectedBefore) {
		t.Fatalf("首次解析的分组、顺序或字段不符：got %+v，预期 %+v", arc.Groups, expectedBefore)
	}
	correct, gi := review.FindWord(arc.Groups, "みず(水)")
	if gi != 0 || correct != &arc.Groups[0].Words[0] {
		t.Fatal("FindWord 必须返回第一组原元素指针")
	}
	wrong, gi := review.FindWord(arc.Groups, "はし(橋)")
	if gi != 1 || wrong != &arc.Groups[1].Words[0] {
		t.Fatal("FindWord 必须返回第二组原元素指针")
	}
	date := time.Date(2020, time.January, 3, 12, 0, 0, 0, time.UTC)
	review.RecordCorrect(correct, date)
	review.RecordWrong(wrong, date)
	expectedAfter := []WordGroup{
		{Title: "第2课 水与季节", Words: []Word{
			{Word: "みず(水)", Definition: "水", ReviewCount: 5, ErrorCount: 0, ConsecutiveCorrect: 5, LastReview: "01/03", Status: "\U0001f7e2已掌握", Group: "第2课 水与季节"},
			{Word: "なつ(夏)", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: "\U0001f504待测试", Group: "第2课 水与季节"},
		}},
		{Title: "第1课 交通", Words: []Word{
			{Word: "はし(橋)", Definition: "桥", ReviewCount: 4, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "01/03", Status: "\U0001f534待巩固", Group: "第1课 交通"},
		}},
	}
	if !reflect.DeepEqual(arc.Groups, expectedAfter) {
		t.Fatalf("原档案回写字段不符：got %+v，预期 %+v", arc.Groups, expectedAfter)
	}
	out := WriteArchive(arc)
	// 只比较词表，不把动态“最后更新”或非目标的空组切片形态锁死。
	const expectedTables = "### 第2课 水与季节\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|みず(水)|水|5|0|5|01/03|\U0001f7e2已掌握|\n" +
		"|なつ(夏)|夏天|0|0|0||\U0001f504待测试|\n\n\n" +
		"### 第1课 交通\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|はし(橋)|桥|4|2|0|01/03|\U0001f534待巩固|"
	_, tables, found := strings.Cut(out, "## \U0001f4c5 单词列表（按课分组）\n\n")
	if !found || strings.TrimSpace(tables) != expectedTables {
		t.Fatalf("写出的词表必须保持七列及原顺序：\n%s", tables)
	}
	roundTrip, err := ParseArchive(out, "ja")
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Language != "ja" || !reflect.DeepEqual(roundTrip.Groups, expectedAfter) {
		t.Fatalf("再次解析字段不符：got %+v，预期 %+v", roundTrip, expectedAfter)
	}
}
