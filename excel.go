package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// reviewCategories is the section order for the daily review Excel.
//
// NOTE: keep this order as-is. SKILL.md documents a slightly different order in
// one place; the code is authoritative.
var reviewCategories = []string{
	"☠️钉子户", "🔴待巩固", "🔄待测试", "🟡基本掌握", "🟢抽查",
}

// excelStyles holds the style IDs used across both sheets.
//
// sentenceHdr is defined identically to header on purpose but MUST remain a
// separate style ID: excelize appends one cellXfs entry per NewStyle call, so
// merging them would shift the internal style indices of generated workbooks.
type excelStyles struct {
	sectionHeader int
	header        int
	numCell       int
	data          int
	sentenceHdr   int
}

// excelLayout holds column widths and row heights.
type excelLayout struct {
	exerRowHt  float64 // exercise sheet row height (0 = default)
	exerBWidth float64 // exercise B col width
	exerCWidth float64
	ansBWidth  float64
	ansCWidth  float64
}

var defaultExcelLayout = excelLayout{
	exerRowHt:  25.05,
	exerBWidth: 19.33,
	exerCWidth: 18.66,
	ansBWidth:  17,
	ansCWidth:  20.5,
}

// excelWriter wraps an excelize file with pre-registered styles and layout.
type excelWriter struct {
	f      *excelize.File
	st     excelStyles
	layout excelLayout
}

func newExcelWriter() *excelWriter {
	f := excelize.NewFile()

	sectionHeader, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14},
	})

	header, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	numCell, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	data, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	sentenceHdr, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	return &excelWriter{
		f: f,
		st: excelStyles{
			sectionHeader: sectionHeader,
			header:        header,
			numCell:       numCell,
			data:          data,
			sentenceHdr:   sentenceHdr,
		},
		layout: defaultExcelLayout,
	}
}

// setColWidths applies column widths. Exercise sheets use 8 columns (with
// auto-check formula columns D + H); answer sheets use 6 columns.
func (w *excelWriter) setColWidths(sheet string, isExer bool) {
	bw, cw := w.layout.ansBWidth, w.layout.ansCWidth
	if isExer {
		bw, cw = w.layout.exerBWidth, w.layout.exerCWidth
	}
	w.f.SetColWidth(sheet, "A", "A", 5)
	w.f.SetColWidth(sheet, "B", "B", bw)
	w.f.SetColWidth(sheet, "C", "C", cw)
	if isExer {
		w.f.SetColWidth(sheet, "D", "D", 6)
		w.f.SetColWidth(sheet, "E", "E", 5)
		w.f.SetColWidth(sheet, "F", "F", 17)
		w.f.SetColWidth(sheet, "G", "G", cw)
		w.f.SetColWidth(sheet, "H", "H", 6)
	} else {
		w.f.SetColWidth(sheet, "D", "D", 5)
		w.f.SetColWidth(sheet, "E", "E", 17)
		w.f.SetColWidth(sheet, "F", "F", 22.5)
	}
}

// rowHeight returns the row height to apply (0 = leave default).
// Only the exercise sheet gets an explicit row height.
func (w *excelWriter) rowHeight(isExer bool) float64 {
	if isExer {
		return w.layout.exerRowHt
	}
	return 0
}

