package lessonimport

import (
	"reflect"
	"testing"

	"github.com/zhangyf/jrp/internal/sentence"
)

// 本文件只补已有测试（lesson_import_test.go）未覆盖的缺口，函数名一律加
// 后缀避免与已有测试同名。断言一律按实现如实写，不为"应该怎样"臆造。

// ---------- 1) UnitOfLesson 的边界 ----------

// 已有 TestUnitOfLesson 覆盖了 1/4/5/12/17/19/20/21/0/-3。这里只补：
// 超大课号、超过 MaxUnit 的课号 —— 实现是纯算术，不按 MaxUnit 封顶。
func TestUnitOfLessonBeyondMaxUnit(t *testing.T) {
	cases := []struct {
		lesson int
		want   int
	}{
		{48, 12},  // (48+3)/4 = 12，正好落在 MaxUnit
		{49, 13},  // 超过 MaxUnit，实现不封顶，照算 13
		{100, 25}, // 超大课号，纯算术
		{2, 1},    // 第1单元内部
		{8, 2},    // (8+3)/4 = 2
	}
	for _, c := range cases {
		if got := UnitOfLesson(c.lesson); got != c.want {
			t.Errorf("UnitOfLesson(%d) = %d, want %d", c.lesson, got, c.want)
		}
	}
}

// ---------- 2) SplitCells ----------

func TestSplitCellsVariants(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"前后竖线", "| a | b |", []string{"a", "b"}},
		{"无外竖线", "a | b", []string{"a", "b"}},
		{"单列", "| a |", []string{"a"}},
		{"空行", "", []string{""}},
		{"空单元格", "| a || b |", []string{"a", "", "b"}},
		{"全空格单元格", "| |", []string{""}},
		{"含中文与假名", "| 日本語 | かな |", []string{"日本語", "かな"}},
		{"多空格trim", "|  部屋  |  かぎ  |", []string{"部屋", "かぎ"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitCells(c.line)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("SplitCells(%q) = %#v, want %#v", c.line, got, c.want)
			}
		})
	}
}

