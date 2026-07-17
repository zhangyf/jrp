package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// GenerateExcel creates a review Excel file with 2 sheets.
// Sheet1 (复习): Chinese definitions + empty column for writing words, plus sentence exercises.
// Sheet2 (答案): Same structure with answers filled in.
//
// Layout: two column-blocks side by side, separated by a narrow gap column.
//
//	| 序号  | 中文释义 | 日语（填写） | gap | 序号  | 中文释义 | 日语（填写） |
//	| 1🔴  | xxx      |              |     | 41🟢 | xxx      |              |
//
// The 序号 cell contains the number + status emoji.
// Words are sorted by status priority (钉子户 first).
// Sentences use the same two-column layout.
func GenerateExcel(plan *ReviewPlan, outputPath string) error {
	f := excelize.NewFile()
	defer f.Close()

	cfg := LangConfigs[plan.Language]

	// Bold style for headers
	boldStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})
	setBold := func(sheet, cell string) {
		f.SetCellStyle(sheet, cell, cell, boldStyle)
	}

	// Count words by status for the summary
	statusCounts := map[string]int{}
	for _, w := range plan.Words {
		statusCounts[w.Status]++
	}

	// Split words into two halves
	half := (len(plan.Words) + 1) / 2 // left gets extra if odd
	leftWords := plan.Words[:half]
	rightWords := plan.Words[half:]

	// Split sentences into two halves
	sHalf := (len(plan.Sentences) + 1) / 2
	leftSentences := plan.Sentences[:sHalf]
	rightSentences := plan.Sentences[sHalf:]

	// === Sheet 1: 复习 (Review - fill in) ===
	sheet1 := "复习"
	f.SetSheetName(f.GetSheetName(0), sheet1)

	// Title spanning both blocks
	f.SetCellValue(sheet1, "A1", fmt.Sprintf("%s单词复习 %s", cfg.Name, plan.Date))
	setBold(sheet1, "A1")
	f.MergeCell(sheet1, "A1", "G1")

	// Status summary row (row 2)
	summary := ""
	if c := statusCounts["🔴钉子户"]; c > 0 {
		summary += fmt.Sprintf("🔴钉子户%d ", c)
	}
	if c := statusCounts["🔴待巩固"]; c > 0 {
		summary += fmt.Sprintf("🔴待巩固%d ", c)
	}
	if c := statusCounts["🔄待测试"]; c > 0 {
		summary += fmt.Sprintf("🔄待测试%d ", c)
	}
	if c := statusCounts["🟡基本掌握"]; c > 0 {
		summary += fmt.Sprintf("🟡基本掌握%d ", c)
	}
	if c := statusCounts["🟢抽查"]; c > 0 {
		summary += fmt.Sprintf("🟢抽查%d ", c)
	}
	f.SetCellValue(sheet1, "A2", summary)
	f.MergeCell(sheet1, "A2", "G2")

	// Word section headers (row 3)
	f.SetCellValue(sheet1, "A3", "序号")
	f.SetCellValue(sheet1, "B3", "中文释义")
	f.SetCellValue(sheet1, "C3", cfg.WordColumn+"（填写）")
	setBold(sheet1, "A3")
	setBold(sheet1, "B3")
	setBold(sheet1, "C3")

	f.SetCellValue(sheet1, "E3", "序号")
	f.SetCellValue(sheet1, "F3", "中文释义")
	f.SetCellValue(sheet1, "G3", cfg.WordColumn+"（填写）")
	setBold(sheet1, "E3")
	setBold(sheet1, "F3")
	setBold(sheet1, "G3")

	// Word entries: 序号 cell shows "N🔴" format (number + status emoji)
	writeWordBlock := func(sheet string, words []PlanWord, startRow int, numCol, defCol, wordCol string, withAnswer bool) {
		for i, w := range words {
			row := startRow + i
			// Number + status emoji in the 序号 cell
			var numLabel string
			if w.Status != "" {
				runes := []rune(w.Status)
				numLabel = fmt.Sprintf("%d%s", w.Number, string(runes[0]))
			} else {
				numLabel = fmt.Sprintf("%d", w.Number)
			}
			f.SetCellValue(sheet, fmt.Sprintf("%s%d", numCol, row), numLabel)
			f.SetCellValue(sheet, fmt.Sprintf("%s%d", defCol, row), w.Definition)
			if withAnswer {
				f.SetCellValue(sheet, fmt.Sprintf("%s%d", wordCol, row), w.Word)
			}
		}
	}

	wordStartRow := 4
	writeWordBlock(sheet1, leftWords, wordStartRow, "A", "B", "C", false)
	writeWordBlock(sheet1, rightWords, wordStartRow, "E", "F", "G", false)

	// Sentence section
	sentenceStartRow := wordStartRow + max(len(leftWords), len(rightWords)) + 2

	f.SetCellValue(sheet1, fmt.Sprintf("A%d", sentenceStartRow), "造句练习")
	setBold(sheet1, fmt.Sprintf("A%d", sentenceStartRow))
	f.MergeCell(sheet1, fmt.Sprintf("A%d", sentenceStartRow), fmt.Sprintf("G%d", sentenceStartRow))

	// Sentence headers
	sHeaderRow := sentenceStartRow + 1
	f.SetCellValue(sheet1, fmt.Sprintf("A%d", sHeaderRow), "序号")
	f.SetCellValue(sheet1, fmt.Sprintf("B%d", sHeaderRow), "中文")
	f.SetCellValue(sheet1, fmt.Sprintf("C%d", sHeaderRow), cfg.Name+"句子（填写）")
	setBold(sheet1, fmt.Sprintf("A%d", sHeaderRow))
	setBold(sheet1, fmt.Sprintf("B%d", sHeaderRow))
	setBold(sheet1, fmt.Sprintf("C%d", sHeaderRow))

	f.SetCellValue(sheet1, fmt.Sprintf("E%d", sHeaderRow), "序号")
	f.SetCellValue(sheet1, fmt.Sprintf("F%d", sHeaderRow), "中文")
	f.SetCellValue(sheet1, fmt.Sprintf("G%d", sHeaderRow), cfg.Name+"句子（填写）")
	setBold(sheet1, fmt.Sprintf("E%d", sHeaderRow))
	setBold(sheet1, fmt.Sprintf("F%d", sHeaderRow))
	setBold(sheet1, fmt.Sprintf("G%d", sHeaderRow))

	writeSentenceBlock := func(sheet string, sentences []PlanSentence, startRow int, numCol, chiCol, ansCol string, withAnswer bool) {
		for i, s := range sentences {
			row := startRow + i
			f.SetCellValue(sheet, fmt.Sprintf("%s%d", numCol, row), s.Number)
			f.SetCellValue(sheet, fmt.Sprintf("%s%d", chiCol, row), s.Chinese)
			if withAnswer {
				f.SetCellValue(sheet, fmt.Sprintf("%s%d", ansCol, row), s.Answer)
			}
		}
	}

	sStartRow := sHeaderRow + 1
	writeSentenceBlock(sheet1, leftSentences, sStartRow, "A", "B", "C", false)
	writeSentenceBlock(sheet1, rightSentences, sStartRow, "E", "F", "G", false)

	// Column widths
	f.SetColWidth(sheet1, "A", "A", 10)
	f.SetColWidth(sheet1, "B", "B", 35)
	f.SetColWidth(sheet1, "C", "C", 25)
	f.SetColWidth(sheet1, "D", "D", 3)
	f.SetColWidth(sheet1, "E", "E", 10)
	f.SetColWidth(sheet1, "F", "F", 35)
	f.SetColWidth(sheet1, "G", "G", 25)

	// === Sheet 2: 答案 (Answers) ===
	sheet2 := "答案"
	f.NewSheet(sheet2)

	// Title
	f.SetCellValue(sheet2, "A1", fmt.Sprintf("%s单词复习答案 %s", cfg.Name, plan.Date))
	setBold(sheet2, "A1")
	f.MergeCell(sheet2, "A1", "G1")

	// Status summary row
	f.SetCellValue(sheet2, "A2", summary)
	f.MergeCell(sheet2, "A2", "G2")

	// Word section headers (row 3)
	f.SetCellValue(sheet2, "A3", "序号")
	f.SetCellValue(sheet2, "B3", "中文释义")
	f.SetCellValue(sheet2, "C3", cfg.WordColumn)
	setBold(sheet2, "A3")
	setBold(sheet2, "B3")
	setBold(sheet2, "C3")

	f.SetCellValue(sheet2, "E3", "序号")
	f.SetCellValue(sheet2, "F3", "中文释义")
	f.SetCellValue(sheet2, "G3", cfg.WordColumn)
	setBold(sheet2, "E3")
	setBold(sheet2, "F3")
	setBold(sheet2, "G3")

	// Word entries with answers
	writeWordBlock(sheet2, leftWords, wordStartRow, "A", "B", "C", true)
	writeWordBlock(sheet2, rightWords, wordStartRow, "E", "F", "G", true)

	// Sentence section
	f.SetCellValue(sheet2, fmt.Sprintf("A%d", sentenceStartRow), "造句练习答案")
	setBold(sheet2, fmt.Sprintf("A%d", sentenceStartRow))
	f.MergeCell(sheet2, fmt.Sprintf("A%d", sentenceStartRow), fmt.Sprintf("G%d", sentenceStartRow))

	f.SetCellValue(sheet2, fmt.Sprintf("A%d", sHeaderRow), "序号")
	f.SetCellValue(sheet2, fmt.Sprintf("B%d", sHeaderRow), "中文")
	f.SetCellValue(sheet2, fmt.Sprintf("C%d", sHeaderRow), cfg.Name+"句子")
	setBold(sheet2, fmt.Sprintf("A%d", sHeaderRow))
	setBold(sheet2, fmt.Sprintf("B%d", sHeaderRow))
	setBold(sheet2, fmt.Sprintf("C%d", sHeaderRow))

	f.SetCellValue(sheet2, fmt.Sprintf("E%d", sHeaderRow), "序号")
	f.SetCellValue(sheet2, fmt.Sprintf("F%d", sHeaderRow), "中文")
	f.SetCellValue(sheet2, fmt.Sprintf("G%d", sHeaderRow), cfg.Name+"句子")
	setBold(sheet2, fmt.Sprintf("E%d", sHeaderRow))
	setBold(sheet2, fmt.Sprintf("F%d", sHeaderRow))
	setBold(sheet2, fmt.Sprintf("G%d", sHeaderRow))

	writeSentenceBlock(sheet2, leftSentences, sStartRow, "A", "B", "C", true)
	writeSentenceBlock(sheet2, rightSentences, sStartRow, "E", "F", "G", true)

	// Column widths
	f.SetColWidth(sheet2, "A", "A", 10)
	f.SetColWidth(sheet2, "B", "B", 35)
	f.SetColWidth(sheet2, "C", "C", 25)
	f.SetColWidth(sheet2, "D", "D", 3)
	f.SetColWidth(sheet2, "E", "E", 10)
	f.SetColWidth(sheet2, "F", "F", 35)
	f.SetColWidth(sheet2, "G", "G", 25)

	return f.SaveAs(outputPath)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
