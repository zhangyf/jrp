package main

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

// GenerateExcel creates a review Excel file with 2 sheets.
// Sheet1 (复习): Chinese definitions + empty column for writing words, plus sentence exercises.
// Sheet2 (答案): Same structure with answers filled in.
func GenerateExcel(plan *ReviewPlan, outputPath string) error {
	f := excelize.NewFile()
	defer f.Close()

	cfg := LangConfigs[plan.Language]

	// Create a bold style for reuse
	boldStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})

	setBold := func(sheet, cell string) {
		f.SetCellStyle(sheet, cell, cell, boldStyle)
	}

	// === Sheet 1: 复习 (Review - fill in) ===
	sheet1 := "复习"
	f.SetSheetName(f.GetSheetName(0), sheet1)

	// Title
	f.SetCellValue(sheet1, "A1", fmt.Sprintf("%s单词复习 %s", cfg.Name, plan.Date))
	setBold(sheet1, "A1")
	f.MergeCell(sheet1, "A1", "B1")

	// Word section header
	f.SetCellValue(sheet1, "A3", "序号")
	f.SetCellValue(sheet1, "B3", "中文释义")
	f.SetCellValue(sheet1, "C3", cfg.WordColumn+"（填写）")
	setBold(sheet1, "A3")
	setBold(sheet1, "B3")
	setBold(sheet1, "C3")

	// Word entries
	for i, w := range plan.Words {
		row := i + 4
		f.SetCellValue(sheet1, fmt.Sprintf("A%d", row), w.Number)
		f.SetCellValue(sheet1, fmt.Sprintf("B%d", row), w.Definition)
		// Column C left empty for user to fill in
	}

	// Sentence section
	sentenceStartRow := len(plan.Words) + 6
	f.SetCellValue(sheet1, fmt.Sprintf("A%d", sentenceStartRow), "造句练习")
	setBold(sheet1, fmt.Sprintf("A%d", sentenceStartRow))
	f.MergeCell(sheet1, fmt.Sprintf("A%d", sentenceStartRow), fmt.Sprintf("C%d", sentenceStartRow))

	f.SetCellValue(sheet1, fmt.Sprintf("A%d", sentenceStartRow+1), "序号")
	f.SetCellValue(sheet1, fmt.Sprintf("B%d", sentenceStartRow+1), "中文释义")
	f.SetCellValue(sheet1, fmt.Sprintf("C%d", sentenceStartRow+1), cfg.Name+"句子（填写）")
	setBold(sheet1, fmt.Sprintf("A%d", sentenceStartRow+1))
	setBold(sheet1, fmt.Sprintf("B%d", sentenceStartRow+1))
	setBold(sheet1, fmt.Sprintf("C%d", sentenceStartRow+1))

	for i, s := range plan.Sentences {
		row := sentenceStartRow + 2 + i
		f.SetCellValue(sheet1, fmt.Sprintf("A%d", row), s.Number)
		f.SetCellValue(sheet1, fmt.Sprintf("B%d", row), s.Chinese)
		// Column C left empty
	}

	// Set column widths
	f.SetColWidth(sheet1, "A", "A", 8)
	f.SetColWidth(sheet1, "B", "B", 40)
	f.SetColWidth(sheet1, "C", "C", 30)

	// === Sheet 2: 答案 (Answers) ===
	sheet2 := "答案"
	f.NewSheet(sheet2)

	// Title
	f.SetCellValue(sheet2, "A1", fmt.Sprintf("%s单词复习答案 %s", cfg.Name, plan.Date))
	setBold(sheet2, "A1")
	f.MergeCell(sheet2, "A1", "B1")

	// Word section header
	f.SetCellValue(sheet2, "A3", "序号")
	f.SetCellValue(sheet2, "B3", "中文释义")
	f.SetCellValue(sheet2, "C3", cfg.WordColumn)
	setBold(sheet2, "A3")
	setBold(sheet2, "B3")
	setBold(sheet2, "C3")

	// Word entries with answers
	for i, w := range plan.Words {
		row := i + 4
		f.SetCellValue(sheet2, fmt.Sprintf("A%d", row), w.Number)
		f.SetCellValue(sheet2, fmt.Sprintf("B%d", row), w.Definition)
		f.SetCellValue(sheet2, fmt.Sprintf("C%d", row), w.Word)
	}

	// Sentence section
	f.SetCellValue(sheet2, fmt.Sprintf("A%d", sentenceStartRow), "造句练习答案")
	setBold(sheet2, fmt.Sprintf("A%d", sentenceStartRow))
	f.MergeCell(sheet2, fmt.Sprintf("A%d", sentenceStartRow), fmt.Sprintf("C%d", sentenceStartRow))

	f.SetCellValue(sheet2, fmt.Sprintf("A%d", sentenceStartRow+1), "序号")
	f.SetCellValue(sheet2, fmt.Sprintf("B%d", sentenceStartRow+1), "中文释义")
	f.SetCellValue(sheet2, fmt.Sprintf("C%d", sentenceStartRow+1), cfg.Name+"句子")
	setBold(sheet2, fmt.Sprintf("A%d", sentenceStartRow+1))
	setBold(sheet2, fmt.Sprintf("B%d", sentenceStartRow+1))
	setBold(sheet2, fmt.Sprintf("C%d", sentenceStartRow+1))

	for i, s := range plan.Sentences {
		row := sentenceStartRow + 2 + i
		f.SetCellValue(sheet2, fmt.Sprintf("A%d", row), s.Number)
		f.SetCellValue(sheet2, fmt.Sprintf("B%d", row), s.Chinese)
		f.SetCellValue(sheet2, fmt.Sprintf("C%d", row), s.Answer)
	}

	// Set column widths
	f.SetColWidth(sheet2, "A", "A", 8)
	f.SetColWidth(sheet2, "B", "B", 40)
	f.SetColWidth(sheet2, "C", "C", 30)

	// Save
	return f.SaveAs(outputPath)
}
