package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// 离线 Excel 闭环：导出时把 plan_date / language / mode 藏进 _meta sheet，
// 回填时读回来定位这次是哪天的练习。
//
// 为什么用隐藏 sheet 而不是文件名或文档属性：
//   - 文件名：微信/邮件转发时经常被改名，plan_date 就没了
//   - 文档属性：WPS 会剥离自定义属性，往返一次就丢
//   - 隐藏 sheet：跟着文件走，excelize 稳定可读，且对现有两个 sheet 的布局零干扰

// metaSheetName 隐藏 sheet 的名字。下划线开头，排在最后，不干扰正常使用。
const metaSheetName = "_meta"

// exerciseSheetName / answerSheetName 两个可见 sheet 的名字。
const (
	exerciseSheetName = "✏️练习版"
	answerSheetName   = "✅答案版"
)

// ExcelMeta 离线包自带的机器可读元信息。
type ExcelMeta struct {
	PlanDate string `json:"plan_date"`
	Language string `json:"language"`
	Mode     string `json:"mode"` // daily | hard | sentences
	Kind     string `json:"kind"` // "" | hard | sentences
}

// writeMetaSheet 写入并隐藏 _meta sheet。
func (w *excelWriter) writeMetaSheet(meta *ExcelMeta) error {
	if _, err := w.f.NewSheet(metaSheetName); err != nil {
		return err
	}
	rows := [][2]string{
		{"plan_date", meta.PlanDate},
		{"language", meta.Language},
		{"mode", meta.Mode},
		{"kind", meta.Kind},
	}
	for i, kv := range rows {
		cell := fmt.Sprintf("A%d", i+1)
		if err := w.f.SetCellValue(metaSheetName, cell, kv[0]); err != nil {
			return err
		}
		if err := w.f.SetCellValue(metaSheetName, fmt.Sprintf("B%d", i+1), kv[1]); err != nil {
			return err
		}
	}
	return w.f.SetSheetVisible(metaSheetName, false)
}

// ---------- 回填解析 ----------

// ImportWord 一道单词题的回填预览。
type ImportWord struct {
	Number      int    `json:"number"`
	Definition  string `json:"definition"`
	Answer      string `json:"answer"`       // 正确答案（来自答案版）
	Input       string `json:"input"`        // 老师手写的（来自练习版）
	AutoCorrect bool   `json:"auto_correct"` // 按当前档位自动判分的结果
}

// ImportSentence 一道造句题的回填预览。
type ImportSentence struct {
	Number      int    `json:"number"`
	Chinese     string `json:"chinese"`
	Answer      string `json:"answer"`
	Input       string `json:"input"`
	AutoCorrect bool   `json:"auto_correct"`
	Confidence  string `json:"confidence"` // 长句手写自动判分不可靠，恒为 low
}

// ImportPreview 上传已填 Excel 后的解析结果。**不落库**，交给前端展示、
// 老师逐题确认后再提交。
type ImportPreview struct {
	PlanDate     string           `json:"plan_date"`
	Language     string           `json:"language"`
	Mode         string           `json:"mode"`
	Words        []ImportWord     `json:"words"`
	Sentences    []ImportSentence `json:"sentences"`
	SkippedBlank []int            `json:"skipped_blank"` // 没写的单词序号
	GradeMode    string           `json:"grade_mode"`
}

// 分区标题行以状态 emoji 开头
var sectionTitleRe = regexp.MustCompile(`^[☠🔴🔄🟡🟢🔥⚠💡]`)

// 造句行形如 S1 / S12
var sentenceNumRe = regexp.MustCompile(`^S(\d+)$`)

