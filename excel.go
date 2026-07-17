package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// GenerateExcel creates a review Excel file with 2 sheets, matching the IMA layout.
//
// Sheet1 (✏️练习版): Chinese definitions + empty column for writing words, plus sentence exercises.
// Sheet2 (✅答案版): Same structure with answers filled in.
//
// Words are grouped by review category (钉子户/待巩固/待测试/基本掌握/抽查) into separate sections.
// Each section has a title row + column header row + word rows in two-column layout.
//
// Layout (6 columns, no gap):
//
//	| A序号 | B中文 | C日语 | D序号 | E中文 | F日语 |
//
// Column widths: A=5, B=17, C=20.5, D=5, E=17, F=22.5
// Header rows and 序号 cells have gray background (D9D9D9) with center alignment.
func GenerateExcel(plan *ReviewPlan, outputPath string) error {
	f := excelize.NewFile()
	defer f.Close()

	// --- Style definitions ---

	// Section header: bold 14pt, merged across A:F
	sectionHeaderStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14},
	})

	// Column header: bold, gray bg, center aligned
	headerStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	// 序号 cell: gray bg, center aligned (non-bold)
	numCellStyle, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"D9D9D9"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	// Normal data cell: center aligned
	dataStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})

	// Sentence header (merged): bold, gray bg, center
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

	// --- Column widths helper ---
	setColWidths := func(sheet string) {
		f.SetColWidth(sheet, "A", "A", 5)
		f.SetColWidth(sheet, "B", "B", 17)
		f.SetColWidth(sheet, "C", "C", 20.5)
		f.SetColWidth(sheet, "D", "D", 5)
		f.SetColWidth(sheet, "E", "E", 17)
		f.SetColWidth(sheet, "F", "F", 22.5)
	}

	// --- Render a single sheet ---
	renderSheet := func(sheet string, withAnswer bool) {
		currentRow := 1

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
				f.SetCellStyle(sheet, fmt.Sprintf("%s%d", col, hdr), fmt.Sprintf("%s%d", col, hdr), headerStyle)
			}
			currentRow++

			// Word rows: two-column layout (left A/B/C, right D/E/F)
			half := (len(words) + 1) / 2
			leftWords := words[:half]
			rightWords := words[half:]

			dataStart := currentRow
			// Left block
			for i, w := range leftWords {
				row := dataStart + i
				numCell := fmt.Sprintf("A%d", row)
				defCell := fmt.Sprintf("B%d", row)
				wordCell := fmt.Sprintf("C%d", row)
				f.SetCellValue(sheet, numCell, w.Number)
				f.SetCellValue(sheet, defCell, w.Definition)
				if withAnswer {
					f.SetCellValue(sheet, wordCell, w.Word)
				}
				f.SetCellStyle(sheet, numCell, numCell, numCellStyle)
				f.SetCellStyle(sheet, defCell, defCell, dataStyle)
				if withAnswer {
					f.SetCellStyle(sheet, wordCell, wordCell, dataStyle)
				}
			}
			// Right block
			for i, w := range rightWords {
				row := dataStart + i
				numCell := fmt.Sprintf("D%d", row)
				defCell := fmt.Sprintf("E%d", row)
				wordCell := fmt.Sprintf("F%d", row)
				f.SetCellValue(sheet, numCell, w.Number)
				f.SetCellValue(sheet, defCell, w.Definition)
				if withAnswer {
					f.SetCellValue(sheet, wordCell, w.Word)
				}
				f.SetCellStyle(sheet, numCell, numCell, numCellStyle)
				f.SetCellStyle(sheet, defCell, defCell, dataStyle)
				if withAnswer {
					f.SetCellStyle(sheet, wordCell, wordCell, dataStyle)
				}
			}

			// Advance past the word rows
			currentRow = dataStart + max(len(leftWords), len(rightWords))

			// Blank separator row
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
		currentRow++

		// Column header row: A=序号, B:C=中文提示, D:F=日语
		sHdr := currentRow
		f.SetCellValue(sheet, fmt.Sprintf("A%d", sHdr), "序号")
		f.SetCellStyle(sheet, fmt.Sprintf("A%d", sHdr), fmt.Sprintf("A%d", sHdr), sentenceHeaderStyle)

		bcCell := fmt.Sprintf("B%d", sHdr)
		f.SetCellValue(sheet, bcCell, "中文提示")
		f.SetCellStyle(sheet, bcCell, bcCell, sentenceHeaderStyle)
		f.MergeCell(sheet, bcCell, fmt.Sprintf("C%d", sHdr))

		dfCell := fmt.Sprintf("D%d", sHdr)
		f.SetCellValue(sheet, dfCell, "日语")
		f.SetCellStyle(sheet, dfCell, dfCell, sentenceHeaderStyle)
		f.MergeCell(sheet, dfCell, fmt.Sprintf("F%d", sHdr))
		currentRow++

		// Sentence data rows: A=S{n}, B:C=chinese(merged), D:F=answer(merged)
		for i, s := range plan.Sentences {
			row := currentRow + i

			// 序号
			numCell := fmt.Sprintf("A%d", row)
			f.SetCellValue(sheet, numCell, fmt.Sprintf("S%d", s.Number))
			f.SetCellStyle(sheet, numCell, numCell, numCellStyle)

			// Chinese (B:C merged)
			chiCell := fmt.Sprintf("B%d", row)
			f.SetCellValue(sheet, chiCell, s.Chinese)
			f.SetCellStyle(sheet, chiCell, chiCell, dataStyle)
			f.MergeCell(sheet, chiCell, fmt.Sprintf("C%d", row))

			// Answer (D:F merged)
			ansCell := fmt.Sprintf("D%d", row)
			if withAnswer {
				f.SetCellValue(sheet, ansCell, s.Answer)
			}
			f.SetCellStyle(sheet, ansCell, ansCell, dataStyle)
			f.MergeCell(sheet, ansCell, fmt.Sprintf("F%d", row))
		}
		currentRow += len(plan.Sentences)
	}

	// === Sheet 1: ✏️练习版 ===
	sheet1 := "✏️练习版"
	f.SetSheetName(f.GetSheetName(0), sheet1)
	setColWidths(sheet1)
	renderSheet(sheet1, false)

	// === Sheet 2: ✅答案版 ===
	sheet2 := "✅答案版"
	f.NewSheet(sheet2)
	setColWidths(sheet2)
	renderSheet(sheet2, true)

	return f.SaveAs(outputPath)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