// renderWordSections writes one section per non-empty category, in the given
// order, using a two-column layout. Returns the next free row.
//
// Exercise sheet (8 columns):
//
//	| A序号 | B中文 | C日语 | D比对 | E序号 | F中文 | G日语 | H比对 |
//
// Answer sheet (6 columns):
//
//	| A序号 | B中文 | C日语 | D序号 | E中文 | F日语 |
//
// When isExer=true, columns D and H contain auto-check formulas that compare
// the user's handwritten answer (C/G) with the answer sheet after stripping
// parenthetical kanji (e.g. "ちがいます(違います)" → "ちがいます").
func (w *excelWriter) renderWordSections(sheet string, isExer, withAnswer bool,
	order []string, byCat map[string][]PlanWord, startRow int) int {

	currentRow := startRow
	rowHt := w.rowHeight(isExer)

	for _, cat := range order {
		words := byCat[cat]
		if len(words) == 0 {
			continue
		}

		// Section title row (merged A:H for exercise, A:F for answer)
		titleCell := fmt.Sprintf("A%d", currentRow)
		w.f.SetCellValue(sheet, titleCell, fmt.Sprintf("%s %d词", cat, len(words)))
		w.f.SetCellStyle(sheet, titleCell, titleCell, w.st.sectionHeader)
		titleEndCol := "F"
		if isExer {
			titleEndCol = "H"
		}
		w.f.MergeCell(sheet, titleCell, fmt.Sprintf("%s%d", titleEndCol, currentRow))
		if rowHt > 0 {
			w.f.SetRowHeight(sheet, currentRow, rowHt)
		}
		currentRow++

		// Column header row
		hdr := currentRow
		w.f.SetCellValue(sheet, fmt.Sprintf("A%d", hdr), "序号")
		w.f.SetCellValue(sheet, fmt.Sprintf("B%d", hdr), "中文")
		w.f.SetCellValue(sheet, fmt.Sprintf("C%d", hdr), "日语")
		var hdrCols []string
		if isExer {
			// Exercise: right block shifts D→E, E→F, F→G; D+H are formula columns (no header)
			w.f.SetCellValue(sheet, fmt.Sprintf("E%d", hdr), "序号")
			w.f.SetCellValue(sheet, fmt.Sprintf("F%d", hdr), "中文")
			w.f.SetCellValue(sheet, fmt.Sprintf("G%d", hdr), "日语")
			hdrCols = []string{"A", "B", "C", "E", "F", "G"}
		} else {
			w.f.SetCellValue(sheet, fmt.Sprintf("D%d", hdr), "序号")
			w.f.SetCellValue(sheet, fmt.Sprintf("E%d", hdr), "中文")
			w.f.SetCellValue(sheet, fmt.Sprintf("F%d", hdr), "日语")
			hdrCols = []string{"A", "B", "C", "D", "E", "F"}
		}
		for _, col := range hdrCols {
			cellRef := fmt.Sprintf("%s%d", col, hdr)
			w.f.SetCellStyle(sheet, cellRef, cellRef, w.st.header)
		}
		if rowHt > 0 {
			w.f.SetRowHeight(sheet, hdr, rowHt)
		}
		currentRow++

		// Word data rows: two-column layout
		half := (len(words) + 1) / 2
		leftWords := words[:half]
		rightWords := words[half:]
		dataStart := currentRow
		maxRows := max(len(leftWords), len(rightWords))
		for i := 0; i < maxRows; i++ {
			row := dataStart + i
			if rowHt > 0 {
				w.f.SetRowHeight(sheet, row, rowHt)
			}
			// Left block
			if i < len(leftWords) {
				pw := leftWords[i]
				w.f.SetCellValue(sheet, fmt.Sprintf("A%d", row), pw.Number)
				w.f.SetCellValue(sheet, fmt.Sprintf("B%d", row), pw.Definition)
				w.f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), w.st.numCell)
				w.f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), w.st.data)
				if withAnswer {
					w.f.SetCellValue(sheet, fmt.Sprintf("C%d", row), pw.Word)
					w.f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), w.st.data)
				}
			}
			// Right block — exercise sheet uses E/F/G; answer sheet uses D/E/F
			rSeqCol, rDefCol, rWordCol := "D", "E", "F"
			if isExer {
				rSeqCol, rDefCol, rWordCol = "E", "F", "G"
			}
			if i < len(rightWords) {
				pw := rightWords[i]
				w.f.SetCellValue(sheet, fmt.Sprintf("%s%d", rSeqCol, row), pw.Number)
				w.f.SetCellValue(sheet, fmt.Sprintf("%s%d", rDefCol, row), pw.Definition)
				w.f.SetCellStyle(sheet, fmt.Sprintf("%s%d", rSeqCol, row), fmt.Sprintf("%s%d", rSeqCol, row), w.st.numCell)
				w.f.SetCellStyle(sheet, fmt.Sprintf("%s%d", rDefCol, row), fmt.Sprintf("%s%d", rDefCol, row), w.st.data)
				if withAnswer {
					w.f.SetCellValue(sheet, fmt.Sprintf("%s%d", rWordCol, row), pw.Word)
					w.f.SetCellStyle(sheet, fmt.Sprintf("%s%d", rWordCol, row), fmt.Sprintf("%s%d", rWordCol, row), w.st.data)
				}
			}
			// Auto-check formulas (exercise sheet only)
			if isExer {
				dCell := fmt.Sprintf("D%d", row)
				dFormula := fmt.Sprintf(`IF(C%d=(_wpsfn.REGEXP(✅答案版!C%d,"[（(][^）)]*[）)]",2,"")),1,0)`, row, row)
				w.f.SetCellFormula(sheet, dCell, dFormula)

				hCell := fmt.Sprintf("H%d", row)
				hFormula := fmt.Sprintf(`IF(G%d=(_wpsfn.REGEXP(✅答案版!F%d,"[（(][^）)]*[）)]",2,"")),1,0)`, row, row)
				w.f.SetCellFormula(sheet, hCell, hFormula)
			}
		}
		currentRow = dataStart + maxRows
		// Blank separator row (its height matters: it determines where the
		// sentence section starts)
		if rowHt > 0 {
			w.f.SetRowHeight(sheet, currentRow, rowHt)
		}
		currentRow++
	}

	return currentRow
}