// 实现不识别 markdown 转义竖线（\|），一律按普通分隔符拆开。如实断言这一行为。
func TestSplitCellsDoesNotHonorEscapedPipe(t *testing.T) {
	got := SplitCells(`| a \| b |`)
	if len(got) != 2 || got[0] != `a \` || got[1] != "b" {
		t.Errorf("转义竖线按实现会被拆开，实际 %#v", got)
	}
}

// ---------- 3) 私有辅助函数 ----------

func TestIsSeparatorRowVariants(t *testing.T) {
	cases := []struct {
		name  string
		cells []string
		want  bool
	}{
		{"标准分隔行", []string{"---", "---", "---"}, true},
		{"带冒号对齐", []string{":---", ":---:", "---:"}, true},
		{"混合空格冒号", []string{"- -", "-: ", " ---"}, true},
		{"空切片", nil, false},
		{"含文字", []string{"---", "日文"}, false},
		{"纯表头", []string{"日文", "中文"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSeparatorRow(c.cells); got != c.want {
				t.Errorf("isSeparatorRow(%#v) = %v, want %v", c.cells, got, c.want)
			}
		})
	}
}

// 已有 TestTableColumnsRejectsTwoColumnVocabTable 只断言 jp/cn 是否 >=0，
// 这里补精确列号：两列返回 -1，三列/四列返回日文、中文各自的下标。
func TestTableColumnsExactIndices(t *testing.T) {
	cases := []struct {
		name string
		line string
		jp   int
		cn   int
	}{
		{"两列生词表", "| 日文 | 中文 |", -1, -1},
		{"三列序号表", "| 序号 | 日文 | 中文 |", 1, 2},
		{"三列说话人表", "| 说话人 | 日文 | 中文 |", 1, 2},
		{"四列对话表", "| 对话 | 说话人 | 日文 | 中文 |", 2, 3},
		{"列顺序颠倒", "| 中文 | 日文 | 备注 |", 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jp, cn := TableColumns(SplitCells(c.line))
			if jp != c.jp || cn != c.cn {
				t.Errorf("TableColumns(%q) = (%d,%d), want (%d,%d)", c.line, jp, cn, c.jp, c.cn)
			}
		})
	}
}

func TestCleanImportedJPVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"斜杠后带空格", "明日は 残業しなくても/ いいですよ。", "明日は 残業しなくても いいですよ。"},
		{"斜杠前带空格", "a / b", "a b"},
		{"斜杠两侧多空格", "a  /  b", "a b"},
		{"斜杠紧贴", "a/b", "a b"},
		{"多个斜杠", "a / b / c", "a b c"},
		{"多余半角空格", "a    b", "a b"},
		{"多余全角空格", "a　　b", "a b"},
		{"首尾空格", "  部屋  ", "部屋"},
		{"占位符不在此清洗", "待补", "待补"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanImportedJP(c.in); got != c.want {
				t.Errorf("cleanImportedJP(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsVocabCellVariants(t *testing.T) {
	cases := []struct {
		name string
		jp   string
		want bool
	}{
		{"全角括号无句号", "初めて（第一次）", true},
		{"全角括号有句号", "初めて（第一次）。", false},
		{"纯汉字句子", "あの 人が 吉田課長ですよ。", false},
		{"括号待补", "（待补）", true},
		{"无括号短句", "心配しなくても いいですよ。", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isVocabCell(c.jp); got != c.want {
				t.Errorf("isVocabCell(%q) = %v, want %v", c.jp, got, c.want)
			}
		})
	}
}

// ---------- 4) LessonDocName / LessonLabelOf ----------

// 已有 TestLessonDocName 覆盖了单元 5 的两种文档类型。这里补越界/0/负数单元：
// 实现不做校验，直接按格式拼进文档名。
func TestLessonDocNameOutOfRangeUnits(t *testing.T) {
	cases := []struct {
		unit int
		kind LessonDocKind
		want string
	}{
		{0, LessonDocKinds[0], "课本原文汇编_第0单元（基本课文）.md"},
		{-1, LessonDocKinds[1], "课本原文汇编_第-1单元（应用课文）.md"},
		{13, LessonDocKinds[0], "课本原文汇编_第13单元（基本课文）.md"},
	}
	for _, c := range cases {
		if got := LessonDocName(c.unit, c.kind); got != c.want {
			t.Errorf("LessonDocName(%d, %q) = %q, want %q", c.unit, c.kind.Suffix, got, c.want)
		}
	}
}

// 已有测试未覆盖 LessonLabelOf。
func TestLessonLabelOfVariants(t *testing.T) {
	cases := map[int]string{1: "第1课", 19: "第19课", 100: "第100课", 0: "", -5: ""}
	for n, want := range cases {
		if got := LessonLabelOf(n); got != want {
			t.Errorf("LessonLabelOf(%d) = %q, want %q", n, got, want)
		}
	}
}

// ---------- 5) HasLessonSection / LessonSectionsIn ----------

// 已有 TestHasLessonSection 只测了第19课存在、第25课不存在。这里补：
// 课号写在正文（无 ## 前缀）不算、标题带额外文字算、"第190课"不匹配第19课。
func TestHasLessonSectionPrefixAndBody(t *testing.T) {
	doc := "# 标题\n\n正文里提到第19课但不是标题。\n\n## 第19课 基本课文\n\n内容\n\n## 第190课 应用课文\n"
	if !HasLessonSection(doc, 19) {
		t.Error("## 第19课 标题应被识别")
	}
	if !HasLessonSection(doc, 190) {
		t.Error("## 第190课 标题应被识别为第190课")
	}
	// 正文里的"第19课"不构成段落。
	bodyOnly := "# 标题\n\n正文里提到第19课但不是标题。\n"
	if HasLessonSection(bodyOnly, 19) {
		t.Error("正文里的课号不该被当成段落")
	}
	// "第190课" 标题不含子串 "第19课"，不该匹配第19课。
	if HasLessonSection("## 第190课\n", 19) {
		t.Error("第190课标题不含子串第19课，不该匹配第19课")
	}
	if HasLessonSection("", 19) {
		t.Error("空文档应返回 false")
	}
}

// 已有 TestLessonSectionsIn 覆盖了升序列表与无课号文档。这里补去重、乱序、正文课号不计。
func TestLessonSectionsInDedupAndSort(t *testing.T) {
	doc := "## 第20课\n\n正文第3课不算。\n\n## 第3课\n\n## 第20课 应用课文\n\n## 附录\n"
	got := LessonSectionsIn(doc)
	want := []int{3, 20}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LessonSectionsIn = %v, want %v（去重且升序，正文课号不计）", got, want)
	}
	if got := LessonSectionsIn(""); got != nil && len(got) != 0 {
		t.Errorf("空文档应返回空，实际 %v", got)
	}
	// "## 第0课" 也会被解析为课号 0（实现不过滤）。
	got0 := LessonSectionsIn("## 第0课\n")
	if !reflect.DeepEqual(got0, []int{0}) {
		t.Errorf("第0课按实现应返回 [0]，实际 %v", got0)
	}
}

// ---------- 6) MaxBankSeq ----------

func TestMaxBankSeqVariants(t *testing.T) {
	existing := []sentence.BankSentence{
		{ID: "L19B001"},
		{ID: "L19B009"},
		{ID: "L19A005"}, // 不同 tag，互不影响
		{ID: "L18B099"}, // 不同课，互不影响
		{ID: "L19Babc"}, // 序号解析失败，跳过
		{ID: "L19B"},    // 无序号，跳过
	}

	t.Run("同课同tag最大序号", func(t *testing.T) {
		if got := MaxBankSeq(existing, 19, "B"); got != 9 {
			t.Errorf("MaxBankSeq(19,B) = %d, want 9", got)
		}
	})
	t.Run("不同tag各自统计", func(t *testing.T) {
		if got := MaxBankSeq(existing, 19, "A"); got != 5 {
			t.Errorf("MaxBankSeq(19,A) = %d, want 5", got)
		}
	})
	t.Run("另一课", func(t *testing.T) {
		if got := MaxBankSeq(existing, 18, "B"); got != 99 {
			t.Errorf("MaxBankSeq(18,B) = %d, want 99", got)
		}
	})
	t.Run("空列表", func(t *testing.T) {
		if got := MaxBankSeq(nil, 19, "B"); got != 0 {
			t.Errorf("空列表应返回 0，实际 %d", got)
		}
	})
	t.Run("无匹配条目", func(t *testing.T) {
		if got := MaxBankSeq(existing, 99, "B"); got != 0 {
			t.Errorf("无匹配应返回 0，实际 %d", got)
		}
	})
	t.Run("序号解析失败计为0", func(t *testing.T) {
		bad := []sentence.BankSentence{{ID: "L19Babc"}, {ID: "L19B"}}
		if got := MaxBankSeq(bad, 19, "B"); got != 0 {
			t.Errorf("序号解析失败应返回 0，实际 %d", got)
		}
	})
}

// ---------- 7) NewBankSentence 全字段 ----------

// 期望值手写，不调用被测函数生成。注意：实现不设置 Prototype，故断言其为空串。
func TestNewBankSentenceFullFields(t *testing.T) {
	row := ImportedRow{JP: "あの 人が 吉田課長ですよ。", CN: "那个人是吉田科长。"}
	got := NewBankSentence(19, LessonDocKinds[0], "课本原文汇编_第5单元（基本课文）.md", 10, row)

	want := sentence.BankSentence{
		ID:        "L19B010",
		Lesson:    "第19课",
		Source:    "基本课文",
		SourceDoc: "课本原文汇编_第5单元（基本课文）.md",
		Type:      "original",
		Prototype: "", // 实现未赋值
		Answer:    "あの 人が 吉田課長ですよ。",
		Chinese:   "那个人是吉田科长。",
	}
	if got != want {
		t.Errorf("NewBankSentence = %+v, want %+v", got, want)
	}
}

func TestNewBankSentenceAppliedKindAndSeqFormat(t *testing.T) {
	cases := []struct {
		name   string
		lesson int
		kind   LessonDocKind
		seq    int
		wantID string
	}{
		{"应用课文个位序号", 7, LessonDocKinds[1], 3, "L7A003"},
		{"三位序号不补零溢出", 12, LessonDocKinds[0], 123, "L12B123"},
		{"序号1补足三位", 1, LessonDocKinds[0], 1, "L1B001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NewBankSentence(c.lesson, c.kind, "d.md", c.seq, ImportedRow{JP: "j", CN: "c"})
			if got.ID != c.wantID {
				t.Errorf("ID = %q, want %q", got.ID, c.wantID)
			}
			if got.Source != c.kind.Source {
				t.Errorf("Source = %q, want %q", got.Source, c.kind.Source)
			}
			if got.Lesson != LessonLabelOf(c.lesson) {
				t.Errorf("Lesson = %q, want %q", got.Lesson, LessonLabelOf(c.lesson))
			}
		})
	}
}
