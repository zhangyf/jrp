package offline

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// 单栏手写小表（--compact）。
//
// 这是与 GenerateExcel（8 列两栏 + 比对公式）完全独立的一条渲染路径：
// 原函数、excelWriter、样式及 server 的 /api/plan 调用链一行都不碰。
// 只由 CLI 的 gen-plan --compact 使用，日语默认流程保持原样。
//
// 版式约定（对齐 2026-11-19 老师确认的英语小表）：
//   - 两个 sheet：✏️练习版（英文列留空）/ ✅答案版（填英文答案）
//   - 三列单栏：序号 | 中文 | 语言（默写/答案），A4 纵向一页，留白手写
//   - 三块分区：一、单词 / 二、词组 / 三、句子
//   - 不印姓名、得分、家长签字、日期栏（老师明确不要）
//   - 单词/词组序号沿用 plan JSON 的 number，保证 record 按序号回填不错位

// compactLangName 按 ReviewPlan.Language 给出语言短名。
// 映射与根包 LangConfigs 的 Name 一致，offline 包不反向依赖根包。
func compactLangName(lang string) string {
	switch lang {
	case "en":
		return "英语"
	case "fr":
		return "法语"
	default:
		return "日语"
	}
}

var (
	compactParenRE = regexp.MustCompile(`[（(]([^）)]*)[）)]`)
	compactASCIIRE = regexp.MustCompile(`[A-Za-z]`)
	compactSemiRE  = regexp.MustCompile(`；{2,}`)
)

// cleanCompactPrompt 练习版中文提示：只删「含英文的括注」（会泄题），
// 保留纯中文括注（保语义，例如「花（时间、钱等）」不能清成「花」）。
// 答案版用原始 Definition，保留完整搭配供讲解。
func cleanCompactPrompt(s string) string {
	s = compactParenRE.ReplaceAllStringFunc(s, func(m string) string {
		parts := compactParenRE.FindStringSubmatch(m)
		if len(parts) > 1 && compactASCIIRE.MatchString(parts[1]) {
			return ""
		}
		return m
	})
	s = compactSemiRE.ReplaceAllString(s, "；")
	return strings.Trim(strings.TrimSpace(s), "； ")
}

const (
	compactWidthA       = 8.0
	compactWidthB       = 34.0
	compactWidthC       = 46.0
	compactTitleRowH    = 30.0
	compactSectionRowH  = 22.0
	compactHeaderRowH   = 20.0
	compactWordRowH     = 30.0
	compactSentenceRowH = 42.0
	compactSpacerRowH   = 8.0
)

type compactStyles struct {
	title, section, header, num, data, write int
}

func newCompactStyles(f *excelize.File) compactStyles {
	mk := func(st *excelize.Style) int {
		id, err := f.NewStyle(st)
		if err != nil {
			return 0
		}
		return id
	}
	gray := excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}}
	write := excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFFDF3"}}
	border := []excelize.Border{
		{Type: "left", Color: "BFBFBF", Style: 1},
		{Type: "right", Color: "BFBFBF", Style: 1},
		{Type: "top", Color: "BFBFBF", Style: 1},
		{Type: "bottom", Color: "BFBFBF", Style: 1},
	}
	return compactStyles{
		title: mk(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Size: 14},
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		}),
		section: mk(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Size: 11},
			Fill:      gray,
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		}),
		header: mk(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Size: 10},
			Fill:      gray,
			Border:    border,
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		}),
		num: mk(&excelize.Style{
			Border:    border,
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		}),
		data: mk(&excelize.Style{
			Border:    border,
			Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center", WrapText: true},
		}),
		write: mk(&excelize.Style{
			Border:    border,
			Fill:      write,
			Alignment: &excelize.Alignment{Horizontal: "left", Vertical: "center", WrapText: true},
		}),
	}
}

type compactRow struct {
	num string // 单词/词组用 plan JSON 的 number；句子用 S1..Sn
	cn  string
	en  string
}

// writeCompactBlock 写一个分区，返回下一个可用行号。空分区直接跳过。
func writeCompactBlock(f *excelize.File, sheet string, row int, title string,
	rows []compactRow, thirdHeader string, withAnswer bool, st compactStyles,
	rowH float64) int {

	if len(rows) == 0 {
		return row
	}

	// 分区标题（合并三列）
	a := fmt.Sprintf("A%d", row)
	c := fmt.Sprintf("C%d", row)
	_ = f.MergeCell(sheet, a, c)
	_ = f.SetCellValue(sheet, a, fmt.Sprintf("%s（%d）", title, len(rows)))
	_ = f.SetCellStyle(sheet, a, c, st.section)
	_ = f.SetRowHeight(sheet, row, compactSectionRowH)
	row++

	// 表头
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "序号")
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "中文")
	_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", row), thirdHeader)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("C%d", row), st.header)
	_ = f.SetRowHeight(sheet, row, compactHeaderRowH)
	row++

	// 题目行
	for _, r := range rows {
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), r.num)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), r.cn)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), st.num)
		_ = f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), st.data)
		cellC := fmt.Sprintf("C%d", row)
		if withAnswer {
			_ = f.SetCellValue(sheet, cellC, r.en)
			_ = f.SetCellStyle(sheet, cellC, cellC, st.data)
		} else {
			_ = f.SetCellStyle(sheet, cellC, cellC, st.write)
		}
		_ = f.SetRowHeight(sheet, row, rowH)
		row++
	}

	_ = f.SetRowHeight(sheet, row, compactSpacerRowH)
	return row + 1
}

