package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// 造一份练习计划，生成 Excel，模拟老师填几个答案，再解析回来。
//
// 全程在内存 + 临时文件里跑，不碰 COS。
func TestExcelRoundTrip(t *testing.T) {
	plan := &ReviewPlan{
		Date:     "2026-09-20",
		Language: "ja",
		Words: []PlanWord{
			{Number: 1, Word: "おんがく(音楽)", Definition: "音乐", Status: "☠️钉子户"},
			{Number: 2, Word: "カレー", Definition: "咖喱", Status: "☠️钉子户"},
			{Number: 3, Word: "はじめまして", Definition: "初次见面", Status: "🔴待巩固"},
			{Number: 4, Word: "てぶくろ(手袋)", Definition: "手套", Status: "🔴待巩固"},
			{Number: 5, Word: "としょかん(図書館)", Definition: "图书馆", Status: "🔄待测试"},
		},
		Sentences: []PlanSentence{
			{Number: 1, Chinese: "小李每天喝咖啡。", Answer: "李さんは 毎日 コーヒーを 飲みます。"},
			{Number: 2, Chinese: "这里是便利店。", Answer: "ここは コンビニです。"},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "review_2026-09-20_v1.0.xlsx")
	meta := &ExcelMeta{PlanDate: "2026-09-20", Language: "ja", Mode: "daily"}
	if err := GenerateExcelWithMeta(plan, path, meta); err != nil {
		t.Fatalf("生成 Excel 失败：%v", err)
	}

	// --- 先验证 _meta 写进去了 ---
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("打开失败：%v", err)
	}
	mrows, err := f.GetRows(metaSheetName)
	if err != nil {
		t.Fatalf("读不到 %s：%v", metaSheetName, err)
	}
	got := map[string]string{}
	for _, r := range mrows {
		if len(r) >= 2 {
			got[r[0]] = r[1]
		}
	}
	if got["plan_date"] != "2026-09-20" || got["language"] != "ja" || got["mode"] != "daily" {
		t.Errorf("_meta 内容不对：%v", got)
	}
	f.Close()

	// --- 模拟老师填写 ---
	// 1 写假名（对）、2 写平假名（假名档折叠后对）、3 写错、4 留空、5 写全形（对）
	// 造句 1 写对、造句 2 写错
	fills := map[int]string{1: "おんがく", 2: "かれー", 3: "はじめました", 5: "としょかん(図書館)"}
	f2, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f2.GetRows(exerciseSheetName)
	if err != nil {
		t.Fatal(err)
	}
	writeAt := func(rowIdx int, colLetter, val string) {
		if err := f2.SetCellValue(exerciseSheetName, colLetter+itoa(rowIdx), val); err != nil {
			t.Fatal(err)
		}
	}
	sentenceRows := map[int]int{}
	for i, r := range rows {
		rowNo := i + 1
		a := col(r, 0)
		// 左栏（A=序号，写 C）
		if n, err := atoi(a); err == nil {
			if v, ok := fills[n]; ok {
				writeAt(rowNo, "C", v)
			}
		}
		// 右栏和左栏同一行（E=序号，写 G），不能 continue
		if len(r) > 4 {
			if n, err := atoi(r[4]); err == nil {
				if v, ok := fills[n]; ok {
					writeAt(rowNo, "G", v)
				}
			}
		}
		if m := sentenceNumRe.FindStringSubmatch(a); m != nil {
			n, _ := atoi(m[1])
			sentenceRows[n] = rowNo
		}
	}
	writeAt(sentenceRows[1], "C", "李さんは毎日コーヒーを飲みます")
	writeAt(sentenceRows[2], "C", "ここはデパートです")
	if err := f2.Save(); err != nil {
		t.Fatal(err)
	}
	f2.Close()

	// --- 解析回来 ---
	prev, err := ParseExerciseFile(path, GradeKana)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	if prev.PlanDate != "2026-09-20" {
		t.Errorf("plan_date = %q, want 2026-09-20", prev.PlanDate)
	}
	if prev.Language != "ja" || prev.Mode != "daily" {
		t.Errorf("meta 解析不全：lang=%q mode=%q", prev.Language, prev.Mode)
	}

	if len(prev.Words) != 5 {
		t.Fatalf("解析出 %d 个词，want 5", len(prev.Words))
	}
	byNum := map[int]ImportWord{}
	for _, w := range prev.Words {
		byNum[w.Number] = w
	}

	// 序号连续且答案对得上
	for i, w := range prev.Words {
		if w.Number != i+1 {
			t.Errorf("序号乱了：%v", prev.Words)
		}
	}
	if byNum[1].AutoCorrect != true {
		t.Errorf("1 写假名应判对，got input=%q answer=%q", byNum[1].Input, byNum[1].Answer)
	}
	if byNum[2].AutoCorrect != true {
		t.Errorf("2 片假名词写平假名应判对，got input=%q", byNum[2].Input)
	}
	if byNum[3].AutoCorrect != false {
		t.Errorf("3 写错应判错，got input=%q", byNum[3].Input)
	}
	if byNum[5].AutoCorrect != true {
		t.Errorf("5 照抄全形应判对，got input=%q", byNum[5].Input)
	}

	// 空白 = 没写 → 进 SkippedBlank，不判对
	if len(prev.SkippedBlank) != 1 || prev.SkippedBlank[0] != 4 {
		t.Errorf("SkippedBlank = %v, want [4]", prev.SkippedBlank)
	}
	if byNum[4].Input != "" || byNum[4].AutoCorrect {
		t.Errorf("空白题不该有输入也不该判对：%+v", byNum[4])
	}

	// 造句
	if len(prev.Sentences) != 2 {
		t.Fatalf("解析出 %d 句，want 2", len(prev.Sentences))
	}
	if !prev.Sentences[0].AutoCorrect {
		t.Errorf("造句 1 归一化后应判对：%+v", prev.Sentences[0])
	}
	if prev.Sentences[1].AutoCorrect {
		t.Errorf("造句 2 写错了不该判对：%+v", prev.Sentences[1])
	}
	for _, s := range prev.Sentences {
		if s.Confidence != "low" {
			t.Errorf("造句置信度应恒为 low：%+v", s)
		}
	}
}

