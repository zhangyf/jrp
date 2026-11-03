package archive

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zhangyf/jrp/internal/review"
)

// 状态串与章节标题里的 emoji 全部用 Go 转义，不新增 emoji 字形。
const (
	ptMastered = "\U0001f7e2已掌握"
	ptBasic    = "\U0001f7e1基本掌握"
	ptConsol   = "\U0001f534待巩固"
	ptUntested = "\U0001f504待测试"
)

// 完整样例：头部在版本历史标题之前，词表跨两个分组，含应被过滤的各类行。
const fullArchiveContent = "" +
	"# 日语单词学习进度档案\n" +
	"最后更新：03/15\n" +
	"前言说明：版本历史之前的头部内容\n" +
	"\n" +
	"## \U0001f4cb 版本历史\n" +
	"\n" +
	"|日期|版本|总词数|\U0001f7e2已掌握|\U0001f7e1基本掌握|\U0001f534待巩固|\U0001f504待测试|累计答错|钉子户|变化内容|\n" +
	"|---|---|---|---|---|---|---|---|---|---|\n" +
	"|03/15|v1.2|3|1|1|0|1|2|0|新增分组|\n" +
	"|\u2014|\u2014|\u2014|\u2014|\u2014|\u2014|\u2014|\u2014|\u2014|\u2014|\n" +
	"|||||||||\n" +
	"\n" +
	"|03/16|v1.3|3|1|1|1|0|3|1|补录单词|\n" +
	"\n" +
	"这段说明位于版本历史之后、单词列表之前\n" +
	"\n" +
	"## \U0001f4c5 单词列表（按课分组）\n" +
	"\n" +
	"单词列表 section 内、分组前的说明\n" +
	"### 第1课 基础词\n" +
	"\n" +
	"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
	"|||||||\n" +
	"|---|---|---|---|---|---|---|\n" +
	"|すし|寿司|3|1|2|03/14|\U0001f7e1基本掌握|\n" +
	"|日期|正确|总数|正确率|语速||||\n" +
	"|みず|水|abc|0|0||\U0001f504待测试|\n" +
	"\n" +
	"### 第2课 进阶\n" +
	"\n" +
	"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
	"|---|---|---|---|---|---|---|\n" +
	"|はし(橋)|桥|2|0|2|03/13|\U0001f7e1基本掌握|\n" +
	"\n" +
	"这是词表后的备注\n"

func TestParseArchiveFull(t *testing.T) {
	t.Parallel()
	// 期望值整结构体手写：不只用条数断言。
	want := &Archive{
		Language: "ja",
		Changelog: []ChangelogEntry{
			{Date: "03/15", Version: "v1.2", Total: "3", Mastered: "1", Basic: "1", NeedsConsol: "0", Untested: "1", Errors: "2", NailHouse: "0", Description: "新增分组"},
			{Date: "03/16", Version: "v1.3", Total: "3", Mastered: "1", Basic: "1", NeedsConsol: "1", Untested: "0", Errors: "3", NailHouse: "1", Description: "补录单词"},
		},
		Title:      "日语单词学习进度档案",
		LastUpdate: "03/15",
		Groups: []review.WordGroup{
			{Title: "第1课 基础词", Words: []review.Word{
				{Word: "すし", Definition: "寿司", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "03/14", Status: ptBasic, Group: "第1课 基础词"},
			}},
			{Title: "第2课 进阶", Words: []review.Word{
				{Word: "はし(橋)", Definition: "桥", ReviewCount: 2, ErrorCount: 0, ConsecutiveCorrect: 2, LastReview: "03/13", Status: ptBasic, Group: "第2课 进阶"},
			}},
		},
		// 版本历史标题之前的所有行（含标题、最后更新、说明、空行）都进 RawHeader。
		RawHeader: "# 日语单词学习进度档案\n最后更新：03/15\n前言说明：版本历史之前的头部内容\n",
		// 词表内所有非空、非表格的正文行都进 footer，含第一个分组前的说明。
		RawFooter: "单词列表 section 内、分组前的说明\n这是词表后的备注",
	}

	got, err := ParseArchive(fullArchiveContent, "ja")
	if err != nil {
		t.Fatalf("ParseArchive 报错：%v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseArchive 全字段不符：\n got %+v\nwant %+v", got, want)
	}

	// Legacy（迁包前的既有行为，本批未改）：版本历史之后、单词列表之前的
	// 非表格说明行会让 inChangelog 退出并留在 preChangelogLines，而该缓冲
	// 只在遇到"## 📋 版本历史"时才并入 RawHeader —— 在它之后出现的这类行
	// 因此既不进 RawHeader 也不进 RawFooter。
	// "单词列表 section 前内容不丢"只对版本历史标题之前的头部成立。
	if strings.Contains(got.RawHeader, "这段说明") || strings.Contains(got.RawFooter, "这段说明") {
		t.Error("changelog 与单词列表之间的说明按实现应被丢弃")
	}
	// 表头行与全空行不能出现在 footer。
	if strings.Contains(got.RawFooter, "中文释义") || strings.Contains(got.RawFooter, "|||||||") {
		t.Errorf("词表表头/全空行不应进 RawFooter：%q", got.RawFooter)
	}
}