// writeCompactSheet 写单个 sheet，返回打印区域的最后一行。
func writeCompactSheet(f *excelize.File, sheet string, plan *ReviewPlan,
	withAnswer bool, st compactStyles, lang string) int {

	row := 1
	// 标题（不带姓名/得分栏）
	a := fmt.Sprintf("A%d", row)
	c := fmt.Sprintf("C%d", row)
	_ = f.MergeCell(sheet, a, c)
	_ = f.SetCellValue(sheet, a, fmt.Sprintf("%s每日复习 · %s", lang, plan.Date))
	_ = f.SetCellStyle(sheet, a, c, st.title)
	_ = f.SetRowHeight(sheet, row, compactTitleRowH)
	row++

	_ = f.SetRowHeight(sheet, row, compactSpacerRowH)
	row++

	// 按 group 名把「单词」与「词组/短语」分成两块；序号仍用 plan JSON 的 number。
	var words, phrases []compactRow
	for _, w := range plan.Words {
		cn := w.Definition
		if !withAnswer {
			cn = cleanCompactPrompt(cn)
		}
		r := compactRow{num: strconv.Itoa(w.Number), cn: cn, en: w.Word}
		if strings.Contains(w.Group, "词组") || strings.Contains(w.Group, "短语") {
			phrases = append(phrases, r)
		} else {
			words = append(words, r)
		}
	}

	var sentences []compactRow
	for _, s := range plan.Sentences {
		cn := s.Chinese
		if !withAnswer {
			cn = cleanCompactPrompt(cn)
		}
		sentences = append(sentences, compactRow{
			num: fmt.Sprintf("S%d", s.Number), cn: cn, en: s.Answer,
		})
	}

	third := fmt.Sprintf("%s（默写）", lang)
	if withAnswer {
		third = fmt.Sprintf("%s（答案）", lang)
	}
	row = writeCompactBlock(f, sheet, row, "一、单词", words, third, withAnswer, st, compactWordRowH)
	row = writeCompactBlock(f, sheet, row, "二、词组", phrases, third, withAnswer, st, compactWordRowH)
	row = writeCompactBlock(f, sheet, row, "三、句子", sentences, third, withAnswer, st, compactSentenceRowH)
	return row - 1
}

// GenerateCompactExcel 生成单栏手写小表（gen-plan --compact）。
// 与 GenerateExcel 完全独立，不影响 server / 日语默认版式。
func GenerateCompactExcel(plan *ReviewPlan, outputPath string) error {
	f := excelize.NewFile()
	defer f.Close()
	st := newCompactStyles(f)
	lang := compactLangName(plan.Language)

	sheetNames := []string{"✏️练习版", "✅答案版"}
	for i, name := range sheetNames {
		withAnswer := i == 1
		if i == 0 {
			_ = f.SetSheetName(f.GetSheetName(0), name)
		} else {
			_, _ = f.NewSheet(name)
		}
		_ = f.SetColWidth(name, "A", "A", compactWidthA)
		_ = f.SetColWidth(name, "B", "B", compactWidthB)
		_ = f.SetColWidth(name, "C", "C", compactWidthC)

		last := writeCompactSheet(f, name, plan, withAnswer, st, lang)

		// A4 纵向、一页打印
		size, orient := 9, "portrait"
		fw, fh := 1, 1
		_ = f.SetPageLayout(name, &excelize.PageLayoutOptions{
			Size: &size, Orientation: &orient, FitToWidth: &fw, FitToHeight: &fh,
		})
		left, right, top, bottom, hdr, ftr := 0.4, 0.4, 0.5, 0.5, 0.2, 0.2
		_ = f.SetPageMargins(name, &excelize.PageLayoutMarginsOptions{
			Left: &left, Right: &right, Top: &top, Bottom: &bottom,
			Header: &hdr, Footer: &ftr,
		})
		_ = f.SetDefinedName(&excelize.DefinedName{
			Name:     "_xlnm.Print_Area",
			RefersTo: fmt.Sprintf("'%s'!$A$1:$C$%d", name, last),
			Scope:    name,
		})
	}

	if idx, err := f.GetSheetIndex(sheetNames[0]); err == nil {
		f.SetActiveSheet(idx)
	}
	return f.SaveAs(outputPath)
}
