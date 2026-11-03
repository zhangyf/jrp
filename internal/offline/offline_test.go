package offline

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"
	"github.com/zhangyf/jrp/internal/grading"
)

// 本文件只测解析侧：造临时 xlsx → ParseExerciseFile → 比对 ImportPreview。
// 不碰 COS、不依赖当前时间、不 sleep。渲染侧（根包的 Generate*）不在本包依赖里。

// ---------- 夹具 ----------

// sheetFixture 一个 sheet 的夹具：名字 + 从 A1 开始逐行铺开的单元格。
//
// 两个 excelize 的坑，夹具必须绕开，否则测的就不是被测函数而是 IO 细节：
//  1. 行尾空单元格会被裁掉，所以「左右栏之间的空列」（练习版 D 列）必须写
//     占位值，否则右栏 E/F/G 会被当成 C/D/E 左移，整行错位；
//  2. 空字符串是否落盘不确定，所以「空行」一律用空白字符串写，读回来无论
//     裁不裁都是被 col 判空，结论一致。
type sheetFixture struct {
	name string
	rows [][]string
}

// metaSheet _meta 夹具。kind 故意写上：ParseExerciseFile 的 switch 里没有
// kind 分支，未知键必须被忽略而不是写进 ImportPreview。
var metaSheet = sheetFixture{name: MetaSheetName, rows: [][]string{
	{"plan_date", "2026-03-05"},
	{"language", "ja"},
	{"mode", "daily"},
	{"kind", "hard"},
}}

// writeBook 在 dir 里造一个临时 xlsx 并返回路径。
// excelize.NewFile() 自带的 Sheet1 保留着，正好当「多余 sheet」。
func writeBook(t *testing.T, dir, filename string, sheets []sheetFixture) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	for _, s := range sheets {
		if _, err := f.NewSheet(s.name); err != nil {
			t.Fatalf("新建 sheet %q 失败：%v", s.name, err)
		}
		for ri, row := range s.rows {
			for ci, v := range row {
				axis, err := excelize.CoordinatesToCellName(ci+1, ri+1)
				if err != nil {
					t.Fatalf("坐标转换失败：%v", err)
				}
				if err := f.SetCellValue(s.name, axis, v); err != nil {
					t.Fatalf("写 %s!%s 失败：%v", s.name, axis, err)
				}
			}
		}
	}

	path := filepath.Join(dir, filename)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("保存 %s 失败：%v", path, err)
	}
	return path
}

// ---------- ParseExerciseFile：结构解析 ----------

