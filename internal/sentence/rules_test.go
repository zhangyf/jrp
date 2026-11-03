package sentence

// 本文件只补 sentence_test.go 没覆盖到的缺口，不重复已有断言。
//
// sentence_test.go 已覆盖（本文件跳过）：
//   - BuildSentencePlan：跨课摊平 / 近 7 天去重 / 错句强制插入 / 变形句配额 /
//     句池耗尽放宽复用 / 提交后刷新
//   - ApplySentenceState 主流程（顺带覆盖了 nextSentenceDue 的 count=2 → +7、
//     count>=3 → +14、新错句 count=1 → +3，以及 chineseFromResults 命中）
//   - pruneSentenceHistory：30 天外清理、26 天内保留、非法格式键清理
//   - Norm：全角空格、全角数字、全角标点折半、句号有无等价
//   - SortLessons：10 < 2 的字符串陷阱、无数字课名排最后
//   - sentenceRotation：同一天稳定、13 天起点轮换、后续轮次不加权
//   - firstRoundOrder：最新两课排两遍、单元末不加权
//
// 本文件补的缺口：
//  1. Norm 与 foldWidth：斜杠 / 逗号 / 句号 / 连续空白 / 空串 / 纯假名 / 汉字假名
//     混合 / 日英混合 / 宽度折叠的区间边界
//  2. LoadSentenceHistory / LoadSentenceWrong 的容错解析全分支
//  3. SortLessons 的数字提取边界、recentLessons 的 n 边界、固定日期的轮换与
//     首轮顺序精确值
//  4. nextSentenceDue：count<=1 的 +3、跨月跨年、非法 planDate 的 time.Now 兜底
//  5. pruneSentenceHistory：边界正好等于 days、days=0、空 map、nil map、
//     不存在的键、非法 today 整个不动
//  6. mergeUnique：去重、保序、空 src、空 dst、全部重复
//  7. chineseFromResults：命中、未命中、空 results、多条取第一条命中

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// eqStrings 逐元素比较；nil 与空切片视为相等（需要区分时另写断言）。
func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- 1) Norm 与 foldWidth ----------