// 没有 _meta 的老导出，要能从文件名兜底抠出日期。
func TestParseExerciseFileFallbackDate(t *testing.T) {
	plan := &ReviewPlan{
		Date:     "2026-09-18",
		Language: "ja",
		Words: []PlanWord{
			{Number: 1, Word: "おんがく(音楽)", Definition: "音乐", Status: "🔄待测试"},
		},
	}
	dir := t.TempDir()
	// 故意不带 meta，文件名带日期
	path := filepath.Join(dir, "review_2026-09-18_v1.2.xlsx")
	if err := GenerateExcel(plan, path); err != nil {
		t.Fatal(err)
	}
	prev, err := ParseExerciseFile(path, GradeKana)
	if err != nil {
		t.Fatal(err)
	}
	if prev.PlanDate != "2026-09-18" {
		t.Errorf("兜底日期 = %q, want 2026-09-18", prev.PlanDate)
	}
	if len(prev.Words) != 1 {
		t.Errorf("解析出 %d 个词，want 1", len(prev.Words))
	}
	// 全空白 → 全部跳过
	if len(prev.SkippedBlank) != 1 {
		t.Errorf("SkippedBlank = %v, want [1]", prev.SkippedBlank)
	}
}

// 汉字档 / 完整档要能在导入预览里生效（老师在预览页可以切档重判）。
func TestParseExerciseFileGradeModes(t *testing.T) {
	plan := &ReviewPlan{
		Date:     "2026-09-20",
		Language: "ja",
		Words: []PlanWord{
			{Number: 1, Word: "おんがく(音楽)", Definition: "音乐", Status: "🔄待测试"},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "m.xlsx")
	if err := GenerateExcelWithMeta(plan, path,
		&ExcelMeta{PlanDate: "2026-09-20", Language: "ja", Mode: "daily"}); err != nil {
		t.Fatal(err)
	}

	fill := func(v string) {
		f, err := excelize.OpenFile(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := f.SetCellValue(exerciseSheetName, "C3", v); err != nil {
			t.Fatal(err)
		}
		if err := f.Save(); err != nil {
			t.Fatal(err)
		}
	}

	// 只写汉字：假名档判错，汉字档判对，任一档判对
	fill("音楽")
	p, _ := ParseExerciseFile(path, GradeKana)
	if len(p.Words) != 1 || p.Words[0].AutoCorrect {
		t.Errorf("假名档下只写汉字应判错：%+v", p.Words)
	}
	p, _ = ParseExerciseFile(path, GradeKanji)
	if len(p.Words) != 1 || !p.Words[0].AutoCorrect {
		t.Errorf("汉字档下只写汉字应判对：%+v", p.Words)
	}
	p, _ = ParseExerciseFile(path, GradeEither)
	if len(p.Words) != 1 || !p.Words[0].AutoCorrect {
		t.Errorf("任一档下只写汉字应判对：%+v", p.Words)
	}

	// 写假名：任一档判对；grade_mode 字段透传
	fill("おんがく")
	p, _ = ParseExerciseFile(path, GradeEither)
	if len(p.Words) != 1 || !p.Words[0].AutoCorrect {
		t.Errorf("任一档下写假名应判对：%+v", p.Words)
	}
	if p.GradeMode != string(GradeEither) {
		t.Errorf("grade_mode 应为 either，实为 %q", p.GradeMode)
	}

	// 写全形：完整档判对
	fill("おんがく(音楽)")
	p, _ = ParseExerciseFile(path, GradeFull)
	if len(p.Words) != 1 || !p.Words[0].AutoCorrect {
		t.Errorf("完整档下写全形应判对：%+v", p.Words)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func atoi(s string) (int, error) {
	s2 := s
	if s2 == "" {
		return 0, os.ErrInvalid
	}
	for _, c := range s2 {
		if c < '0' || c > '9' {
			return 0, os.ErrInvalid
		}
	}
	n := 0
	for _, c := range s2 {
		n = n*10 + int(c-'0')
	}
	return n, nil
}
