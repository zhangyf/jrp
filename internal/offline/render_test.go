package offline

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"github.com/zhangyf/jrp/internal/grading"
	"github.com/zhangyf/jrp/internal/sentence"
)

// 本文件只补渲染侧（根包）当前零覆盖的缺口，不重复 excel_meta_test.go 的
// 往返夹具与断言。期望表格全部按 excel.go 的布局规则手工推导，不调用任何
// 被测函数生成期望值；所有 emoji 一律用 Go 转义，不新增字形字面量。

// 状态符号 / 造句标题符号的首 rune（Go 转义）。
const (
	xlSymSevere   = "\U0001F525" // U+1F525
	xlSymModerate = "\u26A0"     // U+26A0
	xlSymMild     = "\U0001F4A4" // U+1F4A4
	xlSymSentence = "\U0001F4DD" // U+1F4DD
)

var xlSentenceNumRe = regexp.MustCompile(`^S[0-9]+$`)

// xlOpen 打开生成的 workbook；t.TempDir() 之外的文件不写进仓库目录。
func xlOpen(t *testing.T, path string) *excelize.File {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("打开 %s 失败：%v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// xlFromWriter 把内存里的 excelWriter 写出后用 excelize 重新打开校验。
func xlFromWriter(t *testing.T, w *excelWriter) *excelize.File {
	t.Helper()
	defer w.f.Close()
	var buf bytes.Buffer
	if _, err := w.f.WriteTo(&buf); err != nil {
		t.Fatalf("内存写出失败：%v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("内存打开失败：%v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func xlCell(t *testing.T, f *excelize.File, sheet, ref string) string {
	t.Helper()
	v, err := f.GetCellValue(sheet, ref)
	if err != nil {
		t.Fatalf("读 %s!%s 失败：%v", sheet, ref, err)
	}
	return v
}

func xlAssertCells(t *testing.T, f *excelize.File, sheet string, want map[string]string) {
	t.Helper()
	for ref, w := range want {
		if got := xlCell(t, f, sheet, ref); got != w {
			t.Errorf("%s!%s = %q，want %q", sheet, ref, got, w)
		}
	}
}

func xlRows(t *testing.T, f *excelize.File, sheet string) [][]string {
	t.Helper()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("读 %s 行失败：%v", sheet, err)
	}
	return rows
}

// xlSheetText 拼出整张表的文本，用于断言「某字样没有出现」。
func xlSheetText(t *testing.T, f *excelize.File, sheet string) string {
	t.Helper()
	var b strings.Builder
	for _, r := range xlRows(t, f, sheet) {
		for _, c := range r {
			b.WriteString(c)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func xlIsNum(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil
}

// ---------------------------------------------------------------------------
// 1) 钉子户导出分支：GenerateHardExcel / GenerateHardExcelWithMeta
// ---------------------------------------------------------------------------

func TestHardExcelExport(t *testing.T) {
	// hard 的过滤规则（cmd_export_hard.go:24 + excel.go:414）：order 恒为
	// hardCategories 三个状态常量，groupPlanWordsByStatus 其余状态整块丢弃；
	// 且 GenerateHardExcelWithMeta 传死 nil sentences，plan 自带句子也被忽略。
	// ErrorCount / MinAccuracy / MinReviews 取特征值，用来断言它们不落表。
	words := []PlanWord{
		{Number: 1, Word: "word1", Definition: "释义1", Status: HardSevere, ErrorCount: 137},
		{Number: 2, Word: "word2", Definition: "释义2", Status: HardSevere, ErrorCount: 138},
		{Number: 3, Word: "word3", Definition: "释义3", Status: HardSevere, ErrorCount: 139},
		{Number: 4, Word: "word4", Definition: "释义4", Status: HardModerate, ErrorCount: 140},
		{Number: 5, Word: "word5", Definition: "释义5", Status: HardModerate, ErrorCount: 141},
		{Number: 6, Word: "word6", Definition: "释义6", Status: HardMild, ErrorCount: 142},
		{Number: 7, Word: "word7", Definition: "过滤掉", Status: reviewCategories[1]}, // 非钉子户状态
	}
	plan := &ReviewPlan{
		Date:        "2026-11-03",
		Language:    "ja",
		Kind:        "hard",
		MinAccuracy: 0.61,
		MinReviews:  17,
		Words:       words,
		Sentences: []sentence.PlanSentence{
			{Number: 1, Chinese: "句子会被丢弃", Answer: "答え"},
		},
	}

	// 手写期望：三个 section（3/2/1 词），half 分别为 2/1/1，
	// 每个 section 占「标题 + 表头 + 数据行 + 空行」，最后一个空行是尾部。
	wantTitleSevere := fmt.Sprintf("%s 3词", HardSevere)
	wantTitleModerate := fmt.Sprintf("%s 2词", HardModerate)
	wantTitleMild := fmt.Sprintf("%s 1词", HardMild)
	wantTitles := []struct {
		row   string
		sym   string
		title string
	}{
		{"A1", xlSymSevere, wantTitleSevere},
		{"A6", xlSymModerate, wantTitleModerate},
		{"A10", xlSymMild, wantTitleMild},
	}

	// 练习版：8 列，withAnswer=false → C/G 书写列留空；右栏 E/F/G。
	wantExer := map[string]string{
		"A1": wantTitleSevere,
		"A2": "序号", "B2": "中文", "C2": "日语", "E2": "序号", "F2": "中文", "G2": "日语",
		"A3": "1", "B3": "释义1", "C3": "", "E3": "3", "F3": "释义3", "G3": "",
		"A4": "2", "B4": "释义2", "C4": "", "E4": "", "F4": "", "G4": "",
		"A6": wantTitleModerate,
		"A7": "序号", "B7": "中文", "C7": "日语", "E7": "序号", "F7": "中文", "G7": "日语",
		"A8": "4", "B8": "释义4", "C8": "", "E8": "5", "F8": "释义5", "G8": "",
		"A10": wantTitleMild,
		"A11": "序号", "B11": "中文", "C11": "日语", "E11": "序号", "F11": "中文", "G11": "日语",
		"A12": "6", "B12": "释义6", "C12": "", "E12": "", "F12": "", "G12": "",
	}

	// 答案版：6 列，withAnswer=true → C/F 写日语；右栏 D/E/F。
	wantAns := map[string]string{
		"A1": wantTitleSevere,
		"A2": "序号", "B2": "中文", "C2": "日语", "D2": "序号", "E2": "中文", "F2": "日语",
		"A3": "1", "B3": "释义1", "C3": "word1", "D3": "3", "E3": "释义3", "F3": "word3",
		"A4": "2", "B4": "释义2", "C4": "word2", "D4": "", "E4": "", "F4": "",
		"A6": wantTitleModerate,
		"A7": "序号", "B7": "中文", "C7": "日语", "D7": "序号", "E7": "中文", "F7": "日语",
		"A8": "4", "B8": "释义4", "C8": "word4", "D8": "5", "E8": "释义5", "F8": "word5",
		"A10": wantTitleMild,
		"A11": "序号", "B11": "中文", "C11": "日语", "D11": "序号", "E11": "中文", "F11": "日语",
		"A12": "6", "B12": "释义6", "C12": "word6", "D12": "", "E12": "", "F12": "",
	}

	cases := []struct {
		name      string
		withMeta  bool
		wantSheet []string
	}{
		{
			name:      "without_meta",
			wantSheet: []string{ExerciseSheetName, AnswerSheetName},
		},
		{
			name:      "with_meta",
			withMeta:  true,
			wantSheet: []string{ExerciseSheetName, AnswerSheetName, MetaSheetName},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "hard.xlsx")
			var err error
			if tc.withMeta {
				err = GenerateHardExcelWithMeta(plan, path, &ExcelMeta{
					PlanDate: "2026-11-03", Language: "ja", Mode: "hard", Kind: "hard",
				})
			} else {
				err = GenerateHardExcel(plan, path)
			}
			if err != nil {
				t.Fatalf("生成失败：%v", err)
			}
			f := xlOpen(t, path)

			// sheet 名与数量
			if got := f.GetSheetList(); !reflect.DeepEqual(got, tc.wantSheet) {
				t.Errorf("sheet 列表 = %v，want %v", got, tc.wantSheet)
			}

			// 行数与列内容
			for _, sh := range []string{ExerciseSheetName, AnswerSheetName} {
				if got := len(xlRows(t, f, sh)); got != 12 {
					t.Errorf("%s 行数 = %d，want 12", sh, got)
				}
			}
			xlAssertCells(t, f, ExerciseSheetName, wantExer)
			xlAssertCells(t, f, AnswerSheetName, wantAns)

			// 状态符号写入（两个 sheet 的三个标题行都必须带符号）
			for _, sh := range []string{ExerciseSheetName, AnswerSheetName} {
				for _, wt := range wantTitles {
					got := xlCell(t, f, sh, wt.row)
					if got != wt.title {
						t.Errorf("%s!%s = %q，want %q", sh, wt.row, got, wt.title)
					}
					if !strings.HasPrefix(got, wt.sym) {
						t.Errorf("%s!%s 未写入状态符号 %q：%q", sh, wt.row, wt.sym, got)
					}
				}
			}

			// 只导出钉子户类别：非钉子户状态词、句子、造句区块都不出现
			for _, sh := range []string{ExerciseSheetName, AnswerSheetName} {
				txt := xlSheetText(t, f, sh)
				for _, forbidden := range []string{
					reviewCategories[1], "过滤掉", "句子会被丢弃", "答え", "造句", "S1",
				} {
					if strings.Contains(txt, forbidden) {
						t.Errorf("%s 不该含 %q", sh, forbidden)
					}
				}
				// Grademode / 档位字段（若有）：当前实现不落表，按实现如实断言不存在
				for _, absent := range []string{"grade_mode", "档位", "137", "0.61", "17"} {
					if strings.Contains(txt, absent) {
						t.Errorf("%s 不该含 %q（Grademode/档位字段未写入）", sh, absent)
					}
				}
			}

			if tc.withMeta {
				// _meta 的键集合恰好是四个固定键，没有 grade_mode / 档位键。
				xlAssertCells(t, f, MetaSheetName, map[string]string{
					"A1": "plan_date", "A2": "language", "A3": "mode", "A4": "kind",
				})
				if got := len(xlRows(t, f, MetaSheetName)); got != 4 {
					t.Errorf("_meta 行数 = %d，want 4", got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2) nil / 空 sentences
// ---------------------------------------------------------------------------

func TestExcelNilEmptySentences(t *testing.T) {
	cases := []struct {
		name      string
		sentences []sentence.PlanSentence
	}{
		{"nil_sentences", nil},
		{"empty_sentences", []sentence.PlanSentence{}},
	}
	signatures := map[string]string{}
	wantSheets := []string{ExerciseSheetName, AnswerSheetName}
	wantTitle := fmt.Sprintf("%s 1词", reviewCategories[0])

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := &ReviewPlan{
				Date:     "2026-11-03",
				Language: "ja",
				Words: []PlanWord{
					{Number: 1, Word: "word1", Definition: "释义1", Status: reviewCategories[0]},
				},
				Sentences: tc.sentences,
			}
			path := filepath.Join(t.TempDir(), "out.xlsx")
			if err := GenerateExcelWithMeta(plan, path, nil); err != nil {
				t.Fatalf("nil/空 sentences 不该报错：%v", err)
			}
			f := xlOpen(t, path)

			// 按实现：没有句子也照样生成两个可见 sheet，且不带 _meta。
			if got := f.GetSheetList(); !reflect.DeepEqual(got, wantSheets) {
				t.Errorf("sheet 列表 = %v，want %v", got, wantSheets)
			}

			// 单词区块照常写出：标题 + 表头 + 1 个数据行；尾部空行被裁掉 → 3 行。
			xlAssertCells(t, f, ExerciseSheetName, map[string]string{
				"A1": wantTitle,
				"A2": "序号", "B2": "中文", "C2": "日语", "E2": "序号", "F2": "中文", "G2": "日语",
				"A3": "1", "B3": "释义1", "C3": "", "E3": "", "F3": "", "G3": "",
			})
			xlAssertCells(t, f, AnswerSheetName, map[string]string{
				"A1": wantTitle, "A3": "1", "B3": "释义1", "C3": "word1",
			})

			var sig strings.Builder
			for _, sh := range wantSheets {
				if got := len(xlRows(t, f, sh)); got != 3 {
					t.Errorf("%s 行数 = %d，want 3", sh, got)
				}
				txt := xlSheetText(t, f, sh)
				// renderSentenceSection 早退：造句标题与 S1 都不写
				if strings.Contains(txt, "造句") || strings.Contains(txt, "S1") {
					t.Errorf("%s 不该有造句区块", sh)
				}
				sig.WriteString(sh)
				sig.WriteByte(':')
				sig.WriteString(txt)
			}
			signatures[tc.name] = sig.String()
		})
	}

	// nil 与空 slice 输出完全一致
	if signatures["nil_sentences"] != signatures["empty_sentences"] {
		t.Errorf("nil 与空 sentences 生成结果不一致")
	}
}

// ---------------------------------------------------------------------------
// 3) 左右分栏（excel.go:196 half = (len(words)+1)/2）
// ---------------------------------------------------------------------------

func xlCheckSplit(t *testing.T, f *excelize.File, sheet string,
	lSeq, lDef, lAns, rSeq, rDef, rAns string, n, wantHalf int, withAnswer bool) {

	t.Helper()
	type cellEntry struct{ def, ans string }
	left := map[int]cellEntry{}
	right := map[int]cellEntry{}
	dups := []string{}

	// 用坐标扫描，避开 GetRows 对中间空列的压缩。
	for row := 1; row <= 20; row++ {
		if s := xlCell(t, f, sheet, fmt.Sprintf("%s%d", lSeq, row)); xlIsNum(s) {
			num, _ := strconv.Atoi(s)
			if _, ok := left[num]; ok {
				dups = append(dups, fmt.Sprintf("左栏序号 %d 重复", num))
			}
			left[num] = cellEntry{
				def: xlCell(t, f, sheet, fmt.Sprintf("%s%d", lDef, row)),
				ans: xlCell(t, f, sheet, fmt.Sprintf("%s%d", lAns, row)),
			}
		}
		if s := xlCell(t, f, sheet, fmt.Sprintf("%s%d", rSeq, row)); xlIsNum(s) {
			num, _ := strconv.Atoi(s)
			if _, ok := right[num]; ok {
				dups = append(dups, fmt.Sprintf("右栏序号 %d 重复", num))
			}
			right[num] = cellEntry{
				def: xlCell(t, f, sheet, fmt.Sprintf("%s%d", rDef, row)),
				ans: xlCell(t, f, sheet, fmt.Sprintf("%s%d", rAns, row)),
			}
		}
	}

	if len(dups) != 0 {
		t.Errorf("%s 出现重复：%v", sheet, dups)
	}
	if len(left) != wantHalf || len(right) != n-wantHalf {
		t.Errorf("%s 左右栏词数 = %d/%d，want %d/%d",
			sheet, len(left), len(right), wantHalf, n-wantHalf)
	}
	// 每个词都出现且不重复：编号并集恰为 1..n
	for i := 1; i <= n; i++ {
		_, inLeft := left[i]
		_, inRight := right[i]
		if inLeft == inRight {
			t.Errorf("%s 序号 %d 出现次数异常（左=%v 右=%v）", sheet, i, inLeft, inRight)
		}
		// 按实现：前 half 个全在左栏，其余全在右栏，不是左右交替。
		if i <= wantHalf && !inLeft {
			t.Errorf("%s 序号 %d 应在左栏", sheet, i)
		}
		if i > wantHalf && !inRight {
			t.Errorf("%s 序号 %d 应在右栏", sheet, i)
		}
	}
	checkEntry := func(side string, num int, e cellEntry) {
		if want := fmt.Sprintf("释义%d", num); e.def != want {
			t.Errorf("%s %s 序号 %d 中文 = %q，want %q", sheet, side, num, e.def, want)
		}
		if withAnswer {
			if want := fmt.Sprintf("word%d", num); e.ans != want {
				t.Errorf("%s %s 序号 %d 日语 = %q，want %q", sheet, side, num, e.ans, want)
			}
		} else if e.ans != "" {
			// 练习版 C/G 是手写区，必须留空。
			t.Errorf("%s %s 序号 %d 手写列应留空，实为 %q", sheet, side, num, e.ans)
		}
	}
	for num, e := range left {
		checkEntry("左栏", num, e)
	}
	for num, e := range right {
		checkEntry("右栏", num, e)
	}
}

func TestExcelTwoColumnSplit(t *testing.T) {
	cases := []struct {
		name     string
		n        int
		wantHalf int
	}{
		{"odd_seven", 7, 4}, // (7+1)/2 = 4，左栏比右栏多一个
		{"even_six", 6, 3},  // (6+1)/2 = 3
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			words := make([]PlanWord, 0, tc.n)
			for i := 1; i <= tc.n; i++ {
				words = append(words, PlanWord{
					Number:     i,
					Word:       fmt.Sprintf("word%d", i),
					Definition: fmt.Sprintf("释义%d", i),
					Status:     reviewCategories[0],
				})
			}
			plan := &ReviewPlan{Date: "2026-11-03", Language: "ja", Words: words}
			path := filepath.Join(t.TempDir(), "split.xlsx")
			if err := GenerateExcel(plan, path); err != nil {
				t.Fatalf("生成失败：%v", err)
			}
			f := xlOpen(t, path)

			// 练习版：左栏 A/B/C、右栏 E/F/G（C/G 手写列留空）。
			xlCheckSplit(t, f, ExerciseSheetName,
				"A", "B", "C", "E", "F", "G", tc.n, tc.wantHalf, false)
			// 答案版：左栏 A/B/C、右栏 D/E/F（C/F 写答案）。
			xlCheckSplit(t, f, AnswerSheetName,
				"A", "B", "C", "D", "E", "F", tc.n, tc.wantHalf, true)
		})
	}
}

// ---------------------------------------------------------------------------
// 4) renderSentenceSection
// ---------------------------------------------------------------------------

func TestExcelSentenceSection(t *testing.T) {
	sentences := []sentence.PlanSentence{
		{Number: 1, Chinese: "中文提示1", Answer: "日语答案1"},
		{Number: 2, Chinese: "中文提示2", Answer: "日语答案2"},
	}
	wantTitle := xlSymSentence + " 造句 共2句"

	t.Run("exercise", func(t *testing.T) {
		w := newExcelWriter()
		w.renderSentenceSection("Sheet1", true, false, sentences, 1)
		f := xlFromWriter(t, w)

		// 练习版：A=S{n}，B=中文，C:F 合并成书写空白。
		xlAssertCells(t, f, "Sheet1", map[string]string{
			"A1": wantTitle,
			"A2": "序号", "B2": "中文提示", "C2": "日语",
			"A3": "S1", "B3": "中文提示1", "C3": "", "D3": "", "E3": "", "F3": "",
			"A4": "S2", "B4": "中文提示2", "C4": "", "D4": "", "E4": "", "F4": "",
		})
		for _, ref := range []string{"A3", "A4"} {
			if got := xlCell(t, f, "Sheet1", ref); !xlSentenceNumRe.MatchString(got) {
				t.Errorf("Sheet1!%s = %q，编号应为 S{n} 形态", ref, got)
			}
		}
		if got := len(xlRows(t, f, "Sheet1")); got != 4 {
			t.Errorf("练习版句子区块行数 = %d，want 4", got)
		}
	})

	t.Run("answer", func(t *testing.T) {
		w := newExcelWriter()
		w.renderSentenceSection("Sheet1", false, true, sentences, 1)
		f := xlFromWriter(t, w)

		// 答案版：A=S{n}，B:C 合并中文，D:F 合并日语答案。
		xlAssertCells(t, f, "Sheet1", map[string]string{
			"A1": wantTitle,
			"A2": "序号", "B2": "中文提示", "D2": "日语",
			"A3": "S1", "B3": "中文提示1", "D3": "日语答案1",
			"A4": "S2", "B4": "中文提示2", "D4": "日语答案2",
		})
		for _, ref := range []string{"A3", "A4"} {
			if got := xlCell(t, f, "Sheet1", ref); !xlSentenceNumRe.MatchString(got) {
				t.Errorf("Sheet1!%s = %q，编号应为 S{n} 形态", ref, got)
			}
		}
		if got := len(xlRows(t, f, "Sheet1")); got != 4 {
			t.Errorf("答案版句子区块行数 = %d，want 4", got)
		}
	})

	t.Run("no_sentences", func(t *testing.T) {
		w := newExcelWriter()
		w.renderSentenceSection("Sheet1", true, false, nil, 1)
		f := xlFromWriter(t, w)
		if got := xlSheetText(t, f, "Sheet1"); got != "" {
			t.Errorf("nil sentences 应完全不写单元格，实为 %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// 5) _meta sheet：隐藏、活动表复位、Kind 写而未读回
// ---------------------------------------------------------------------------

func TestExcelMetaSheetVisibility(t *testing.T) {
	plan := &ReviewPlan{
		Date:     "2026-11-03",
		Language: "ja",
		Words: []PlanWord{
			{Number: 1, Word: "word1", Definition: "释义1", Status: reviewCategories[0]},
		},
	}
	meta := &ExcelMeta{
		PlanDate: "2026-11-03", Language: "ja", Mode: "daily", Kind: "hard",
	}

	t.Run("hidden_and_active_reset", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "meta.xlsx")
		if err := GenerateExcelWithMeta(plan, path, meta); err != nil {
			t.Fatalf("生成失败：%v", err)
		}
		f := xlOpen(t, path)

		wantSheets := []string{
			ExerciseSheetName, AnswerSheetName, MetaSheetName,
		}
		if got := f.GetSheetList(); !reflect.DeepEqual(got, wantSheets) {
			t.Errorf("sheet 列表 = %v，want %v", got, wantSheets)
		}

		// writeMetaSheet 用 SetSheetVisible(false) 隐藏 _meta。
		vis, err := f.GetSheetVisible(MetaSheetName)
		if err != nil || vis {
			t.Errorf("_meta 应隐藏：visible=%v err=%v", vis, err)
		}
		for _, sh := range wantSheets[:2] {
			vis, err := f.GetSheetVisible(sh)
			if err != nil || !vis {
				t.Errorf("%s 应可见：visible=%v err=%v", sh, vis, err)
			}
		}

		// excel.go:381-384 在 _meta 建完后无条件 SetActiveSheet(练习版索引)。
		active := f.GetActiveSheetIndex()
		if active != 0 {
			t.Errorf("活动表索引 = %d，want 0", active)
		}
		if got := f.GetSheetName(active); got != ExerciseSheetName {
			t.Errorf("活动表 = %q，want %q", got, ExerciseSheetName)
		}

		// 写入侧：四行固定键，kind 也在其中。
		xlAssertCells(t, f, MetaSheetName, map[string]string{
			"A1": "plan_date", "B1": "2026-11-03",
			"A2": "language", "B2": "ja",
			"A3": "mode", "B3": "daily",
			"A4": "kind", "B4": "hard",
		})
		if got := len(xlRows(t, f, MetaSheetName)); got != 4 {
			t.Errorf("_meta 行数 = %d，want 4", got)
		}
	})

	t.Run("kind_written_but_not_read_back", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "meta.xlsx")
		if err := GenerateExcelWithMeta(plan, path, meta); err != nil {
			t.Fatalf("生成失败：%v", err)
		}
		f := xlOpen(t, path)
		if got := xlCell(t, f, MetaSheetName, "B4"); got != "hard" {
			t.Errorf("_meta!B4 = %q，kind 应写入 hard", got)
		}

		// 如实断言当前行为：ExcelMeta 有 Kind 字段（写入侧），但 ImportPreview
		// 没有 Kind 字段，ParseExerciseFile 的 switch 也没有 kind 分支，
		// 未知键被忽略 —— kind 写进去了却读不回来。这不是 bug，不当成 bug 改。
		if _, ok := reflect.TypeOf(ExcelMeta{}).FieldByName("Kind"); !ok {
			t.Errorf("ExcelMeta 应有 Kind 字段")
		}
		if _, ok := reflect.TypeOf(ImportPreview{}).FieldByName("Kind"); ok {
			t.Errorf("ImportPreview 不应有 Kind 字段（当前实现）")
		}

		prev, err := ParseExerciseFile(path, grading.GradeKana)
		if err != nil {
			t.Fatalf("带 kind 的 _meta 不该导致解析失败：%v", err)
		}
		if prev.PlanDate != "2026-11-03" || prev.Language != "ja" || prev.Mode != "daily" {
			t.Errorf("已知键解析不对：%+v", prev)
		}
		v := reflect.ValueOf(*prev)
		for i := 0; i < v.NumField(); i++ {
			if s, ok := v.Field(i).Interface().(string); ok && s == "hard" {
				t.Errorf("kind 被读回进了字段 %s（当前实现不该发生）", v.Type().Field(i).Name)
			}
		}
	})

	t.Run("no_meta_still_resets_active", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "plain.xlsx")
		if err := GenerateExcel(plan, path); err != nil {
			t.Fatalf("生成失败：%v", err)
		}
		f := xlOpen(t, path)
		// 复位代码在 if meta != nil 之外，无 _meta 时也会执行。
		wantSheets := []string{ExerciseSheetName, AnswerSheetName}
		if got := f.GetSheetList(); !reflect.DeepEqual(got, wantSheets) {
			t.Errorf("sheet 列表 = %v，want %v", got, wantSheets)
		}
		if got := f.GetActiveSheetIndex(); got != 0 {
			t.Errorf("活动表索引 = %d，want 0", got)
		}
		if got := f.GetSheetName(0); got != ExerciseSheetName {
			t.Errorf("活动表 = %q，want %q", got, ExerciseSheetName)
		}
	})
}