func TestParseExerciseFile(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		sheets   []sheetFixture
		mode     grading.GradeMode
		want     *ImportPreview
		wantErr  bool
	}{
		{
			// 一页里塞满各种行型，验证「哪些行被吃、哪些行留下」。
			name:     "正常单词区与句子区",
			filename: "book.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{
					name: ExerciseSheetName,
					rows: [][]string{
						// 状态 emoji 开头的分区标题行 → 整行跳过，
						// 连右栏（E/F/G）一起丢，9 号因此拿不到手写内容
						{"\U0001f534待巩固", "音乐", "占位", "-", "9", "橘子", "みかん"},
						{"1", "音乐", "おんがく(音楽)", "-", "3", "图书馆", "としょかん"},
						{"2", "咖喱", "かれー", "-", "4", "手套", "てぶくろ"},
						{"  ", "", "", "", "", "", ""}, // 空行
						{"---", "", ""},                // 分隔行：atoi 失败
						{"S2", "这里是便利店。", "ここはコンビニです"},
						{"S10", "小李每天喝咖啡。", "李さんは毎日コーヒーを飲みます"},
						{"S1", "我吃面包。", "私はパンを食べます"},
					},
				},
				{
					name: AnswerSheetName,
					rows: [][]string{
						{"1", "音乐", "おんがく(音楽)", "3", "图书馆", "としょかん(図書館)"},
						{"2", "咖喱", "カレー", "4", "手套", "てぶくろ(手袋)"},
						{"5", "苹果", "りんご(林檎)"},
						{"9", "橘子", "みかん(蜜柑)"},
						{"S1", "（日语）", "（手写）", "私はパンを食べます。"},
						{"S2", "（日语）", "（手写）", "ここはコンビニです。"},
						{"S10", "（日语）", "（手写）", "李さんは毎日コーヒーを飲みます。"},
					},
				},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Words: []ImportWord{
					{1, "音乐", "おんがく(音楽)", "おんがく(音楽)", true},
					{2, "咖喱", "カレー", "かれー", true},
					{3, "图书馆", "としょかん(図書館)", "としょかん", true},
					{4, "手套", "てぶくろ(手袋)", "てぶくろ", true},
					// 练习版没写 5 → 占位中文；答案版的「苹果」不作数，
					// 中文提示只从练习版取
					{5, "(无中文提示)", "りんご(林檎)", "", false},
					// 9 号的手写内容随标题行一起被丢掉了
					{9, "(无中文提示)", "みかん(蜜柑)", "", false},
				},
				Sentences: []ImportSentence{
					// 练习版里是 S2/S10/S1 乱序，输出按答案版序号数值升序
					{1, "我吃面包。", "私はパンを食べます。", "私はパンを食べます", true, "low"},
					{2, "这里是便利店。", "ここはコンビニです。", "ここはコンビニです", true, "low"},
					{10, "小李每天喝咖啡。", "李さんは毎日コーヒーを飲みます。", "李さんは毎日コーヒーを飲みます", true, "low"},
				},
				SkippedBlank: []int{5, 9},
			},
		},
		{
			// 只有练习版缺失才报错：老师填的东西没了，比答案缺失严重得多。
			name:     "缺练习版 sheet 报错",
			filename: "no_exercise.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: AnswerSheetName, rows: [][]string{{"1", "音乐", "おんがく(音楽)"}}},
			},
			wantErr: true,
		},
		{
			// 反过来缺答案版**不报错**：判分没有基准，静默返回空结果。
			name:     "缺答案版 sheet 不报错但结果为空",
			filename: "no_answer.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{{"1", "音乐", "おんがく"}}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
			},
		},
		{
			// 老版本导出没有 _meta → 从文件名兜底抠日期。
			name:     "缺 meta 时从文件名兜底日期",
			filename: "review_2026-03-05_v1.0.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				{name: ExerciseSheetName, rows: [][]string{{"1", "音乐", "おんがく"}}},
				{name: AnswerSheetName, rows: [][]string{{"1", "音乐", "おんがく(音楽)"}}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				GradeMode: "kana",
				Words: []ImportWord{
					{1, "音乐", "おんがく(音楽)", "おんがく", true},
				},
			},
		},
		{
			// _meta 存在但没有 plan_date → **不**回退文件名，日期就是空。
			// 兜底只在整张 _meta 缺失时发生。
			name:     "meta 存在但无日期时不兜底",
			filename: "review_2026-03-05_v2.0.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				{name: MetaSheetName, rows: [][]string{{"language", "ja"}, {"mode", "hard"}}},
				{name: ExerciseSheetName, rows: [][]string{{"1", "音乐", "おんがく"}}},
				{name: AnswerSheetName, rows: [][]string{{"1", "音乐", "おんがく(音楽)"}}},
			},
			want: &ImportPreview{
				Language:  "ja",
				Mode:      "hard",
				GradeMode: "kana",
				Words: []ImportWord{
					{1, "音乐", "おんがく(音楽)", "おんがく", true},
				},
			},
		},
		{
			name:     "空表",
			filename: "empty.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName},
				{name: AnswerSheetName},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
			},
		},
		{
			// 答案列为空：写了也判错，而且因为「写了」不进 SkippedBlank。
			name:     "答案列为空时不进 SkippedBlank",
			filename: "blank_answer.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{{"1", "音乐", "おんがく"}}},
				{name: AnswerSheetName, rows: [][]string{{"1", "音乐", ""}}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Words: []ImportWord{
					{1, "音乐", "", "おんがく", false},
				},
			},
		},
		{
			// 右栏答案必须落在 F 列（行长度 6）。F 列没写 → 行被裁成 5 列，
			// len(r) > 5 不成立 → 2 号连 SkippedBlank 都进不去，整条消失。
			name:     "右栏答案缺 F 列时整条消失",
			filename: "short_row.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{
					{"1", "音乐", "おんがく", "-", "2", "苹果", ""},
				}},
				{name: AnswerSheetName, rows: [][]string{
					{"1", "音乐", "おんがく(音楽)", "2", "苹果"},
				}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Words: []ImportWord{
					{1, "音乐", "おんがく(音楽)", "おんがく", true},
				},
			},
		},
		{
			// 非法单元格：atoi 失败的整格跳过；负数 atoi 是成功的，
			// 于是「-2」被当成合法序号，还排在 1 前面。
			name:     "非法单元格与负数序号",
			filename: "junk.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{
					{"abc", "非法1", "x", "-", "xyz", "非法2", "y"},
					{"3.5", "小数", "x"},
					{"1e2", "科学计数", "x"},
					{"-2", "笨蛋", "ばか", "-", "", "", ""},
				}},
				{name: AnswerSheetName, rows: [][]string{
					{"-2", "笨蛋", "ばか(馬鹿)"},
					{"1", "一", "いち"},
				}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Words: []ImportWord{
					{-2, "笨蛋", "ばか(馬鹿)", "ばか", true},
					{1, "(无中文提示)", "いち", "", false},
				},
				SkippedBlank: []int{1},
			},
		},
		{
			// 多余的 sheet（含默认的 Sheet1）一律忽略，只看三个已知 sheet。
			name:     "多余 sheet 被忽略",
			filename: "extra.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{{"1", "音乐", "おんがく"}}},
				{name: AnswerSheetName, rows: [][]string{{"1", "音乐", "おんがく(音楽)"}}},
				{name: "备注", rows: [][]string{
					{"随便写", "2026-03-05", "おんがく"},
					{"1", "不该被解析", "x"},
				}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Words: []ImportWord{
					{1, "音乐", "おんがく(音楽)", "おんがく", true},
				},
			},
		},
		{
			// 中文 + 假名 + 汉字混写的提示列与手写列，原样透传、判分照旧。
			name:     "中文与假名混合",
			filename: "mixed.xlsx",
			mode:     grading.GradeKanji,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{
					{"1", "音乐（おんがく・音楽）", "林檎"},
				}},
				{name: AnswerSheetName, rows: [][]string{
					{"1", "苹果", "りんご(林檎)"},
				}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kanji",
				Words: []ImportWord{
					{1, "音乐（おんがく・音楽）", "りんご(林檎)", "林檎", true},
				},
			},
		},
		{
			// 答案版有句子、练习版没写 → Chinese 是空串，不像单词那样有
			// 「(无中文提示)」占位。
			name:     "句子未写时中文提示为空串",
			filename: "s_blank.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName},
				{name: AnswerSheetName, rows: [][]string{
					{"S1", "（日语）", "（手写）", "ここはコンビニです。"},
				}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Sentences: []ImportSentence{
					{1, "", "ここはコンビニです。", "", false, "low"},
				},
			},
		},
		{
			// 答案是空时，手写只剩标点也判「对」：Norm 把句号删光后两边都是
			// 空串，input != "" 只看 trim 后的原文。
			name:     "句子答案为空时输入标点判对",
			filename: "s_punct.xlsx",
			mode:     grading.GradeKana,
			sheets: []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{{"S1", "提示", "。"}}},
				{name: AnswerSheetName, rows: [][]string{{"S1", "（日语）", "（手写）", ""}}},
			},
			want: &ImportPreview{
				PlanDate:  "2026-03-05",
				Language:  "ja",
				Mode:      "daily",
				GradeMode: "kana",
				Sentences: []ImportSentence{
					{1, "提示", "", "。", true, "low"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeBook(t, t.TempDir(), tc.filename, tc.sheets)

			got, err := ParseExerciseFile(path, tc.mode)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际返回：%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("解析结果不符\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// ---------- ParseExerciseFile：四档判分 ----------

// 同一道题（答案「おんがく(音楽)」）在不同档位下的结论不同，
// 期望值全部手写，不调用 grading.GradeAnswer 反推。
func TestParseExerciseFileGradeModes(t *testing.T) {
	cases := []struct {
		name        string
		answer      string // 答案版 C 列
		input       string // 练习版 C 列（老师手写）
		mode        grading.GradeMode
		gradeMode   string // 期望透传到 ImportPreview.GradeMode 的字面值
		wantCorrect bool
		wantSkipped []int
	}{
		{"kana-写假名对", "おんがく(音楽)", "おんがく", grading.GradeKana, "kana", true, nil},
		{"kana-照抄全形对", "おんがく(音楽)", "おんがく(音楽)", grading.GradeKana, "kana", true, nil},
		{"kana-只写汉字错", "おんがく(音楽)", "音楽", grading.GradeKana, "kana", false, nil},
		{"kana-片假名词写平假名对", "カレー", "かれー", grading.GradeKana, "kana", true, nil},
		{"kana-写错", "おんがく(音楽)", "おんかく", grading.GradeKana, "kana", false, nil},
		{"kana-空白判未填", "おんがく(音楽)", "", grading.GradeKana, "kana", false, []int{1}},

		{"kanji-只写汉字对", "おんがく(音楽)", "音楽", grading.GradeKanji, "kanji", true, nil},
		{"kanji-只写假名错", "おんがく(音楽)", "おんがく", grading.GradeKanji, "kanji", false, nil},
		{"kanji-照抄全形对", "おんがく(音楽)", "おんがく(音楽)", grading.GradeKanji, "kanji", true, nil},
		{"kanji-无汉字的词退回假名档", "カレー", "かれー", grading.GradeKanji, "kanji", true, nil},

		{"full-照抄全形对", "おんがく(音楽)", "おんがく(音楽)", grading.GradeFull, "full", true, nil},
		{"full-只写假名错", "おんがく(音楽)", "おんがく", grading.GradeFull, "full", false, nil},
		{"full-只写汉字错", "おんがく(音楽)", "音楽", grading.GradeFull, "full", false, nil},

		{"either-写假名对", "おんがく(音楽)", "おんがく", grading.GradeEither, "either", true, nil},
		{"either-写汉字对", "おんがく(音楽)", "音楽", grading.GradeEither, "either", true, nil},
		{"either-照抄全形对", "おんがく(音楽)", "おんがく(音楽)", grading.GradeEither, "either", true, nil},
		{"either-写错", "おんがく(音楽)", "おんがく音", grading.GradeEither, "either", false, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeBook(t, t.TempDir(), "grade.xlsx", []sheetFixture{
				metaSheet,
				{name: ExerciseSheetName, rows: [][]string{{"1", "音乐", tc.input}}},
				{name: AnswerSheetName, rows: [][]string{{"1", "音乐", tc.answer}}},
			})

			got, err := ParseExerciseFile(path, tc.mode)
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			want := &ImportPreview{
				PlanDate:     "2026-03-05",
				Language:     "ja",
				Mode:         "daily",
				GradeMode:    tc.gradeMode,
				Words:        []ImportWord{{1, "音乐", tc.answer, tc.input, tc.wantCorrect}},
				SkippedBlank: tc.wantSkipped,
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("解析结果不符\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

// ---------- ParseExerciseFile：打不开的文件 ----------

func TestParseExerciseFileErrors(t *testing.T) {
	dir := t.TempDir()
	notExcel := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(notExcel, []byte("这不是 Excel"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.xlsx")
	if err := os.WriteFile(broken, []byte("PK\x03\x04broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"文件不存在", filepath.Join(dir, "missing.xlsx")},
		{"非 xlsx 扩展名", notExcel},
		{"xlsx 内容损坏", broken},
		{"路径是目录", dir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseExerciseFile(tc.path, grading.GradeKana)
			if err == nil {
				t.Fatalf("期望报错，实际返回：%+v", got)
			}
			if got != nil {
				t.Errorf("出错时预览应为 nil，got %+v", got)
			}
		})
	}
}

// ---------- col ----------

func TestCol(t *testing.T) {
	cases := []struct {
		name string
		row  []string
		idx  int
		want string
	}{
		{"正常-首列", []string{" おんがく ", "音乐"}, 0, "おんがく"},
		{"正常-中间列", []string{"1", " 音乐 ", "おんがく"}, 1, "音乐"},
		{"正常-末列", []string{"1", "音乐", " おんがく "}, 2, "おんがく"},
		{"正常-值为空串", []string{"", "音乐"}, 0, ""},
		{"越界-刚好超出", []string{"1"}, 1, ""},
		{"越界-差很多", []string{"1", "音乐"}, 7, ""},
		{"越界-nil 行", nil, 0, ""},
		{"空行-空切片", []string{}, 0, ""},
		{"空行-全为空白", []string{"   ", "\t", ""}, 0, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := col(tc.row, tc.idx); got != tc.want {
				t.Errorf("col(%v, %d) = %q, want %q", tc.row, tc.idx, got, tc.want)
			}
		})
	}
}

// ---------- sortedKeys ----------

func TestSortedKeys(t *testing.T) {
	cases := []struct {
		name string
		in   map[int]string
		want []int
	}{
		{"空 map", map[int]string{}, []int{}},
		{"单键", map[int]string{7: "x"}, []int{7}},
		{"乱序键升序", map[int]string{3: "c", 1: "a", 2: "b"}, []int{1, 2, 3}},
		{"已升序", map[int]string{1: "a", 2: "b", 3: "c"}, []int{1, 2, 3}},
		{"逆序", map[int]string{10: "a", 2: "b", 1: "c"}, []int{1, 2, 10}},
		{"负键", map[int]string{-1: "a", 5: "b", 0: "c", -9: "d"}, []int{-9, -1, 0, 5}},
		{"含零键", map[int]string{0: "z", -3: "n", 4: "p"}, []int{-3, 0, 4}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sortedKeys(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sortedKeys(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	// 泛型：换成别的 value 类型，排序结果必须一致
	if got := sortedKeys(map[int]int{2: 0, -1: 0, 0: 0}); !reflect.DeepEqual(got, []int{-1, 0, 2}) {
		t.Errorf("sortedKeys(map[int]int) = %v, want [-1 0 2]", got)
	}
}

// ---------- firstNonEmpty ----------

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want string
	}{
		{"首选非空", "おんがく", "音楽", "おんがく"},
		{"首为空取备选", "", "音楽", "音楽"},
		{"首为空白取备选", "   ", "音楽", "音楽"},
		{"两者都空", "", "", ""},
		{"原样返回不 trim", " おんがく ", "音楽", " おんがく "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstNonEmpty(tc.a, tc.b); got != tc.want {
				t.Errorf("firstNonEmpty(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ---------- dateFromFilename ----------

func TestDateFromFilename(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"路径含日期", "C:/tmp/review_2026-03-05_v1.0.xlsx", "2026-03-05"},
		{"正斜杠分隔符", "C:/Users/x/2026-03-05/review.xlsx", "2026-03-05"},
		{"反斜杠分隔符", `C:\Users\x\review_2026-03-05.xlsx`, "2026-03-05"},
		{"多个日期取最左一个", "a_2026-01-01_b_2026-03-05.xlsx", "2026-01-01"},
		// 正则不校验日期合法性，形态对就算数
		{"形态合法但日期不存在", "x_2026-99-99.xlsx", "2026-99-99"},
		// 扫的是整条路径，目录名里的日期也算（虽然函数名叫 FromFilename）
		{"目录名里的日期", `C:\2026-03-05\review.xlsx`, "2026-03-05"},
		{"无日期", "review.xlsx", ""},
		{"位数不足", "x_2026-3-05.xlsx", ""},
		{"空路径", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dateFromFilename(tc.path); got != tc.want {
				t.Errorf("dateFromFilename(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// ---------- 三个正则 ----------

func TestSentenceNumRe(t *testing.T) {
	cases := []struct {
		in       string
		wantHit  bool
		wantNum  string
		wantFull string
	}{
		{"S1", true, "1", "S1"},
		{"S10", true, "10", "S10"},
		{"S0", true, "0", "S0"},
		{"S007", true, "007", "S007"},
		{"s1", false, "", ""},   // 小写不认
		{"S1 ", false, "", ""},  // 尾空格，$ 锚定不认
		{" S1", false, "", ""},  // 前导空格
		{"S", false, "", ""},    // 没有数字
		{"S1a", false, "", ""},  // 尾部多余字符
		{"SS1", false, "", ""},  // 两个 S
		{"1", false, "", ""},    // 单词序号不是句子
		{"", false, "", ""},     // 空串
		{"S-1", false, "", ""},  // 负数不认
		{"S1.5", false, "", ""}, // 非整数不认
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			m := sentenceNumRe.FindStringSubmatch(tc.in)
			if tc.wantHit {
				if m == nil {
					t.Fatalf("sentenceNumRe 应命中 %q", tc.in)
				}
				if len(m) != 2 || m[0] != tc.wantFull || m[1] != tc.wantNum {
					t.Errorf("sentenceNumRe(%q) = %v, want [%s %s]", tc.in, m, tc.wantFull, tc.wantNum)
				}
				return
			}
			if m != nil {
				t.Errorf("sentenceNumRe 不该命中 %q，got %v", tc.in, m)
			}
		})
	}
}

func TestSectionTitleRe(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"钉子户", "\u2620\ufe0f钉子户", true},
		{"待巩固", "\U0001f534待巩固", true},
		{"待测试", "\U0001f504待测试", true},
		{"复习中", "\U0001f7e1复习中", true},
		{"已掌握", "\U0001f7e2已掌握", true},
		{"重点", "\U0001f525重点", true},
		{"注意", "\u26a0注意", true},
		{"提示", "\U0001f4a1提示", true},
		{"只有状态符", "\U0001f534", true},
		{"单词序号", "1", false},
		{"句子序号", "S1", false},
		{"空串", "", false},
		{"状态符不在开头", "待巩固\U0001f534", false},
		// 练习版 sheet 的标题以铅笔开头，不在状态符集合里，靠 atoi 失败兜底跳过
		{"铅笔标题", "\u270f\ufe0f练习版 2026-03-05", false},
		{"普通中文", "单词区", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionTitleRe.MatchString(tc.in); got != tc.want {
				t.Errorf("sectionTitleRe.MatchString(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFilenameDateRe(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"文件名带日期", "review_2026-03-05_v1.0.xlsx", "2026-03-05"},
		{"纯日期", "2026-03-05", "2026-03-05"},
		{"多个日期取最左", "a_2026-01-01_b_2026-03-05.xlsx", "2026-01-01"},
		{"不校验月份", "x_2026-99-99.xlsx", "2026-99-99"},
		{"多一位数字时从中间截", "12345-2026-03-05.xlsx", "2026-03-05"},
		{"无日期", "review.xlsx", ""},
		{"位数不足", "2026-3-5", ""},
		{"只有年月", "2026-03", ""},
		{"空串", "", ""},
		{"字母和数字混杂", "abcdef", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filenameDateRe.FindString(tc.in); got != tc.want {
				t.Errorf("filenameDateRe.FindString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---------- sheet 名常量 ----------

// 三个 sheet 名是离线文件的契约：改了就打不开历史文件，字面值必须钉死。
func TestSheetNameConstants(t *testing.T) {
	if MetaSheetName != "_meta" {
		t.Errorf("MetaSheetName = %q, want _meta", MetaSheetName)
	}
	if ExerciseSheetName != "\u270f\ufe0f练习版" {
		t.Errorf("ExerciseSheetName = %q, want ✏️练习版", ExerciseSheetName)
	}
	if AnswerSheetName != "\u2705答案版" {
		t.Errorf("AnswerSheetName = %q, want ✅答案版", AnswerSheetName)
	}
}