// renderSentenceSection writes the sentence exercise block.
//
// Layout differs by sheet:
//
//	练习版: | A=S{n} | B=中文 | C:F merged=空(书写区) |
//	答案版: | A=S{n} | B:C merged=中文 | D:F merged=日语 |
func (w *excelWriter) renderSentenceSection(sheet string, isExer, withAnswer bool,
	sentences []PlanSentence, startRow int) {

	if len(sentences) == 0 {
		return
	}

	currentRow := startRow
	rowHt := w.rowHeight(isExer)

	// Title row
	sTitleCell := fmt.Sprintf("A%d", currentRow)
	w.f.SetCellValue(sheet, sTitleCell, fmt.Sprintf("📝 造句 共%d句", len(sentences)))
	w.f.SetCellStyle(sheet, sTitleCell, sTitleCell, w.st.sectionHeader)
	w.f.MergeCell(sheet, sTitleCell, fmt.Sprintf("F%d", currentRow))
	if rowHt > 0 {
		w.f.SetRowHeight(sheet, currentRow, rowHt)
	}
	currentRow++

	// Column header row
	sHdr := currentRow
	w.f.SetCellValue(sheet, fmt.Sprintf("A%d", sHdr), "序号")
	w.f.SetCellStyle(sheet, fmt.Sprintf("A%d", sHdr), fmt.Sprintf("A%d", sHdr), w.st.sentenceHdr)

	if isExer {
		// 练习版: B=中文提示, C=日语(C:F merged header)
		w.f.SetCellValue(sheet, fmt.Sprintf("B%d", sHdr), "中文提示")
		w.f.SetCellStyle(sheet, fmt.Sprintf("B%d", sHdr), fmt.Sprintf("B%d", sHdr), w.st.sentenceHdr)
		w.f.SetCellValue(sheet, fmt.Sprintf("C%d", sHdr), "日语")
		w.f.SetCellStyle(sheet, fmt.Sprintf("C%d", sHdr), fmt.Sprintf("C%d", sHdr), w.st.sentenceHdr)
		w.f.MergeCell(sheet, fmt.Sprintf("C%d", sHdr), fmt.Sprintf("F%d", sHdr))
	} else {
		// 答案版: B:C merged=中文提示, D:F merged=日语
		w.f.SetCellValue(sheet, fmt.Sprintf("B%d", sHdr), "中文提示")
		w.f.SetCellStyle(sheet, fmt.Sprintf("B%d", sHdr), fmt.Sprintf("B%d", sHdr), w.st.sentenceHdr)
		w.f.MergeCell(sheet, fmt.Sprintf("B%d", sHdr), fmt.Sprintf("C%d", sHdr))
		w.f.SetCellValue(sheet, fmt.Sprintf("D%d", sHdr), "日语")
		w.f.SetCellStyle(sheet, fmt.Sprintf("D%d", sHdr), fmt.Sprintf("D%d", sHdr), w.st.sentenceHdr)
		w.f.MergeCell(sheet, fmt.Sprintf("D%d", sHdr), fmt.Sprintf("F%d", sHdr))
	}
	if rowHt > 0 {
		w.f.SetRowHeight(sheet, sHdr, rowHt)
	}
	currentRow++

	for i, s := range sentences {
		row := currentRow + i
		if rowHt > 0 {
			w.f.SetRowHeight(sheet, row, rowHt)
		}

		// 序号
		w.f.SetCellValue(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("S%d", s.Number))
		w.f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), w.st.numCell)

		if isExer {
			// 练习版: B=中文, C:F merged=书写空白
			w.f.SetCellValue(sheet, fmt.Sprintf("B%d", row), s.Chinese)
			w.f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), w.st.data)
			w.f.MergeCell(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("F%d", row))
		} else {
			// 答案版: B:C merged=中文, D:F merged=日语(答案)
			w.f.SetCellValue(sheet, fmt.Sprintf("B%d", row), s.Chinese)
			w.f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), w.st.data)
			w.f.MergeCell(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("C%d", row))
			if withAnswer {
				w.f.SetCellValue(sheet, fmt.Sprintf("D%d", row), s.Answer)
			}
			w.f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), w.st.data)
			w.f.MergeCell(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("F%d", row))
		}
	}
}