// ParseExerciseFile 解析老师填好的练习版 Excel，返回预览。
//
// 判分一律在 Go 里重做，**绝不读 D/H 列** —— 那是 WPS 私有的
// _wpsfn.REGEXP 公式，只有 WPS 能算，离线文件里是空值。
//
// 空白单元格 = 老师没写，按「未出现的词完全不处理」的铁律不进结果，
// 只在 SkippedBlank 里列出序号让前端提示。
func ParseExerciseFile(path string, mode GradeMode) (*ImportPreview, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打不开 Excel：%w", err)
	}
	defer f.Close()

	out := &ImportPreview{GradeMode: string(mode)}

	// --- meta ---
	if rows, err := f.GetRows(metaSheetName); err == nil {
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			switch strings.TrimSpace(r[0]) {
			case "plan_date":
				out.PlanDate = strings.TrimSpace(r[1])
			case "language":
				out.Language = strings.TrimSpace(r[1])
			case "mode":
				out.Mode = strings.TrimSpace(r[1])
			}
		}
	} else {
		// 老版本导出没有 _meta，退回到文件名里猜日期
		out.PlanDate = dateFromFilename(path)
	}

	// --- 练习版：老师手写的内容 ---
	// 左栏 A=序号 B=中文 C=手写；右栏 E=序号 F=中文 G=手写
	type entry struct {
		def   string
		input string
	}
	exWords := map[int]*entry{}
	exSentences := map[int]*entry{}

	rows, err := f.GetRows(exerciseSheetName)
	if err != nil {
		return nil, fmt.Errorf("找不到「%s」sheet：%w", exerciseSheetName, err)
	}
	for _, r := range rows {
		a := col(r, 0)
		if a == "" || sectionTitleRe.MatchString(a) {
			continue
		}
		if m := sentenceNumRe.FindStringSubmatch(a); m != nil {
			n, _ := strconv.Atoi(m[1])
			exSentences[n] = &entry{def: col(r, 1), input: col(r, 2)}
			continue
		}
		// 左栏（A=序号 B=中文 C=手写）
		if n, err := strconv.Atoi(a); err == nil {
			exWords[n] = &entry{def: col(r, 1), input: col(r, 2)}
		}
		// 右栏和左栏在**同一行**（E=序号 F=中文 G=手写），不能 continue 掉
		if len(r) > 4 {
			if n, err := strconv.Atoi(strings.TrimSpace(r[4])); err == nil {
				exWords[n] = &entry{def: col(r, 5), input: col(r, 6)}
			}
		}
	}

	// --- 答案版：正确答案 ---
	// 左栏 A=序号 B=中文 C=答案；右栏 D=序号 E=中文 F=答案；句子 A=S{n} D=答案
	ansWords := map[int]string{}
	ansSentences := map[int]string{}
	if arows, err := f.GetRows(answerSheetName); err == nil {
		for _, r := range arows {
			a := col(r, 0)
			if a == "" || sectionTitleRe.MatchString(a) {
				continue
			}
			if m := sentenceNumRe.FindStringSubmatch(a); m != nil {
				n, _ := strconv.Atoi(m[1])
				ansSentences[n] = col(r, 3) // D 列
				continue
			}
			// 左栏：A=序号，C=答案
			if n, err := strconv.Atoi(a); err == nil {
				ansWords[n] = col(r, 2)
			}
			// 右栏同样在这一行：D=序号，F=答案
			if len(r) > 5 {
				if n, err := strconv.Atoi(strings.TrimSpace(r[3])); err == nil {
					ansWords[n] = col(r, 5)
				}
			}
		}
	}

	// --- 合并并按序号排序 ---
	nums := sortedKeys(ansWords)
	for _, n := range nums {
		e, ok := exWords[n]
		if !ok {
			e = &entry{}
		}
		if strings.TrimSpace(e.input) == "" {
			out.SkippedBlank = append(out.SkippedBlank, n)
		}
		def := e.def
		if def == "" {
			def = "(无中文提示)"
		}
		out.Words = append(out.Words, ImportWord{
			Number:      n,
			Definition:  def,
			Answer:      ansWords[n],
			Input:       e.input,
			AutoCorrect: GradeAnswer(e.input, ansWords[n], mode),
		})
	}

	snums := sortedKeys(ansSentences)
	for _, n := range snums {
		e, ok := exSentences[n]
		if !ok {
			e = &entry{}
		}
		input := strings.TrimSpace(e.input)
		out.Sentences = append(out.Sentences, ImportSentence{
			Number:      n,
			Chinese:     e.def,
			Answer:      ansSentences[n],
			Input:       input,
			AutoCorrect: input != "" && normSentence(input) == normSentence(ansSentences[n]),
			Confidence:  "low", // 长句手写，自动判分仅供参考，必须人工确认
		})
	}

	return out, nil
}

// col 安全取第 i 列（越界返回空串）。
func col(r []string, i int) string {
	if i < len(r) {
		return strings.TrimSpace(r[i])
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func sortedKeys[T any](m map[int]T) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

var filenameDateRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)

// dateFromFilename 老版本导出没有 _meta 时的兜底：从文件名里抠日期。
func dateFromFilename(path string) string {
	m := filenameDateRe.FindString(path)
	if m != "" {
		return m
	}
	return ""
}
