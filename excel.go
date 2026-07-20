package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// GenerateExcel creates a review Excel file with 2 sheets.
//
// Sheet1 (✏️练习版): definitions + blank columns for writing.  Sentences give a wide
// writing area (C:F) so the student can hand-write Japanese answers.
// Sheet2 (✅答案版): Same word layout, sentences with answers filled in.
//
// Word layout (6 columns):
//
//	| A序号 | B中文 | C日语 | D序号 | E中文 | F日语 |
//
// Sentence layout differs by sheet:
//
//	练习版: | A=S{n} | B=中文 | C:F merged=空(书写区) |  — B wider (19.33)
//	答案版: | A=S{n} | B:C merged=中文 | D:F merged=日语   — standard widths
func GenerateExcel(plan *ReviewPlan, outputPath string) error {
	f := excelize.NewFile()
	defer f.Close()

	// --- Style definitions ---

	sectionHeaderStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14},
	})

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	numCellStyle, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	dataStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	sentenceHeaderStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	// --- Group words by category ---
	categories := []string{
		"☠️钉子户", "🔴待巩固", "🔄待测试", "🟡基本掌握", "🟢抽查",
	}
	groupsByCategory := map[string][]PlanWord{}
	for _, w := range plan.Words {
		groupsByCategory[w.Status] = append(groupsByCategory[w.Status], w)
	}

	// --- Render word sections (shared by both sheets) ---
	type renderOpts struct {
		exerRowHt  float64 // exercise sheet row height (0 = default)
		ansRowHt   float64 // answer sheet row height (0 = default)
		exerBWidth float64 // exercise B col width
		exerCWidth float64
		ansBWidth  float64
		ansCWidth  float64
	}

	opts := renderOpts{
		exerRowHt:  25.05,
		exerBWidth: 19.33,
		exerCWidth: 18.66,
		ansBWidth:  17,
		ansCWidth:  20.5,
	}

	setColWidths := func(sheet string, isExer bool) {
		bw, cw := opts.ansBWidth, opts.ansCWidth
		if isExer {
			bw, cw = opts.exerBWidth, opts.exerCWidth
		}
		f.SetColWidth(sheet, "A", "A", 5)
		f.SetColWidth(sheet, "B", "B", bw)
		f.SetColWidth(sheet, "C", "C", cw)
		f.SetColWidth(sheet, "D", "D", 5)
		f.SetColWidth(sheet, "E", "E", 17)
		f.SetColWidth(sheet, "F", "F", 22.5)
	}

	renderSheet := func(sheet string, isExer, withAnswer bool) {
		currentRow := 1

		rowHt := 0.0
		if isExer {
			rowHt = opts.exerRowHt
		}

		for _, cat := range categories {
			words := groupsByCategory[cat]
			if len(words) == 0 {
				continue
			}

			// Section title row (merged A:F)
			titleCell := fmt.Sprintf("A%d", currentRow)
			f.SetCellValue(sheet, titleCell, fmt.Sprintf("%s %d词", cat, len(words)))
			f.SetCellStyle(sheet, titleCell, titleCell, sectionHeaderStyle)
			f.MergeCell(sheet, titleCell, fmt.Sprintf("F%d", currentRow))
			if rowHt > 0 {
				f.SetRowHeight(sheet, currentRow, rowHt)
			}
			currentRow++

			// Column header row
			hdr := currentRow
			f.SetCellValue(sheet, fmt.Sprintf("A%d", hdr), "序号")
			f.SetCellValue(sheet, fmt.Sprintf("B%d", hdr), "中文")
			f.SetCellValue(sheet, fmt.Sprintf("C%d", hdr), "日语")
			f.SetCellValue(sheet, fmt.Sprintf("D%d", hdr), "序号")
			f.SetCellValue(sheet, fmt.Sprintf("E%d", hdr), "中文")
			f.SetCellValue(sheet, fmt.Sprintf("F%d", hdr), "日语")
			for _, col := range []string{"A", "B", "C", "D", "E", "F"} {
				cellRef := fmt.Sprintf("%s%d", col, hdr)
				f.SetCellStyle(sheet, cellRef, cellRef, headerStyle)
			}
			if rowHt > 0 {
				f.SetRowHeight(sheet, hdr, rowHt)
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
					f.SetRowHeight(sheet, row, rowHt)
				}
				// Left block
				if i < len(leftWords) {
					w := leftWords[i]
					f.SetCellValue(sheet, fmt.Sprintf("A%d", row), w.Number)
					f.SetCellValue(sheet, fmt.Sprintf("B%d", row), w.Definition)
					f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), numCellStyle)
					f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), dataStyle)
					if withAnswer {
						f.SetCellValue(sheet, fmt.Sprintf("C%d", row), w.Word)
						f.SetCellStyle(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("C%d", row), dataStyle)
					}
				}
				// Right block
				if i < len(rightWords) {
					w := rightWords[i]
					f.SetCellValue(sheet, fmt.Sprintf("D%d", row), w.Number)
					f.SetCellValue(sheet, fmt.Sprintf("E%d", row), w.Definition)
					f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), numCellStyle)
					f.SetCellStyle(sheet, fmt.Sprintf("E%d", row), fmt.Sprintf("E%d", row), dataStyle)
					if withAnswer {
						f.SetCellValue(sheet, fmt.Sprintf("F%d", row), w.Word)
						f.SetCellStyle(sheet, fmt.Sprintf("F%d", row), fmt.Sprintf("F%d", row), dataStyle)
					}
				}
			}
			currentRow = dataStart + maxRows
			// Blank separator row
			if rowHt > 0 {
				f.SetRowHeight(sheet, currentRow, rowHt)
			}
			currentRow++
		}

		// --- Sentence section ---
		if len(plan.Sentences) == 0 {
			return
		}

		// Title row
		sTitleCell := fmt.Sprintf("A%d", currentRow)
		f.SetCellValue(sheet, sTitleCell, fmt.Sprintf("📝 造句 共%d句", len(plan.Sentences)))
		f.SetCellStyle(sheet, sTitleCell, sTitleCell, sectionHeaderStyle)
		f.MergeCell(sheet, sTitleCell, fmt.Sprintf("F%d", currentRow))
		if rowHt > 0 {
			f.SetRowHeight(sheet, currentRow, rowHt)
		}
		currentRow++

		// Column header row
		sHdr := currentRow
		f.SetCellValue(sheet, fmt.Sprintf("A%d", sHdr), "序号")
		f.SetCellStyle(sheet, fmt.Sprintf("A%d", sHdr), fmt.Sprintf("A%d", sHdr), sentenceHeaderStyle)

		if isExer {
			// 练习版: B=中文提示, C=日语(C:F merged header)
			f.SetCellValue(sheet, fmt.Sprintf("B%d", sHdr), "中文提示")
			f.SetCellStyle(sheet, fmt.Sprintf("B%d", sHdr), fmt.Sprintf("B%d", sHdr), sentenceHeaderStyle)
			f.SetCellValue(sheet, fmt.Sprintf("C%d", sHdr), "日语")
			f.SetCellStyle(sheet, fmt.Sprintf("C%d", sHdr), fmt.Sprintf("C%d", sHdr), sentenceHeaderStyle)
			f.MergeCell(sheet, fmt.Sprintf("C%d", sHdr), fmt.Sprintf("F%d", sHdr))
		} else {
			// 答案版: B:C merged=中文提示, D:F merged=日语
			f.SetCellValue(sheet, fmt.Sprintf("B%d", sHdr), "中文提示")
			f.SetCellStyle(sheet, fmt.Sprintf("B%d", sHdr), fmt.Sprintf("B%d", sHdr), sentenceHeaderStyle)
			f.MergeCell(sheet, fmt.Sprintf("B%d", sHdr), fmt.Sprintf("C%d", sHdr))
			f.SetCellValue(sheet, fmt.Sprintf("D%d", sHdr), "日语")
			f.SetCellStyle(sheet, fmt.Sprintf("D%d", sHdr), fmt.Sprintf("D%d", sHdr), sentenceHeaderStyle)
			f.MergeCell(sheet, fmt.Sprintf("D%d", sHdr), fmt.Sprintf("F%d", sHdr))
		}
		if rowHt > 0 {
			f.SetRowHeight(sheet, sHdr, rowHt)
		}
		currentRow++

		for i, s := range plan.Sentences {
			row := currentRow + i
			if rowHt > 0 {
				f.SetRowHeight(sheet, row, rowHt)
			}

			// 序号
			f.SetCellValue(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("S%d", s.Number))
			f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("A%d", row), numCellStyle)

			if isExer {
				// 练习版: B=中文, C:F merged=书写空白
				f.SetCellValue(sheet, fmt.Sprintf("B%d", row), s.Chinese)
				f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), dataStyle)
				f.MergeCell(sheet, fmt.Sprintf("C%d", row), fmt.Sprintf("F%d", row))
			} else {
				// 答案版: B:C merged=中文, D:F merged=日语(答案)
				f.SetCellValue(sheet, fmt.Sprintf("B%d", row), s.Chinese)
				f.SetCellStyle(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("B%d", row), dataStyle)
				f.MergeCell(sheet, fmt.Sprintf("B%d", row), fmt.Sprintf("C%d", row))
				if withAnswer {
					f.SetCellValue(sheet, fmt.Sprintf("D%d", row), s.Answer)
				}
				f.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), dataStyle)
				f.MergeCell(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("F%d", row))
			}
		}
	}

	// === Sheet 1: ✏️练习版 ===
	sheet1 := "✏️练习版"
	f.SetSheetName(f.GetSheetName(0), sheet1)
	setColWidths(sheet1, true)
	renderSheet(sheet1, true, false)

	// === Sheet 2: ✅答案版 ===
	sheet2 := "✅答案版"
	f.NewSheet(sheet2)
	setColWidths(sheet2, false)
	renderSheet(sheet2, false, true)

	return f.SaveAs(outputPath)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