// groupPlanWordsByStatus buckets plan words by their Status field.
func groupPlanWordsByStatus(words []PlanWord) map[string][]PlanWord {
	byCat := map[string][]PlanWord{}
	for _, w := range words {
		byCat[w.Status] = append(byCat[w.Status], w)
	}
	return byCat
}

// writeTwoSheetWorkbook builds the standard 2-sheet workbook.
//
// Sheet1 (✏️练习版): definitions + blank columns for writing. Sentences give a
// wide writing area (C:F) so the student can hand-write Japanese answers.
// Sheet2 (✅答案版): same word layout, sentences with answers filled in.
func writeTwoSheetWorkbook(order []string, byCat map[string][]PlanWord,
	sentences []PlanSentence, outputPath string) error {

	w := newExcelWriter()
	defer w.f.Close()

	// === Sheet 1: ✏️练习版 ===
	sheet1 := "✏️练习版"
	w.f.SetSheetName(w.f.GetSheetName(0), sheet1)
	w.setColWidths(sheet1, true)
	next1 := w.renderWordSections(sheet1, true, false, order, byCat, 1)
	w.renderSentenceSection(sheet1, true, false, sentences, next1)

	// === Sheet 2: ✅答案版 ===
	sheet2 := "✅答案版"
	w.f.NewSheet(sheet2)
	w.setColWidths(sheet2, false)
	next2 := w.renderWordSections(sheet2, false, true, order, byCat, 1)
	w.renderSentenceSection(sheet2, false, true, sentences, next2)

	return w.f.SaveAs(outputPath)
}

// GenerateExcel creates the daily review Excel file with 2 sheets, grouping
// words by review category.
func GenerateExcel(plan *ReviewPlan, outputPath string) error {
	return writeTwoSheetWorkbook(
		reviewCategories,
		groupPlanWordsByStatus(plan.Words),
		plan.Sentences,
		outputPath,
	)
}

// GenerateHardExcel creates the hard-word ("钉子户") Excel file, grouping words
// by accuracy severity. No sentence exercises are included.
func GenerateHardExcel(plan *ReviewPlan, outputPath string) error {
	return writeTwoSheetWorkbook(
		hardCategories,
		groupPlanWordsByStatus(plan.Words),
		nil,
		outputPath,
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