// TestNormExtra 补 TestNormSentence 没覆盖的归一化分支。
func TestNormExtra(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空串", "", ""},
		{"只有全角空格", "　　", ""},
		{"全角句号。", "はい。", "はい"},
		{"全角句点．折叠后进删除集", "はい．", "はい"},
		{"半角句点", "はい.", "はい"},
		{"半角逗号", "はい,そうです", "はいそうです"},
		{"全角逗号，折叠后进删除集", "はい，そうです", "はいそうです"},
		{"读点、", "はい、そうです", "はいそうです"},
		{"半角斜杠", "甲/乙", "甲乙"},
		{"全角斜杠／折叠后进删除集", "甲／乙", "甲乙"},
		{"成对句混用斜杠等价", "甲／乙です。", "甲乙です"},
		{"全角感叹问号冒号分号", "すごい！本当？時間：３時；", "すごい本当時間3時"},
		{"半角标点全集", "a!b?c:d;e,f.g/h", "abcdefgh"},
		{"连续空白混合", "　a \t　\n b　", "ab"},
		{"回车换页也算空白", "a\r\nb\x0c", "ab"},
		{"纯假名", "あいうえお", "あいうえお"},
		{"汉字假名混合", "日本語を勉強します", "日本語を勉強します"},
		{"日英混合空格也被删", "I am a studentです", "Iamastudentです"},
		{"日英混合带句点", "This is a book. これは本です。", "Thisisabookこれは本です"},
		{"全角波浪号折叠后不在删除集内", "１～３", "1~3"},
		{"半角波浪号保留", "1~3", "1~3"},
		{"句点会吃掉小数点", "3.14", "314"},
		{"全角括号折叠后不在删除集内", "（笑）", "(笑)"},
		// Go 的 \s 是 [\t\n\f\r ]，不含垂直制表符 U+000B —— 与 Python 的
		// Unicode-aware \s 不同。这是实现的真实行为，不是 bug，固定住。
		{"垂直制表符不在Go的s内故保留", "甲\v乙", "甲\v乙"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Norm(c.in); got != c.want {
				t.Errorf("Norm(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormIdempotent 归一化后再归一化应当不变（历史上有带句号/不带句号两个版本）。
func TestNormIdempotent(t *testing.T) {
	in := "　李さんは 毎日 コーヒーを 飲みます。"
	once := Norm(in)
	if once != "李さんは毎日コーヒーを飲みます" {
		t.Fatalf("Norm(%q) = %q", in, once)
	}
	if twice := Norm(once); twice != once {
		t.Errorf("Norm 不是幂等的：Norm(%q) = %q, want %q", once, twice, once)
	}
}

// TestFoldWidth 直接测私有 foldWidth 的 U+FF01～U+FF5E 折叠区间边界。
func TestFoldWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空串", "", ""},
		{"全角数字", "０１２３４５６７８９", "0123456789"},
		{"全角大写", "ＡＢＣＸＹＺ", "ABCXYZ"},
		{"全角小写", "ａｂｃｘｙｚ", "abcxyz"},
		{"区间下界FF01", "！", "!"},
		{"区间上界FF5E", "～", "~"},
		{"区间外FF00不变", "＀", "＀"},
		{"区间外FF5F不变", "｟", "｟"},
		{"全角空格不在区间内不变", "　", "　"},
		{"假名汉字不变", "あいう漢字", "あいう漢字"},
		{"半角不受影响", "ABC 123 !~", "ABC 123 !~"},
		{"全角美元", "＄", "$"},
		{"混合", "ＡＢＣあ１２／", "ABCあ12/"},
		{"全角空格与全角数字混排", "　２万円　", "　2万円　"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := foldWidth(c.in); got != c.want {
				t.Errorf("foldWidth(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// ---------- 2) LoadSentenceHistory / LoadSentenceWrong 容错 ----------

func TestLoadSentenceHistoryTolerant(t *testing.T) {
	cases := []struct {
		name        string
		data        string
		wantErr     bool
		errContains string
		want        SentenceHistory // 仅在 !wantErr 时严格比较
		wantNil     bool            // JSON null → 返回 nil map
	}{
		{
			name: "顶层就是日期对象",
			data: `{"2026-09-03":["甲です。","乙です。"]}`,
			want: SentenceHistory{"2026-09-03": {"甲です。", "乙です。"}},
		},
		{
			name: "空对象字段缺失",
			data: `{}`,
			want: SentenceHistory{},
		},
		{
			name:    "JSON为null返回nil map",
			data:    `null`,
			wantNil: true,
		},
		{
			name: "某天的值为null",
			data: `{"2026-09-03":null}`,
			want: SentenceHistory{"2026-09-03": nil},
		},
		{
			name: "dates包装",
			data: `{"dates":{"2026-09-03":["a"]}}`,
			want: SentenceHistory{"2026-09-03": {"a"}},
		},
		{
			name: "history包装",
			data: `{"history":{"2026-09-04":["b"]}}`,
			want: SentenceHistory{"2026-09-04": {"b"}},
		},
		{
			name: "records包装",
			data: `{"records":{"2026-09-05":["c"]}}`,
			want: SentenceHistory{"2026-09-05": {"c"}},
		},
		{
			name:        "空字节",
			data:        ``,
			wantErr:     true,
			errContains: "无法解析 sentence_history.json",
		},
		{
			name:        "非法JSON",
			data:        `not json at all`,
			wantErr:     true,
			errContains: "无法解析 sentence_history.json",
		},
		{
			name:        "顶层是数组类型不符",
			data:        `[["a"],["b"]]`,
			wantErr:     true,
			errContains: "无法解析 sentence_history.json",
		},
		{
			name:        "顶层是字符串类型不符",
			data:        `"abc"`,
			wantErr:     true,
			errContains: "无法解析 sentence_history.json",
		},
		{
			name:        "值不是字符串数组",
			data:        `{"2026-09-03":"不是数组"}`,
			wantErr:     true,
			errContains: "无法解析 sentence_history.json",
		},
		{
			name:        "包装键的值类型不符",
			data:        `{"dates":"x"}`,
			wantErr:     true,
			errContains: "无法解析 sentence_history.json",
		},
		{
			name:        "错误信息只截断前200字节",
			data:        strings.Repeat("x", 250),
			wantErr:     true,
			errContains: "前 200 字节",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := LoadSentenceHistory([]byte(c.data))
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (hist=%v)", got)
				}
				if c.errContains != "" && !strings.Contains(err.Error(), c.errContains) {
					t.Errorf("错误信息 %q 不含 %q", err.Error(), c.errContains)
				}
				// 失败时返回的是非 nil 的空 map，调用方可以直接用
				if got == nil {
					t.Error("解析失败时应返回非 nil 的空 SentenceHistory")
				}
				if len(got) != 0 {
					t.Errorf("解析失败时应返回空 map，got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil map", got)
				}
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestLoadSentenceWrongTolerant(t *testing.T) {
	cases := []struct {
		name         string
		data         string
		wantErr      bool
		errContains  string
		wantLen      int
		wantAns      string // 若非空，断言 Sentences[0].Answer
		wantNilSents bool   // 顶层 null → Sentences 为 nil
		wantZeroRest bool   // 字段缺失时其余字段保持零值
	}{
		{
			name:    "顶层是数组",
			data:    `[{"answer":"甲です。","chinese":"甲","status":"open","next_due":"2026-09-20","wrong_count":2}]`,
			wantLen: 1,
			wantAns: "甲です。",
		},
		{
			name:    "顶层空数组",
			data:    `[]`,
			wantLen: 0,
		},
		{
			name:    "sentences包装",
			data:    `{"sentences":[{"answer":"乙です。","status":"open"}]}`,
			wantLen: 1,
			wantAns: "乙です。",
		},
		{
			// 顶层空数组分支要 len>0，空数组会掉到 raw 包装分支，仍然成功
			name:    "sentences包装且为空数组仍走包装分支",
			data:    `{"sentences":[]}`,
			wantLen: 0,
		},
		{
			name:    "items包装",
			data:    `{"items":[{"answer":"丙です。"}]}`,
			wantLen: 1,
			wantAns: "丙です。",
		},
		{
			name:    "wrong包装",
			data:    `{"wrong":[{"answer":"丁です。"}]}`,
			wantLen: 1,
			wantAns: "丁です。",
		},
		{
			name:    "wrong_sentences包装",
			data:    `{"wrong_sentences":[{"answer":"戊です。"}]}`,
			wantLen: 1,
			wantAns: "戊です。",
		},
		{
			name:         "JSON为null走到数组分支返回nil切片",
			data:         `null`,
			wantNilSents: true,
		},
		{
			name:         "字段缺失的错句对象其余字段为零值",
			data:         `[{"answer":"己です。"}]`,
			wantLen:      1,
			wantAns:      "己です。",
			wantZeroRest: true,
		},
		{
			name:        "空对象字段缺失",
			data:        `{}`,
			wantErr:     true,
			errContains: "无法解析 sentence_wrong.json",
		},
		{
			name:        "空字节",
			data:        ``,
			wantErr:     true,
			errContains: "无法解析 sentence_wrong.json",
		},
		{
			name:        "非法JSON",
			data:        `definitely not json`,
			wantErr:     true,
			errContains: "无法解析 sentence_wrong.json",
		},
		{
			name:        "数字数组类型不符",
			data:        `[1,2]`,
			wantErr:     true,
			errContains: "无法解析 sentence_wrong.json",
		},
		{
			name:        "sentences值类型不符",
			data:        `{"sentences":"x"}`,
			wantErr:     true,
			errContains: "无法解析 sentence_wrong.json",
		},
		{
			name:        "错误信息只截断前200字节",
			data:        strings.Repeat("y", 300),
			wantErr:     true,
			errContains: "前 200 字节",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := LoadSentenceWrong([]byte(c.data))
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (wrong=%v)", got)
				}
				if c.errContains != "" && !strings.Contains(err.Error(), c.errContains) {
					t.Errorf("错误信息 %q 不含 %q", err.Error(), c.errContains)
				}
				if got == nil {
					t.Fatal("解析失败时应返回非 nil 的空 *SentenceWrong")
				}
				if len(got.Sentences) != 0 {
					t.Errorf("解析失败时应返回空结构，got %v", got.Sentences)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatal("got nil *SentenceWrong")
			}
			if c.wantNilSents {
				if got.Sentences != nil {
					t.Errorf("got.Sentences = %#v, want nil slice", got.Sentences)
				}
				return
			}
			if len(got.Sentences) != c.wantLen {
				t.Fatalf("len(Sentences) = %d, want %d (%#v)", len(got.Sentences), c.wantLen, got.Sentences)
			}
			if c.wantAns != "" && got.Sentences[0].Answer != c.wantAns {
				t.Errorf("Sentences[0].Answer = %q, want %q", got.Sentences[0].Answer, c.wantAns)
			}
			if c.wantZeroRest {
				w := got.Sentences[0]
				if w.Chinese != "" || w.Status != "" || w.NextDue != "" || w.LastSeen != "" || w.LastWrong != "" || w.WrongCount != 0 {
					t.Errorf("字段缺失时应保持零值，got %+v", w)
				}
			}
		})
	}
}

// ---------- 3) SortLessons / recentLessons / sentenceRotation / firstRoundOrder ----------

func TestSortLessonsRules(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "空切片",
			in:   []string{},
			want: []string{},
		},
		{
			name: "nil切片",
			in:   nil,
			want: nil,
		},
		{
			name: "单元素",
			in:   []string{"第1课"},
			want: []string{"第1课"},
		},
		{
			name: "数字课号与非数字课名混排",
			in:   []string{"附录", "第3课", "第1课", "单元末", "第2课"},
			want: []string{"第1课", "第2课", "第3课", "单元末", "附录"},
		},
		{
			name: "重复课号按字符串稳定排",
			in:   []string{"第3课", "第1课", "第3课", "第2课"},
			want: []string{"第1课", "第2课", "第3课", "第3课"},
		},
		{
			name: "全部无数字则按字符串排",
			in:   []string{"附录", "单元末"},
			want: []string{"单元末", "附录"},
		},
		{
			name: "前导零按数字相等再按字符串",
			in:   []string{"第10课", "第08课", "第8课", "第2课"},
			want: []string{"第2课", "第08课", "第8课", "第10课"},
		},
		{
			name: "课名中间的数字",
			in:   []string{"第2课（上）", "第1课（下）", "单元末"},
			want: []string{"第1课（下）", "第2课（上）", "单元末"},
		},
		{
			name: "多个数字取第一个",
			in:   []string{"第3课 p12", "第1课 p5"},
			want: []string{"第1课 p5", "第3课 p12"},
		},
		{
			// Go 正则的 \d 只认 ASCII 数字，全角１２取不到数字 → 排最后
			name: "全角数字不参与数字排序",
			in:   []string{"第１２課", "第2课"},
			want: []string{"第2课", "第１２課"},
		},
		{
			// strconv.Atoi 溢出 → 回退到 1<<30，与「无数字」同级排最后
			name: "数字溢出回退到最大值",
			in:   []string{"第99999999999999999999课", "第1课"},
			want: []string{"第1课", "第99999999999999999999课"},
		},
		{
			name: "两位数与三位数",
			in:   []string{"第100课", "第9课", "第20课"},
			want: []string{"第9课", "第20课", "第100课"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := append([]string(nil), c.in...)
			SortLessons(got)
			if !eqStrings(got, c.want) {
				t.Errorf("SortLessons(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestRecentLessonsRules(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		n    int
		want map[string]bool
	}{
		{
			name: "空列表",
			in:   []string{},
			n:    2,
			want: map[string]bool{},
		},
		{
			name: "n为0",
			in:   []string{"第1课", "第2课"},
			n:    0,
			want: map[string]bool{},
		},
		{
			name: "n为负数",
			in:   []string{"第1课", "第2课"},
			n:    -1,
			want: map[string]bool{},
		},
		{
			name: "n大于课数则全取",
			in:   []string{"第1课", "第2课", "第3课"},
			n:    5,
			want: map[string]bool{"第1课": true, "第2课": true, "第3课": true},
		},
		{
			name: "n为1只取课号最大",
			in:   []string{"第1课", "第3课", "第2课"},
			n:    1,
			want: map[string]bool{"第3课": true},
		},
		{
			name: "全部无数字则为空",
			in:   []string{"单元末", "附录"},
			n:    2,
			want: map[string]bool{},
		},
		{
			name: "无数字课名被跳过不占位",
			in:   []string{"单元末", "第5课", "第4课"},
			n:    2,
			want: map[string]bool{"第5课": true, "第4课": true},
		},
		{
			name: "重复课号都进集合",
			in:   []string{"第5课", "第5课（复习）", "第1课"},
			n:    2,
			want: map[string]bool{"第5课": true, "第5课（复习）": true},
		},
		{
			name: "课号相同时按输入顺序取前n个",
			in:   []string{"第7课A", "第7课B", "第7课C"},
			n:    2,
			want: map[string]bool{"第7课A": true, "第7课B": true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := recentLessons(c.in, c.n)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("recentLessons(%v, %d) = %v, want %v", c.in, c.n, got, c.want)
			}
		})
	}
}

// fixedDay 轮换期望全部手算：fnv32a("2026-03-02") 对 5 课取模得到起点，
// 再按 (start+i)%len 展开。不依赖 time.Now，也不调用被测函数生成期望。
var fixedDay = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

func TestSentenceRotationFixedDate(t *testing.T) {
	five := []string{"第1课", "第2课", "第3课", "第4课", "第5课"}
	withTrailer := []string{"第1课", "第2课", "第3课", "第4课", "单元末"}

	cases := []struct {
		name    string
		lessons []string
		want    []string
	}{
		{
			name:    "五课在2026-03-02从第五课起",
			lessons: five,
			want:    []string{"第5课", "第1课", "第2课", "第3课", "第4课"},
		},
		{
			name:    "含无数字课名同样参与轮换",
			lessons: withTrailer,
			want:    []string{"单元末", "第1课", "第2课", "第3课", "第4课"},
		},
		{
			name:    "单元素",
			lessons: []string{"第1课"},
			want:    []string{"第1课"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sentenceRotation(c.lessons, fixedDay)
			if !eqStrings(got, c.want) {
				t.Errorf("sentenceRotation(%v, 2026-03-02) = %v, want %v", c.lessons, got, c.want)
			}
		})
	}

	t.Run("空列表返回nil", func(t *testing.T) {
		if got := sentenceRotation(nil, fixedDay); got != nil {
			t.Errorf("got %v, want nil", got)
		}
		if got := sentenceRotation([]string{}, fixedDay); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("结果是原集合的一个排列", func(t *testing.T) {
		got := sentenceRotation(withTrailer, fixedDay)
		if len(got) != len(withTrailer) {
			t.Fatalf("len = %d, want %d", len(got), len(withTrailer))
		}
		seen := map[string]int{}
		for _, ls := range got {
			seen[ls]++
		}
		for _, ls := range withTrailer {
			if seen[ls] != 1 {
				t.Errorf("%s 出现 %d 次，want 1", ls, seen[ls])
			}
		}
	})

	t.Run("不修改入参", func(t *testing.T) {
		in := append([]string(nil), five...)
		sentenceRotation(in, fixedDay)
		if !eqStrings(in, five) {
			t.Errorf("入参被修改了：%v", in)
		}
	})
}

func TestFirstRoundOrderFixedDate(t *testing.T) {
	cases := []struct {
		name    string
		lessons []string
		want    []string
	}{
		{
			// 2026-03-02 的轮转是 [第5课 第1课 第2课 第3课 第4课]，
			// 最近两课（第5课、第4课）各再排一遍
			name:    "五课首轮加权",
			lessons: []string{"第1课", "第2课", "第3课", "第4课", "第5课"},
			want: []string{"第5课", "第5课", "第1课", "第2课",
				"第3课", "第4课", "第4课"},
		},
		{
			// 轮转是 [单元末 第1课 第2课 第3课 第4课]，单元末无数字不加权
			name:    "无数字课名不加权",
			lessons: []string{"第1课", "第2课", "第3课", "第4课", "单元末"},
			want: []string{"单元末", "第1课", "第2课", "第3课",
				"第3课", "第4课", "第4课"},
		},
		{
			// recentLessons 为空 → 直接返回轮转顺序
			name:    "全部无数字时退化为轮转顺序",
			lessons: []string{"单元末", "附录"},
			want:    []string{"单元末", "附录"},
		},
		{
			name:    "单元素且无数字",
			lessons: []string{"单元末"},
			want:    []string{"单元末"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstRoundOrder(c.lessons, fixedDay)
			if !eqStrings(got, c.want) {
				t.Errorf("firstRoundOrder(%v, 2026-03-02) = %v, want %v", c.lessons, got, c.want)
			}
		})
	}

	t.Run("空列表返回nil", func(t *testing.T) {
		if got := firstRoundOrder(nil, fixedDay); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// ---------- 4) nextSentenceDue ----------

func TestNextSentenceDueRules(t *testing.T) {
	cases := []struct {
		name     string
		planDate string
		count    int
		want     string
		// fallback=true 表示实现用 time.Now() 兜底，只断言日期格式
		fallback bool
	}{
		{name: "错1次加3天", planDate: "2026-09-20", count: 1, want: "2026-09-23"},
		{name: "错2次加7天", planDate: "2026-09-20", count: 2, want: "2026-09-27"},
		{name: "错3次加14天", planDate: "2026-09-20", count: 3, want: "2026-10-04"},
		{name: "错10次仍是14天", planDate: "2026-09-20", count: 10, want: "2026-10-04"},
		// count<=1 都走 +3
		{name: "count为0按1次算", planDate: "2026-09-20", count: 0, want: "2026-09-23"},
		{name: "count为负按1次算", planDate: "2026-09-20", count: -5, want: "2026-09-23"},
		{name: "跨月", planDate: "2026-04-30", count: 1, want: "2026-05-03"},
		{name: "跨月加14天", planDate: "2026-09-28", count: 3, want: "2026-10-12"},
		{name: "跨年加14天", planDate: "2026-12-25", count: 3, want: "2027-01-08"},
		{name: "跨年加7天", planDate: "2026-12-30", count: 2, want: "2027-01-06"},
		{name: "闰年2月加14天", planDate: "2028-02-20", count: 3, want: "2028-03-05"},
		{name: "平年2月加14天", planDate: "2027-02-20", count: 3, want: "2027-03-06"},
		// 实现里 planDate 解析失败就用 time.Now() 兜底，只断言输出格式
		{name: "空日期兜底到今天", planDate: "", count: 1, fallback: true},
		{name: "非法日期兜底到今天", planDate: "not-a-date", count: 3, fallback: true},
		{name: "日期字段越界兜底到今天", planDate: "2026-13-45", count: 2, fallback: true},
		{name: "斜杠分隔的日期兜底到今天", planDate: "2026/09/20", count: 1, fallback: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nextSentenceDue(c.planDate, c.count)
			if c.fallback {
				// 兜底分支依赖真实当前时间，只能断言格式，不比较具体日期
				if len(got) != len("2006-01-02") {
					t.Fatalf("got %q, want YYYY-MM-DD 格式", got)
				}
				if _, err := time.Parse("2006-01-02", got); err != nil {
					t.Errorf("got %q 不是合法日期：%v", got, err)
				}
				return
			}
			if got != c.want {
				t.Errorf("nextSentenceDue(%q, %d) = %q, want %q", c.planDate, c.count, got, c.want)
			}
		})
	}
}

// ---------- 5) pruneSentenceHistory ----------

func TestPruneSentenceHistoryRules(t *testing.T) {
	cases := []struct {
		name  string
		hist  SentenceHistory
		today string
		days  int
		want  SentenceHistory
	}{
		{
			name: "保留边界当天删除更早一天",
			hist: SentenceHistory{
				"2026-09-20": {"a"},
				"2026-09-13": {"b"}, // 正好 7 天前 → 保留
				"2026-09-12": {"c"}, // 8 天前 → 删
			},
			today: "2026-09-20",
			days:  7,
			want: SentenceHistory{
				"2026-09-20": {"a"},
				"2026-09-13": {"b"},
			},
		},
		{
			name: "边界正好等于days保留",
			hist: SentenceHistory{
				"2026-08-21": {"a"}, // 正好 30 天前 → 保留
				"2026-08-20": {"b"}, // 31 天前 → 删
			},
			today: "2026-09-20",
			days:  30,
			want:  SentenceHistory{"2026-08-21": {"a"}},
		},
		{
			name: "days为0只保留今天",
			hist: SentenceHistory{
				"2026-09-20": {"a"},
				"2026-09-19": {"b"},
			},
			today: "2026-09-20",
			days:  0,
			want:  SentenceHistory{"2026-09-20": {"a"}},
		},
		{
			name:  "空map不panic",
			hist:  SentenceHistory{},
			today: "2026-09-20",
			days:  30,
			want:  SentenceHistory{},
		},
		{
			name:  "nil map不panic",
			hist:  nil,
			today: "2026-09-20",
			days:  30,
			want:  nil,
		},
		{
			name:  "单键保留不变且不多出键",
			hist:  SentenceHistory{"2026-09-01": {"a"}},
			today: "2026-09-20",
			days:  30,
			want:  SentenceHistory{"2026-09-01": {"a"}},
		},
		{
			name: "未来日期不受清理影响",
			hist: SentenceHistory{
				"2027-01-01": {"future"},
				"2026-01-01": {"past"},
			},
			today: "2026-09-20",
			days:  30,
			want:  SentenceHistory{"2027-01-01": {"future"}},
		},
		{
			// today 解析失败时实现直接 return，一条都不删
			name: "today非法则整个不动",
			hist: SentenceHistory{
				"2026-09-20": {"a"},
				"2020-01-01": {"b"},
				"格式不对":       {"c"},
			},
			today: "不是日期",
			days:  30,
			want: SentenceHistory{
				"2026-09-20": {"a"},
				"2020-01-01": {"b"},
				"格式不对":       {"c"},
			},
		},
		{
			// days 为负时 cutoff 落到未来：只有小于 cutoff 的键被删，
			// 正好等于 cutoff 的那天按 Before 语义保留
			name: "days为负数则cutoff在未来仅保留等于cutoff的键",
			hist: SentenceHistory{
				"2026-09-20": {"a"},
				"2026-09-25": {"future"},
				"2026-10-01": {"far"},
			},
			today: "2026-09-20",
			days:  -5,
			want:  SentenceHistory{"2026-09-25": {"future"}, "2026-10-01": {"far"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pruneSentenceHistory(c.hist, c.today, c.days)
			if !reflect.DeepEqual(c.hist, c.want) {
				t.Errorf("pruneSentenceHistory(..., %q, %d) = %#v, want %#v", c.today, c.days, c.hist, c.want)
			}
		})
	}
}

// ---------- 6) mergeUnique ----------

func TestMergeUniqueRules(t *testing.T) {
	cases := []struct {
		name string
		dst  []string
		src  []string
		want []string
	}{
		{
			name: "追加新句保序",
			dst:  []string{"x", "y"},
			src:  []string{"p", "q"},
			want: []string{"x", "y", "p", "q"},
		},
		{
			name: "已存在的不再追加",
			dst:  []string{"a"},
			src:  []string{"a", "b"},
			want: []string{"a", "b"},
		},
		{
			name: "全部重复则原样返回",
			dst:  []string{"a", "b"},
			src:  []string{"a", "b"},
			want: []string{"a", "b"},
		},
		{
			name: "空src原样返回dst",
			dst:  []string{"x"},
			src:  nil,
			want: []string{"x"},
		},
		{
			name: "空dst则返回src去重结果",
			dst:  nil,
			src:  []string{"a", "b"},
			want: []string{"a", "b"},
		},
		{
			name: "两端都空",
			dst:  nil,
			src:  nil,
			want: nil,
		},
		{
			name: "归一化后相同视为重复",
			dst:  []string{"甲です。"},
			src:  []string{"甲です"},
			want: []string{"甲です。"},
		},
		{
			name: "src内部重复只留第一个",
			dst:  nil,
			src:  []string{"a", "a", "b"},
			want: []string{"a", "b"},
		},
		{
			name: "src里的空串跳过",
			dst:  nil,
			src:  []string{"", "a"},
			want: []string{"a"},
		},
		{
			name: "src里归一化后为空的跳过",
			dst:  []string{"x"},
			src:  []string{"。", "  ", "　"},
			want: []string{"x"},
		},
		{
			name: "全角半角混用去重",
			dst:  []string{"２万円です"},
			src:  []string{"2万円です。"},
			want: []string{"２万円です"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeUnique(c.dst, c.src)
			if !eqStrings(got, c.want) {
				t.Errorf("mergeUnique(%v, %v) = %v, want %v", c.dst, c.src, got, c.want)
			}
		})
	}
}

// ---------- 7) chineseFromResults ----------

func TestChineseFromResultsRules(t *testing.T) {
	cases := []struct {
		name    string
		results []SentenceResult
		answer  string
		want    string
	}{
		{
			name:    "字面命中",
			results: []SentenceResult{{Number: 1, Answer: "甲です。", Chinese: "甲译文"}},
			answer:  "甲です。",
			want:    "甲译文",
		},
		{
			name:    "归一化命中（answer不带句号）",
			results: []SentenceResult{{Number: 1, Answer: "甲です。", Chinese: "甲译文"}},
			answer:  "甲です",
			want:    "甲译文",
		},
		{
			name:    "归一化命中（answer带全角空格）",
			results: []SentenceResult{{Number: 1, Answer: "李さんは 来ます。", Chinese: "小李要来"}},
			answer:  "李さんは　来ます",
			want:    "小李要来",
		},
		{
			name:    "未命中返回空串",
			results: []SentenceResult{{Number: 1, Answer: "甲です。", Chinese: "甲译文"}},
			answer:  "乙です。",
			want:    "",
		},
		{
			name:    "空results返回空串",
			results: nil,
			answer:  "甲です。",
			want:    "",
		},
		{
			name: "多条命中取第一条",
			results: []SentenceResult{
				{Number: 1, Answer: "甲です", Chinese: "第一条"},
				{Number: 2, Answer: "甲です。", Chinese: "第二条"},
			},
			answer: "甲です。",
			want:   "第一条",
		},
		{
			name: "第一条中文为空则继续往后找",
			results: []SentenceResult{
				{Number: 1, Answer: "甲です", Chinese: ""},
				{Number: 2, Answer: "甲です。", Chinese: "第二条"},
			},
			answer: "甲です。",
			want:   "第二条",
		},
		{
			name: "全部命中但中文都空则返回空串",
			results: []SentenceResult{
				{Number: 1, Answer: "甲です", Chinese: ""},
				{Number: 2, Answer: "甲です。", Chinese: ""},
			},
			answer: "甲です。",
			want:   "",
		},
		{
			name: "中间夹着不匹配的条目不影响",
			results: []SentenceResult{
				{Number: 1, Answer: "乙です。", Chinese: "乙译文"},
				{Number: 2, Answer: "甲です。", Chinese: "甲译文"},
			},
			answer: "甲です。",
			want:   "甲译文",
		},
		{
			// 实现的真实行为：answer 归一化成空串时，会命中 results 里同样
			// 归一化为空串（如纯标点）的条目。不是 bug，固定住。
			name:    "answer为空串时命中同样归一化为零的条目",
			results: []SentenceResult{{Number: 1, Answer: "。", Chinese: "标点条目"}},
			answer:  "",
			want:    "标点条目",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chineseFromResults(c.results, c.answer); got != c.want {
				t.Errorf("chineseFromResults(%v, %q) = %q, want %q", c.results, c.answer, got, c.want)
			}
		})
	}
}