func TestParseArchiveEmpty(t *testing.T) {
	t.Parallel()
	got, err := ParseArchive("", "fr")
	if err != nil {
		t.Fatalf("空输入不应报错：%v", err)
	}
	// 空输入只带 lang 参数，其余字段全零值。
	want := &Archive{Language: "fr"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("空输入 = %+v, want %+v", got, want)
	}
}

func TestSplitTableRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want []string
	}{
		{name: "前后竖线", line: "|a|b|c|", want: []string{"a", "b", "c"}},
		{name: "单元格首尾空白", line: "|  a  | b\t|  c |", want: []string{"a", "b", "c"}},
		{name: "单列带竖线", line: "|only|", want: []string{"only"}},
		{name: "无竖线单列", line: "only", want: []string{"only"}},
		{name: "空行", line: "", want: []string{""}},
		{name: "全空七竖线", line: "|||||||", want: []string{"", "", "", "", "", ""}},
		{name: "只有前竖线", line: "|a|b", want: []string{"a", "b"}},
		{name: "只有后竖线", line: "a|b|", want: []string{"a", "b"}},
		{name: "中文与数字", line: "|すし|寿司|3|1|2|03/14|状态|", want: []string{"すし", "寿司", "3", "1", "2", "03/14", "状态"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitTableRow(tt.line); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitTableRow(%q) = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseWordRow(t *testing.T) {
	t.Parallel()
	t.Run("列不足7返回nil", func(t *testing.T) {
		for _, line := range []string{"|a|b|", "|a|b|c|d|e|f|", "单个", ""} {
			if got := parseWordRow(line); got != nil {
				t.Errorf("parseWordRow(%q) = %+v, want nil", line, got)
			}
		}
	})
	t.Run("七列全字段", func(t *testing.T) {
		got := parseWordRow("| すし | 寿司 | 3 | 1 | 2 | 03/14 | " + ptBasic + " |")
		want := &review.Word{Word: "すし", Definition: "寿司", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "03/14", Status: ptBasic}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseWordRow = %+v, want %+v", got, want)
		}
	})
	t.Run("Atoi失败归零", func(t *testing.T) {
		// Atoi 的错误被忽略，三个计数位都归零，其余列照常保留。
		got := parseWordRow("|w|d|xx|yy|zz|date|status|")
		want := &review.Word{Word: "w", Definition: "d", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "date", Status: "status"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseWordRow = %+v, want %+v", got, want)
		}
	})
	t.Run("超过7列只取前7", func(t *testing.T) {
		got := parseWordRow("|w|d|1|2|3|date|status|多余列|")
		want := &review.Word{Word: "w", Definition: "d", ReviewCount: 1, ErrorCount: 2, ConsecutiveCorrect: 3, LastReview: "date", Status: "status"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("parseWordRow = %+v, want %+v", got, want)
		}
	})
}

func TestParseChangelogRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want ChangelogEntry
	}{
		{
			name: "十列全字段",
			line: "|03/15|v1.2|6|1|2|2|1|10|3|新增第2课|",
			want: ChangelogEntry{Date: "03/15", Version: "v1.2", Total: "6", Mastered: "1", Basic: "2", NeedsConsol: "2", Untested: "1", Errors: "10", NailHouse: "3", Description: "新增第2课"},
		},
		{
			name: "不足十列补空",
			line: "|a|b|c|",
			want: ChangelogEntry{Date: "a", Version: "b", Total: "c"},
		},
		{
			name: "空行全补空",
			line: "",
			want: ChangelogEntry{},
		},
		{
			name: "超过十列只取前十",
			line: "|a|b|c|d|e|f|g|h|i|j|多余列|",
			want: ChangelogEntry{Date: "a", Version: "b", Total: "c", Mastered: "d", Basic: "e", NeedsConsol: "f", Untested: "g", Errors: "h", NailHouse: "i", Description: "j"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseChangelogRow(tt.line); got != tt.want {
				t.Errorf("parseChangelogRow(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestIsNonWordRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "首列日期", line: "|日期|正确|总数|正确率|语速||||", want: true},
		{name: "首列正确", line: "|正确|x|1|0|0||s|", want: true},
		{name: "首列总数", line: "|总数|x|1|0|0||s|", want: true},
		{name: "首列正确率", line: "|正确率|x|1|0|0||s|", want: true},
		{name: "首列语速", line: "|语速|x|1|0|0||s|", want: true},
		{name: "首列六短横", line: "|------|x|1|0|0||s|", want: true},
		{name: "首列以模式开头也命中", line: "|日期统计|x|1|0|0||s|", want: true},
		{name: "第三列非数字命中", line: "|みず|水|abc|0|0||s|", want: true},
		{name: "正常词行不命中", line: "|すし|寿司|3|1|2|03/14|s|", want: false},
		// splitTableRow 至少返回 1 个元素，len(parts)==0 分支不可达；
		// 列数不足（<3）时跳过数字校验，只有首列命中模式才算非词行。
		{name: "列不足但首列是模式仍命中", line: "|日期|", want: true},
		{name: "列不足且首列非模式不命中", line: "|word|def|", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonWordRow(tt.line); got != tt.want {
				t.Errorf("isNonWordRow(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

// newWriteTestArchive 只构造手写输入，不参与期望值计算。
func newWriteTestArchive() *Archive {
	return &Archive{
		Language: "ja",
		Changelog: []ChangelogEntry{
			// 与词表统计口径一致（手算见 TestWriteArchiveProgressStats）：
			// 🟢1 / 🟡0 / 🔴1 / 🔄0，累计答错 1，钉子户 1。
			{Date: "03/15", Version: "v1.2", Total: "2", Mastered: "1", Basic: "0", NeedsConsol: "1", Untested: "0", Errors: "1", NailHouse: "1", Description: "新增第2课"},
		},
		Title:     "日语单词学习进度档案",
		RawHeader: "这行是原始头部，WriteArchive 不输出",
		RawFooter: "自定义页脚内容",
		Groups: []review.WordGroup{
			{Title: "第1课 基础词", Words: []review.Word{
				{Word: "すし", Definition: "寿司", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "03/14", Status: ptBasic, Group: "第1课 基础词"},
			}},
			{Title: "第2课 进阶", Words: []review.Word{
				{Word: "はし(橋)", Definition: "桥", ReviewCount: 2, ErrorCount: 0, ConsecutiveCorrect: 2, LastReview: "03/13", Status: ptMastered, Group: "第2课 进阶"},
			}},
		},
	}
}

func TestWriteArchive(t *testing.T) {
	t.Parallel()
	out := WriteArchive(newWriteTestArchive(), "日语", "日语单词")

	// 标题：langName 拼进 "# ...单词学习进度档案"。
	if !strings.Contains(out, "# 日语单词学习进度档案") {
		t.Error("输出缺少拼入 langName 的标题")
	}
	// 词表表头首列必须是传入的 wordColumn。
	const wordHeader = "|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|"
	if !strings.Contains(out, wordHeader) {
		t.Errorf("输出缺少以 %q 为首列的词表表头", wordHeader)
	}
	// 紧跟表头的 7 列分隔行（用组合串，避免被 changelog 的 10 列分隔行误命中）。
	if !strings.Contains(out, wordHeader+"\n|---|---|---|---|---|---|---|\n") {
		t.Error("输出缺少紧跟词表表头的 7 列分隔行")
	}
	if got := strings.Count(out, "|日语单词|中文释义|"); got != 2 {
		t.Errorf("词表表头行数 = %d, want 2（每个分组一行）", got)
	}
	// changelog 行按 10 列原样输出。
	if !strings.Contains(out, "|03/15|v1.2|2|1|0|1|0|1|1|新增第2课|") {
		t.Error("输出缺少 changelog 数据行")
	}
	// RawFooter 保留在末尾；RawHeader 完全不输出。
	if !strings.HasSuffix(out, "自定义页脚内容\n") {
		t.Error("RawFooter 未保留在输出末尾")
	}
	if strings.Contains(out, "这行是原始头部") {
		t.Error("WriteArchive 按实现不输出 RawHeader")
	}
	// 不断言 time.Now 生成的"最后更新"日期，也不做整文件全等比较。
	if !strings.Contains(out, "最后更新：") {
		t.Error("输出缺少最后更新行")
	}
}

// TestWriteArchiveProgressStats 专门盯 WriteArchive 里那几行走 review 统计的
// 输出（总体进度段 + 💡 汇总行）。这一段在迁包前没有任何断言，
// 统计口径变了、列串拼错了都不会被发现。
//
// 期望值全部手算，不调用 review.CountByStatus / TotalErrors / CountNailHouseholds：
//   - すし：复习 3、答错 1（错误率 33% ≥ 30%）→ 🔴 待巩固
//   - はし(橋)：状态本身是 🟢已掌握 → 🟢 已掌握
//   - 累计答错 = 1；钉子户 = 1（すし 错误率 33% < 50% 且答错 > 0）
func TestWriteArchiveProgressStats(t *testing.T) {
	t.Parallel()
	out := WriteArchive(newWriteTestArchive(), "日语", "日语单词")

	want := []string{
		"|总单词数|2|",
		"|\U0001f7e2 已掌握（正确率≥85%，≥5次）|1|",
		"|\U0001f7e1 基本掌握（正确率≥70%，≥2次）|0|",
		"|\U0001f534 待巩固（正确率<70%或次）|1|",
		"|累计答错次数|1|",
		"💡 当前2词中：\U0001f7e21 + \U0001f7e10 + \U0001f5341 + \U0001f5040 = 2。" +
			"其中高频钉子户(答错≥5次/正确率<50%): 1词",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("总体进度段缺少 %q", w)
		}
	}
	// 本例没有待测试词，🔄 行按实现只在 untested > 0 时输出。
	if strings.Contains(out, "|\U0001f504 待测试|") {
		t.Error("untested=0 时不应输出待测试行")
	}
}

func TestWriteArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	out := WriteArchive(newWriteTestArchive(), "日语", "日语单词")
	got, err := ParseArchive(out, "ja")
	if err != nil {
		t.Fatalf("往返解析失败：%v", err)
	}

	// 期望值独立手写，不引用输入 arc，也不用被测函数生成。
	wantGroups := []review.WordGroup{
		{Title: "第1课 基础词", Words: []review.Word{
			{Word: "すし", Definition: "寿司", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "03/14", Status: ptBasic, Group: "第1课 基础词"},
		}},
		{Title: "第2课 进阶", Words: []review.Word{
			{Word: "はし(橋)", Definition: "桥", ReviewCount: 2, ErrorCount: 0, ConsecutiveCorrect: 2, LastReview: "03/13", Status: ptMastered, Group: "第2课 进阶"},
		}},
	}
	wantChangelog := []ChangelogEntry{
		{Date: "03/15", Version: "v1.2", Total: "2", Mastered: "1", Basic: "0", NeedsConsol: "1", Untested: "0", Errors: "1", NailHouse: "1", Description: "新增第2课"},
	}
	if !reflect.DeepEqual(got.Groups, wantGroups) {
		t.Errorf("往返后 Groups 不符：\n got %+v\nwant %+v", got.Groups, wantGroups)
	}
	if !reflect.DeepEqual(got.Changelog, wantChangelog) {
		t.Errorf("往返后 Changelog 不符：\n got %+v\nwant %+v", got.Changelog, wantChangelog)
	}
	if got.Language != "ja" {
		t.Errorf("Language = %q, want ja", got.Language)
	}
	if got.Title != "日语单词学习进度档案" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.RawFooter != "自定义页脚内容" {
		t.Errorf("RawFooter = %q", got.RawFooter)
	}
	// 实现细节：WriteArchive 输出里版本历史是首段，之前没有头部行，
	// 且它不序列化 RawHeader，所以往返后 RawHeader 为空。
	if got.RawHeader != "" {
		t.Errorf("RawHeader = %q, want 空", got.RawHeader)
	}
	// LastUpdate 来自 time.Now，只断言非空，不断言具体日期。
	if got.LastUpdate == "" {
		t.Error("LastUpdate 应非空")
	}
}

func TestAppendWord(t *testing.T) {
	t.Parallel()
	arc := &Archive{Language: "ja"}
	steps := []struct {
		name    string
		group   string
		word    review.Word
		wantNew bool
	}{
		{name: "空字符串组名落到未分组并新建", group: "", word: review.Word{Word: "すし", Definition: "寿司", Group: "旧组名应被覆盖"}, wantNew: true},
		{name: "纯空格组名命中已存在的未分组", group: "  ", word: review.Word{Word: "なつ", Definition: "夏天"}, wantNew: false},
		{name: "新建第1课组", group: "第1课", word: review.Word{Word: "みず", Definition: "水"}, wantNew: true},
		{name: "第1课已存在返回false", group: "第1课", word: review.Word{Word: "はし", Definition: "桥", Group: "错误组名"}, wantNew: false},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			if got := arc.AppendWord(s.group, s.word); got != s.wantNew {
				t.Errorf("AppendWord 返回 %v, want %v", got, s.wantNew)
			}
		})
	}
	// 词按组追加且顺序正确，Group 字段一律被覆盖成实际落到的组名。
	want := []review.WordGroup{
		{Title: "未分组", Words: []review.Word{
			{Word: "すし", Definition: "寿司", Group: "未分组"},
			{Word: "なつ", Definition: "夏天", Group: "未分组"},
		}},
		{Title: "第1课", Words: []review.Word{
			{Word: "みず", Definition: "水", Group: "第1课"},
			{Word: "はし", Definition: "桥", Group: "第1课"},
		}},
	}
	if !reflect.DeepEqual(arc.Groups, want) {
		t.Errorf("AppendWord 后 Groups 不符：\n got %+v\nwant %+v", arc.Groups, want)
	}
}

func TestSetDefinition(t *testing.T) {
	t.Parallel()
	arc := &Archive{
		Groups: []review.WordGroup{
			{Title: "第1课", Words: []review.Word{
				{Word: "すし", Definition: "寿司", Group: "第1课"},
				{Word: "はし", Definition: "桥", Group: "第1课"},
			}},
			{Title: "第2课", Words: []review.Word{
				{Word: "はし", Definition: "重复的桥", Group: "第2课"},
			}},
		},
	}
	// 命中：返回 true 且释义变更。
	if !arc.SetDefinition("すし", "寿司（补充）") {
		t.Error("命中时应返回 true")
	}
	if arc.Groups[0].Words[0].Definition != "寿司（补充）" {
		t.Errorf("命中词释义未变更：%q", arc.Groups[0].Words[0].Definition)
	}
	// 未命中：返回 false 且所有释义不变。
	if arc.SetDefinition("不存在", "新释义") {
		t.Error("未命中时应返回 false")
	}
	if arc.Groups[0].Words[1].Definition != "桥" || arc.Groups[1].Words[0].Definition != "重复的桥" {
		t.Error("未命中时不应改动任何释义")
	}
	// 跨组同名只改第一个匹配：第一组的"はし"变更，第二组保持原样。
	if !arc.SetDefinition("はし", "桥（区分）") {
		t.Error("同名命中时应返回 true")
	}
	want := []review.WordGroup{
		{Title: "第1课", Words: []review.Word{
			{Word: "すし", Definition: "寿司（补充）", Group: "第1课"},
			{Word: "はし", Definition: "桥（区分）", Group: "第1课"},
		}},
		{Title: "第2课", Words: []review.Word{
			{Word: "はし", Definition: "重复的桥", Group: "第2课"},
		}},
	}
	if !reflect.DeepEqual(arc.Groups, want) {
		t.Errorf("SetDefinition 后 Groups 不符：\n got %+v\nwant %+v", arc.Groups, want)
	}
}
