package offline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"github.com/zhangyf/jrp/internal/sentence"
)

func TestCleanCompactPrompt(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"花（时间、钱等）（动词，spend time/money on sth.）", "花（时间、钱等）"},
		{"在储物柜对面（be across from...）", "在储物柜对面"},
		{"大量；许多（可数/不可数名词均可）", "大量；许多（可数/不可数名词均可）"},
		{"一起；共同", "一起；共同"},
	}
	for _, c := range cases {
		if got := cleanCompactPrompt(c.in); got != c.want {
			t.Errorf("cleanCompactPrompt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func compactFixture() *ReviewPlan {
	return &ReviewPlan{
		Date:     "2026-11-19",
		Language: "en",
		Words: []PlanWord{
			{Number: 1, Word: "funny", Definition: "滑稽的；好笑的", Group: "Unit 2 单词（测试卷）"},
			{Number: 2, Word: "spend", Definition: "花（时间、钱等）（动词，spend time/money on sth.）", Group: "Unit 2 单词（测试卷）"},
			{Number: 3, Word: "have fun", Definition: "玩得高兴（have fun）", Group: "Unit 2 词组（测试卷）"},
		},
		Sentences: []sentence.PlanSentence{
			{Number: 1, Chinese: "早上好。", Answer: "Good morning."},
		},
	}
}

func TestGenerateCompactExcel(t *testing.T) {
	out := filepath.Join(t.TempDir(), "compact.xlsx")
	if err := GenerateCompactExcel(compactFixture(), out); err != nil {
		t.Fatalf("GenerateCompactExcel: %v", err)
	}
	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer f.Close()

	for _, name := range []string{"✏️练习版", "✅答案版"} {
		if _, err := f.GetSheetIndex(name); err != nil {
			t.Errorf("missing sheet %q: %v", name, err)
		}
	}

	// 练习版英文列留空，答案版填答案（C5 = 单词区第一行）
	ex, _ := f.GetCellValue("✏️练习版", "C5")
	if ex != "" {
		t.Errorf("exercise C5 should be blank, got %q", ex)
	}
	ans, _ := f.GetCellValue("✅答案版", "C5")
	if ans != "funny" {
		t.Errorf("answer C5 = %q, want %q", ans, "funny")
	}

	// 中文提示：含英文的括注被删掉，纯中文括注保留
	cn, _ := f.GetCellValue("✏️练习版", "B6")
	if strings.Contains(cn, "spend") || !strings.Contains(cn, "花（时间、钱等）") {
		t.Errorf("exercise B6 not cleaned properly: %q", cn)
	}
	cnFull, _ := f.GetCellValue("✅答案版", "B6")
	if !strings.Contains(cnFull, "spend time/money") {
		t.Errorf("answer B6 should keep full definition: %q", cnFull)
	}

	// 老师明确不要姓名 / 得分 / 家长签字 / 填写栏
	for _, name := range []string{"✏️练习版", "✅答案版"} {
		rows, _ := f.GetRows(name)
		for _, r := range rows {
			for _, cell := range r {
				for _, banned := range []string{"家长签字", "得分", "姓名", "日期："} {
					if strings.Contains(cell, banned) {
						t.Errorf("sheet %q contains unwanted field %q: %q", name, banned, cell)
					}
				}
			}
		}
	}

	// 表头按语言，不写死「日语」
	hdr, _ := f.GetCellValue("✏️练习版", "C4")
	if !strings.Contains(hdr, "英语") || strings.Contains(hdr, "日语") {
		t.Errorf("header C4 = %q, want English", hdr)
	}
}
