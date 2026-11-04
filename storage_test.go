// storage_test.go —— 存档 / 快照 / 用户表 / 对象存储桩相关测试（配 storage.go）
//
// 腿一收尾：把原本平铺在根目录的 4 个 *_test.go 按被测域物理合并到本文件。
// 测试函数体逐字保留，仅对 import 取并集去重；被测行为、断言、测试名均不变。
// 每个分区以「原文件名」标注，便于定位；文件头来源清单列出各源顶层声明。
//
// 来源与顶层声明：
// archive_test.go: TestParseArchiveGarbageFooterShrinks, TestReviewTypeAliasesJSON, TestArchiveReviewRoundTrip
// review_test.go: TestReviewKey, TestValidReviewMode, TestMergeReview, TestMergeReviewEmptyOld, TestMergeReviewPartialCommit, TestMergeReviewBlankDoesNotClobberAnswered, TestMergeReviewAnsweredClobbersBlank, TestMergeReviewNewBatchKeepsEarlierWords, TestMergeReviewSameWordSameDay, TestMergeReviewDropsDuplicateWordRows
// bootstrap_test.go: memObjStore, newMemObjStore, HeadObject, ListObjects, GetAll, PutObject, DeleteObject, GetObject, GetRange, PutObjectStream, ... (共 16 个顶层声明)
// storage_user_test.go: TestOwnerKeysAreUnchanged, TestOwnerSharesTextbookKeys, TestForUserIsolatesPersonalKeepsTextbookShared, draftKeyOf, reviewKeyOf, TestFirstUserIsOwnerWithEmptyUID, TestRoleForNewUser
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/objstore"
)

// ==========================================================================
// 原 archive_test.go
// ==========================================================================

// 2026-09-24 事故：词表表头行出现在 |--- 之前，解析时被当"正文"塞进 RawFooter，
// 写出时原样保留 —— 每次保存 footer 多 58 行（=分组数），积到 1 万行。
// 回归测试：带垃圾 footer 的档案解析→写出→再解析，词数不变且 footer 不再增长。
func TestParseArchiveGarbageFooterShrinks(t *testing.T) {
	garbage := strings.Repeat("|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n", 10) +
		strings.Repeat("|||||||\n", 5)

	mk := func() string {
		var b strings.Builder
		b.WriteString("## 📋 版本历史\n\n")
		b.WriteString("|日期|版本|总词数|🟢已掌握|🟡基本掌握|🔴待巩固|🔄待测试|累计答错|钉子户|变化内容|\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
		b.WriteString("|09/24|v1.0|2|0|0|0|2|0|0|测试|\n")
		b.WriteString("\n\n")
		b.WriteString("# 日语单词学习进度档案\n\n")
		b.WriteString("## 📅 单词列表（按课分组）\n\n")
		b.WriteString("### 第1课 测试\n\n")
		b.WriteString("|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		b.WriteString("|すし|寿司|3|1|2|09/23|🟡基本掌握|\n")
		b.WriteString("### 第2课 测试\n\n")
		b.WriteString("|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		b.WriteString("|はし(橋)|桥|2|0|2|09/22|🟡基本掌握|\n")
		b.WriteString("\n")
		b.WriteString(garbage)
		return b.String()
	}

	// 第一轮解析+写出
	arc1, err := archive.ParseArchive(mk(), "ja")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got := len(review.AllWords(arc1.Groups)); got != 2 {
		t.Fatalf("第一轮词数 = %d, want 2", got)
	}
	cfg := LangConfigs["ja"]
	out1 := archive.WriteArchive(arc1, cfg.Name, cfg.WordColumn)
	if strings.Count(out1, "|日语单词|中文释义|") != 2 {
		t.Fatalf("第一轮写出后表头行数 = %d, want 2（每个分组恰好一个）",
			strings.Count(out1, "|日语单词|中文释义|"))
	}

	// 第二轮解析+写出：footer 必须稳定，不再增长
	arc2, err := archive.ParseArchive(out1, "ja")
	if err != nil {
		t.Fatalf("第二轮解析失败：%v", err)
	}
	if got := len(review.AllWords(arc2.Groups)); got != 2 {
		t.Fatalf("第二轮词数 = %d, want 2（垃圾行不能被当词）", got)
	}
	cfg = LangConfigs["ja"]
	out2 := archive.WriteArchive(arc2, cfg.Name, cfg.WordColumn)
	if got := strings.Count(out2, "|日语单词|中文释义|"); got != 2 {
		t.Fatalf("第二轮写出后表头行数 = %d, want 2（footer 不能每次写涨 2 行）", got)
	}
	if strings.Count(out2, "|||||||") > 0 {
		t.Fatalf("全空行不应被保留进 footer")
	}
}

func TestReviewTypeAliasesJSON(t *testing.T) {
	// 切片双向直接赋值，编译期保护类型别名；不约束迁包后的类型元数据。
	var words []Word
	var reviewWords []review.Word = words
	words = reviewWords
	var groups []WordGroup
	var reviewGroups []review.WordGroup = groups
	groups = reviewGroups

	word := Word{
		Word: "はし(橋)", Definition: "桥", ReviewCount: 6, ErrorCount: 1,
		ConsecutiveCorrect: 3, LastReview: "01/02", Status: "\U0001f7e1基本掌握", Group: "第2课",
	}
	cases := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{
			name: "单词全字段", value: word,
			expected: `{"Word":"はし(橋)","Definition":"桥","ReviewCount":6,"ErrorCount":1,"ConsecutiveCorrect":3,"LastReview":"01/02","Status":"` + "\U0001f7e1基本掌握" + `","Group":"第2课"}`,
		},
		{
			name: "单词零值", value: Word{},
			expected: `{"Word":"","Definition":"","ReviewCount":0,"ErrorCount":0,"ConsecutiveCorrect":0,"LastReview":"","Status":"","Group":""}`,
		},
		{
			name: "分组零值及nil切片", value: WordGroup{},
			expected: `{"Title":"","Words":null}`,
		},
		{
			name: "分组空切片", value: WordGroup{Title: "空组", Words: []Word{}},
			expected: `{"Title":"空组","Words":[]}`,
		},
		{
			name: "分组嵌套单词", value: WordGroup{Title: "第2课", Words: []Word{word}},
			expected: `{"Title":"第2课","Words":[{"Word":"はし(橋)","Definition":"桥","ReviewCount":6,"ErrorCount":1,"ConsecutiveCorrect":3,"LastReview":"01/02","Status":"` + "\U0001f7e1基本掌握" + `","Group":"第2课"}]}`,
		},
	}
	for _, tc := range cases {
		data, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatalf("%s序列化失败：%v", tc.name, err)
		}
		if string(data) != tc.expected {
			t.Errorf("%s JSON = %s，预期 %s", tc.name, data, tc.expected)
		}
	}
}

func TestArchiveReviewRoundTrip(t *testing.T) {
	const source = "# 日语单词学习进度档案\n\n" +
		"## \U0001f4c5 单词列表（按课分组）\n\n" +
		"### 第2课 水与季节\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|みず(水)|水|4|0|4|01/01|\U0001f7e1基本掌握|\n" +
		"|なつ(夏)|夏天|0|0|0||\U0001f504待测试|\n\n" +
		"### 第1课 交通\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|はし(橋)|桥|3|1|2|12/31|\U0001f7e1基本掌握|\n"
	arc, err := archive.ParseArchive(source, "ja")
	if err != nil {
		t.Fatal(err)
	}
	expectedBefore := []WordGroup{
		{Title: "第2课 水与季节", Words: []Word{
			{Word: "みず(水)", Definition: "水", ReviewCount: 4, ErrorCount: 0, ConsecutiveCorrect: 4, LastReview: "01/01", Status: "\U0001f7e1基本掌握", Group: "第2课 水与季节"},
			{Word: "なつ(夏)", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: "\U0001f504待测试", Group: "第2课 水与季节"},
		}},
		{Title: "第1课 交通", Words: []Word{
			{Word: "はし(橋)", Definition: "桥", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "12/31", Status: "\U0001f7e1基本掌握", Group: "第1课 交通"},
		}},
	}
	if !reflect.DeepEqual(arc.Groups, expectedBefore) {
		t.Fatalf("首次解析的分组、顺序或字段不符：got %+v，预期 %+v", arc.Groups, expectedBefore)
	}
	correct, gi := review.FindWord(arc.Groups, "みず(水)")
	if gi != 0 || correct != &arc.Groups[0].Words[0] {
		t.Fatal("FindWord 必须返回第一组原元素指针")
	}
	wrong, gi := review.FindWord(arc.Groups, "はし(橋)")
	if gi != 1 || wrong != &arc.Groups[1].Words[0] {
		t.Fatal("FindWord 必须返回第二组原元素指针")
	}
	date := time.Date(2020, time.January, 3, 12, 0, 0, 0, time.UTC)
	review.RecordCorrect(correct, date)
	review.RecordWrong(wrong, date)
	expectedAfter := []WordGroup{
		{Title: "第2课 水与季节", Words: []Word{
			{Word: "みず(水)", Definition: "水", ReviewCount: 5, ErrorCount: 0, ConsecutiveCorrect: 5, LastReview: "01/03", Status: "\U0001f7e2已掌握", Group: "第2课 水与季节"},
			{Word: "なつ(夏)", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: "\U0001f504待测试", Group: "第2课 水与季节"},
		}},
		{Title: "第1课 交通", Words: []Word{
			{Word: "はし(橋)", Definition: "桥", ReviewCount: 4, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "01/03", Status: "\U0001f534待巩固", Group: "第1课 交通"},
		}},
	}
	if !reflect.DeepEqual(arc.Groups, expectedAfter) {
		t.Fatalf("原档案回写字段不符：got %+v，预期 %+v", arc.Groups, expectedAfter)
	}
	cfg := LangConfigs["ja"]
	out := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
	// 只比较词表，不把动态“最后更新”或非目标的空组切片形态锁死。
	const expectedTables = "### 第2课 水与季节\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|みず(水)|水|5|0|5|01/03|\U0001f7e2已掌握|\n" +
		"|なつ(夏)|夏天|0|0|0||\U0001f504待测试|\n\n\n" +
		"### 第1课 交通\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|はし(橋)|桥|4|2|0|01/03|\U0001f534待巩固|"
	_, tables, found := strings.Cut(out, "## \U0001f4c5 单词列表（按课分组）\n\n")
	if !found || strings.TrimSpace(tables) != expectedTables {
		t.Fatalf("写出的词表必须保持七列及原顺序：\n%s", tables)
	}
	roundTrip, err := archive.ParseArchive(out, "ja")
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Language != "ja" || !reflect.DeepEqual(roundTrip.Groups, expectedAfter) {
		t.Fatalf("再次解析字段不符：got %+v，预期 %+v", roundTrip, expectedAfter)
	}
}

// ==========================================================================
// 原 review_test.go
// ==========================================================================

func TestReviewKey(t *testing.T) {
	s := &Storage{lang: "ja"}
	got := s.reviewKey("2026-09-20", "words")
	if !strings.HasPrefix(got, "language-review/ja/reviews/") {
		t.Fatalf("回看快照键应落在 reviews/ 前缀下，实际 %q", got)
	}
	if !strings.HasSuffix(got, "review_2026-09-20_words.json") {
		t.Fatalf("回看快照键应带日期和模式，实际 %q", got)
	}
	// 今日练习和钉子户各占一个键
	if s.reviewKey("2026-09-20", "words") == s.reviewKey("2026-09-20", "hard") {
		t.Fatal("words 与 hard 的回看键撞了")
	}
	// 隔天的不能串到今天
	if s.reviewKey("2026-09-20", "words") == s.reviewKey("2026-09-21", "words") {
		t.Fatal("相邻两天的回看键撞了")
	}
}

func TestValidReviewMode(t *testing.T) {
	for _, m := range []string{"words", "hard", "WORDS"} { // 大小写宽容，调用方已 ToLower
		if !validReviewMode(m) {
			t.Errorf("validReviewMode(%q) = false, want true", m)
		}
	}
	// 模式直接进 COS 键，任意字符串会把键打散
	for _, m := range []string{"", "../../etc", "sentences", "offline"} {
		if validReviewMode(m) {
			t.Errorf("validReviewMode(%q) = true, want false", m)
		}
	}
}

func TestMergeReview(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "a", Correct: true},
		{Number: 2, Word: "b", Correct: false},
		{Number: 3, Word: "c", Correct: true},
	}

	// 覆盖旧的 + 追加新的，且保持旧的顺序（号码合并后重排成 1..N 连续）
	got := mergeReview([]ReviewItem{
		{Number: 2, Word: "b", Correct: true, Manual: true},
		{Number: 9, Word: "i", Correct: false},
		{Number: 1, Word: "a", Correct: false},
	}, old)

	want := []ReviewItem{
		{Number: 1, Word: "a", Correct: false},
		{Number: 2, Word: "b", Correct: true, Manual: true},
		{Number: 3, Word: "c", Correct: true},
		{Number: 4, Word: "i", Correct: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeReview =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMergeReviewEmptyOld(t *testing.T) {
	in := []ReviewItem{{Number: 5, Word: "e"}}
	got := mergeReview(in, nil)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("mergeReview with no old = %+v, want %+v", got, in)
	}
}

// 同一批词分两次回写（先写一半回写，剩下的写完再回写）不能把前一半冲掉。
func TestMergeReviewPartialCommit(t *testing.T) {
	first := []ReviewItem{
		{Number: 1, Word: "a", Correct: true},
		{Number: 2, Word: "b", Correct: false},
	}
	merged := mergeReview(first, nil)

	second := []ReviewItem{
		{Number: 3, Word: "c", Correct: true},
		{Number: 4, Word: "d", Answer: "で", Correct: false},
	}
	merged = mergeReview(second, merged)

	if len(merged) != 4 {
		t.Fatalf("merged len = %d, want 4", len(merged))
	}
	if merged[0].Number != 1 || merged[3].Number != 4 {
		t.Errorf("order wrong: %+v", merged)
	}
}

// 老师中途刷新页面（客户端状态清零）后整批重发，早前批次已答的词在
// 新批次里全成了 blank —— blank 不能把已答条目（尤其错词）覆盖掉。
// 2026-10-05 事故：当天 10 个错词被抹得只剩 7 个。
func TestMergeReviewBlankDoesNotClobberAnswered(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "a", Correct: false, Answer: "あけみ", Status: "🔴待巩固"},
		{Number: 2, Word: "b", Correct: true},
		{Number: 3, Word: "c", Blank: true},
	}
	// 刷新后的整批重发：#1 #2 变 blank，#3 照旧 blank，#4 是新练的
	newer := []ReviewItem{
		{Number: 1, Word: "a", Blank: true},
		{Number: 2, Word: "b", Blank: true},
		{Number: 3, Word: "c", Blank: true},
		{Number: 4, Word: "d", Correct: false, Answer: "で"},
	}
	got := mergeReview(newer, old)

	if len(got) != 4 {
		t.Fatalf("merged len = %d, want 4", len(got))
	}
	if got[0].Number != 1 || got[0].Blank || got[0].Correct || got[0].Answer != "あけみ" {
		t.Errorf("#1 blank 不该覆盖已答错词: %+v", got[0])
	}
	if got[1].Number != 2 || got[1].Blank || !got[1].Correct {
		t.Errorf("#2 blank 不该覆盖已答对词: %+v", got[1])
	}
	if got[2].Number != 3 || !got[2].Blank {
		t.Errorf("#3 双方都是 blank，取新: %+v", got[2])
	}
	if got[3].Number != 4 || got[3].Blank {
		t.Errorf("#4 新练的照常追加: %+v", got[3])
	}
}

// 反向：刷新后老师把之前空着的词练了（非 blank）→ 正常覆盖。
func TestMergeReviewAnsweredClobbersBlank(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "a", Blank: true},
	}
	got := mergeReview([]ReviewItem{
		{Number: 1, Word: "a", Correct: false, Answer: "みせ"},
	}, old)
	if got[0].Blank || got[0].Correct || got[0].Answer != "みせ" {
		t.Errorf("非 blank 该覆盖 blank: %+v", got[0])
	}
}

// 一天里会出好几批 plan（回写成功 → plan 清 → 新一批），**每批题号都从 1 重新编**。
// 按号码合并的话，后一批的 #1 会盖掉前一批的 #1 —— 前几批练过的词全丢。
// 2026-10-05 事故：当天档案里 83 个词的 LastReview 是当天，快照只剩 44 个词，
// 老师看到的「共 75 / 对 62 / 错 9」全是虚的。
func TestMergeReviewNewBatchKeepsEarlierWords(t *testing.T) {
	// 第一批：#1 #2
	old := []ReviewItem{
		{Number: 1, Word: "あ", Correct: true, Answer: "あ"},
		{Number: 2, Word: "い", Correct: false, Answer: "いけ"},
	}
	// 第二批（回写后新出的）：题号重新从 1 开始，且是另一批词
	got := mergeReview([]ReviewItem{
		{Number: 1, Word: "う", Correct: true, Answer: "う"},
		{Number: 2, Word: "え", Correct: true, Answer: "え"},
	}, old)

	if len(got) != 4 {
		t.Fatalf("新一批的词应全部保留（按词合并），len = %d, want 4: %+v", len(got), got)
	}
	want := []string{"あ", "い", "う", "え"}
	for i, w := range want {
		if got[i].Word != w {
			t.Errorf("got[%d].Word = %q, want %q", i, got[i].Word, w)
		}
	}
	// 第一批的错词不能因为号码撞车被冲掉
	if got[1].Correct || got[1].Answer != "いけ" {
		t.Errorf("第一批的错词被新一批盖掉了: %+v", got[1])
	}
	// 号码重排成 1..N 连续，否则前端按号码索引会错位
	for i, it := range got {
		if it.Number != i+1 {
			t.Errorf("got[%d].Number = %d, want %d（重排避免跨批次撞号）", i, it.Number, i+1)
		}
	}
}

// 同一天同一个词被练了两次（两批都抽到它）→ 只留一条，后一次覆盖前一次。
func TestMergeReviewSameWordSameDay(t *testing.T) {
	old := []ReviewItem{
		{Number: 1, Word: "あ", Correct: false, Answer: "い"},
	}
	got := mergeReview([]ReviewItem{
		{Number: 7, Word: "あ", Correct: true, Answer: "あ"},
	}, old)
	if len(got) != 1 {
		t.Fatalf("同一天同一个词只留一条，len = %d, want 1: %+v", len(got), got)
	}
	if !got[0].Correct || got[0].Answer != "あ" {
		t.Errorf("后一次该覆盖前一次: %+v", got[0])
	}
}

// 历史遗留：号码撞车时同一个词被写了两遍（快照里出现两条同词不同号）。
// 合并后必须收敛成一条，否则「共 N / 对 X」会把同一个词数两次。
// 2026-10-05：同一事故里快照 101 条其实只有 68 个词。
func TestMergeReviewDropsDuplicateWordRows(t *testing.T) {
	old := []ReviewItem{
		{Number: 6, Word: "あ", Correct: false, Answer: "い"},
		{Number: 18, Word: "あ", Blank: true},
		{Number: 49, Word: "あ", Blank: true},
		{Number: 7, Word: "う", Correct: true},
	}
	got := mergeReview([]ReviewItem{
		{Number: 1, Word: "う", Correct: true},
	}, old)

	if len(got) != 2 {
		t.Fatalf("同一个词只应留一条，len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Word != "あ" || got[0].Blank {
		t.Errorf("重复条目应保留有信息量那条（错词，不是 blank）: %+v", got[0])
	}
	if got[0].Number != 1 || got[1].Number != 2 {
		t.Errorf("号码应重排成 1..N: %+v", got)
	}
}

// ==========================================================================
// 原 bootstrap_test.go
// ==========================================================================

// memObjStore 测试用的内存对象存储，只实现 EnsureArchive 用到的那几条路径。
type memObjStore struct {
	mu   sync.Mutex
	objs map[string][]byte
	mod  map[string]time.Time
}

func newMemObjStore() *memObjStore {
	return &memObjStore{objs: map[string][]byte{}, mod: map[string]time.Time{}}
}

func (m *memObjStore) HeadObject(ctx context.Context, key string) (*objstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.objs[key]; ok {
		return &objstore.ObjectInfo{Key: key, Size: int64(len(d)), LastModified: m.mod[key]}, nil
	}
	return nil, errNotFound
}

func (m *memObjStore) ListObjects(ctx context.Context, opts objstore.ListOptions) ([]objstore.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []objstore.ObjectInfo{}
	for k, d := range m.objs {
		if strings.HasPrefix(k, opts.Prefix) {
			out = append(out, objstore.ObjectInfo{Key: k, Size: int64(len(d)), LastModified: m.mod[k]})
		}
	}
	return out, nil
}

func (m *memObjStore) GetAll(ctx context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.objs[key]; ok {
		return d, nil
	}
	return nil, errNotFound
}

func (m *memObjStore) PutObject(ctx context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objs[key] = cp
	m.mod[key] = time.Now()
	return nil
}

func (m *memObjStore) DeleteObject(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objs, key)
	return nil
}

func (m *memObjStore) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	d, err := m.GetAll(ctx, key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(string(d))), nil
}

func (m *memObjStore) GetRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	return nil, nil
}
func (m *memObjStore) PutObjectStream(ctx context.Context, key string, r io.Reader, size int64) error {
	return nil
}
func (m *memObjStore) MultipartUpload(ctx context.Context, key string, totalSize, chunkSize int64,
	concurrency int, fetchPart func(partNumber int, offset, size int64) ([]byte, error)) error {
	return nil
}
func (m *memObjStore) PresignGetObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	return "", nil
}
func (m *memObjStore) PresignPutObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	return "", nil
}
func (m *memObjStore) BucketName() string          { return "mem" }
func (m *memObjStore) Provider() objstore.ProviderType { return objstore.ProviderCOS }

var errNotFound = fmt.Errorf("not found")

// 新账号引导：第一次进来没有档案也要能走通，而不是 500「no archives found」。
func TestBootstrapEmptyAccount(t *testing.T) {
	st := &Storage{lang: "ja", store: newMemObjStore()}
	ctx := context.Background()

	if st.hasArchive(ctx) {
		t.Fatal("新账号不该有档案")
	}

	// 写路径：EnsureArchive 造出空档案并落盘
	arc, fname, err := st.EnsureArchive(ctx, "ja")
	if err != nil {
		t.Fatalf("EnsureArchive: %v", err)
	}
	if arc == nil || len(arc.Groups) != 0 {
		t.Errorf("该拿到空档案，got %+v", arc)
	}
	if fname == "" {
		t.Error("该有档案文件名")
	}
	if !st.hasArchive(ctx) {
		t.Error("EnsureArchive 之后该有档案了")
	}

	// 第二次拿的必须是刚落盘那份（而不是又造一份）
	arc2, fname2, err := st.EnsureArchive(ctx, "ja")
	if err != nil {
		t.Fatalf("第二次 EnsureArchive: %v", err)
	}
	if fname2 != fname {
		t.Errorf("第二次该复用同一份档案，got %s vs %s", fname2, fname)
	}
	if arc2 == nil {
		t.Error("第二次该拿到档案")
	}

	// 读路径：没档案 → 空档案（不落盘）
	st2 := &Storage{lang: "ja", store: newMemObjStore()}
	arc3, err := st2.LoadArchiveForRead(ctx, "ja")
	if err != nil {
		t.Fatalf("LoadArchiveForRead: %v", err)
	}
	if arc3 == nil || len(arc3.Groups) != 0 {
		t.Error("没档案该回空档案")
	}
	if st2.hasArchive(ctx) {
		t.Error("读路径不许写 COS")
	}

	// 空档案也必须能录词（审核员录第一个词就是这条路）
	res, err := addWordsToArchive(ctx, st2, "", []WordToAdd{{Word: "みず(水)", Definition: "水"}}, false)
	if err != nil {
		t.Fatalf("空档案录词失败: %v", err)
	}
	if res.Added != 1 {
		t.Errorf("该录进 1 个词，实际 %d", res.Added)
	}
}

// ==========================================================================
// 原 storage_user_test.go
// ==========================================================================

// 加账号体系时最怕的一件事：owner（老师）的 COS key 被改动，等于老数据一夜之间
// 「消失」。这组测试把「owner 的 key 与 2026-10 之前逐字节相同」钉死，
// 顺便把「普通成员走 users/<uid>/ 前缀、教材仍然共享」也钉死。
// 真要改 key 规则，这里必须先红 —— 那就意味着要写迁移脚本，不是随手改改。

func TestOwnerKeysAreUnchanged(t *testing.T) {
	s := &Storage{lang: "ja"} // uid 为空 = owner
	const p = "language-review/ja"

	cases := map[string]string{
		"archive prefix":  s.personalBase() + "/archives/",
		"history prefix":  s.personalBase() + "/history/",
		"daily plan":      s.planJSONKey("", "2026-10-20"),
		"hard plan":       s.planJSONKey("hard", "2026-10-20"),
		"sentence plan":   s.planJSONKey("sentences", "2026-10-20"),
		"draft":           draftKeyOf(s),
		"review snapshot": reviewKeyOf(s),
		"sentence hist":   s.sentenceHistoryKey(),
		"sentence wrong":  s.sentenceWrongKey(),
	}
	want := map[string]string{
		"archive prefix":  p + "/archives/",
		"history prefix":  p + "/history/",
		"daily plan":      p + "/plans/plan_2026-10-20.json",
		"hard plan":       p + "/plans/hard_2026-10-20.json",
		"sentence plan":   p + "/plans/sentences_2026-10-20.json",
		"draft":           p + "/drafts/draft_2026-10-20_words.json",
		"review snapshot": p + "/reviews/review_2026-10-20_words.json",
		"sentence hist":   p + "/plans/sentence_history.json",
		"sentence wrong":  p + "/plans/sentence_wrong.json",
	}
	for name, got := range cases {
		if got != want[name] {
			t.Errorf("%s: got %q, want %q（owner 的 key 变了 = 老数据读不到）", name, got, want[name])
		}
	}
}

func TestOwnerSharesTextbookKeys(t *testing.T) {
	s := &Storage{lang: "ja"}
	const p = "language-review/ja"
	cases := map[string]string{
		"sentence bank": s.sentenceBankKey(),
		"review bank":   s.reviewBankKey(),
		"word meta":     s.wordMetaKey(),
		"plan config":   s.planConfigKey(),
		"judge prompt":  s.judgePromptKey(),
		"knowledge":     s.sharedBase() + "/knowledge/x.md",
		"shared base":   s.sharedBase(),
		"personal base": s.personalBase(),
	}
	want := map[string]string{
		"sentence bank": p + "/plans/sentence_bank.json",
		"review bank":   p + "/plans/sentence_review.json",
		"word meta":     p + "/word_meta.json",
		"plan config":   p + "/configs/plan_config.json",
		"judge prompt":  p + "/configs/judge_prompt.txt",
		"knowledge":     p + "/knowledge/x.md",
		"shared base":   p,
		"personal base": p,
	}
	for name, got := range cases {
		if got != want[name] {
			t.Errorf("%s: got %q, want %q", name, got, want[name])
		}
	}
}

func TestForUserIsolatesPersonalKeepsTextbookShared(t *testing.T) {
	owner := &Storage{lang: "ja"}
	if !owner.IsOwner() {
		t.Fatalf("uid 为空应该是 owner")
	}
	member := owner.ForUser("u_7f3a2b9c")
	if member.IsOwner() || member.UserID() != "u_7f3a2b9c" {
		t.Fatalf("ForUser 没绑上 uid：%q owner=%v", member.UserID(), member.IsOwner())
	}
	if owner.UserID() != "" {
		t.Errorf("ForUser 把原对象也改了（不该有副作用）：%q", owner.UserID())
	}

	const mp = "language-review/ja/users/u_7f3a2b9c"
	personal := map[string]string{
		"daily plan":     member.planJSONKey("", "2026-10-20"),
		"draft":          draftKeyOf(member),
		"review":         reviewKeyOf(member),
		"sentence hist":  member.sentenceHistoryKey(),
		"sentence wrong": member.sentenceWrongKey(),
	}
	wantPersonal := map[string]string{
		"daily plan":     mp + "/plans/plan_2026-10-20.json",
		"draft":          mp + "/drafts/draft_2026-10-20_words.json",
		"review":         mp + "/reviews/review_2026-10-20_words.json",
		"sentence hist":  mp + "/plans/sentence_history.json",
		"sentence wrong": mp + "/plans/sentence_wrong.json",
	}
	for name, got := range personal {
		if got != wantPersonal[name] {
			t.Errorf("个人数据 %s: got %q, want %q", name, got, wantPersonal[name])
		}
	}

	// 教材和配置对所有人都是同一份 —— 按人复制等于改一句要改两遍。
	shared := map[string]string{
		"sentence bank": member.sentenceBankKey(),
		"review bank":   member.reviewBankKey(),
		"word meta":     member.wordMetaKey(),
		"plan config":   member.planConfigKey(),
		"judge prompt":  member.judgePromptKey(),
	}
	for name, got := range shared {
		if want := owner.sharedBase(); got == "" || got[:len(want)] != want {
			t.Errorf("教材 %s 不该带用户前缀：%q", name, got)
		}
		if member.personalBase() != mp {
			t.Errorf("personalBase got %q, want %q", member.personalBase(), mp)
		}
	}
}

// 这两个 key 函数未导出且带参数，测试里包一层避免重复拼日期。
func draftKeyOf(s *Storage) string  { return s.draftKey("2026-10-20", "words") }
func reviewKeyOf(s *Storage) string { return s.reviewKey("2026-10-20", "words") }

// TestFirstUserIsOwnerWithEmptyUID：第一个登录的人必须是 owner，且 uid 为空。
// ⚠️ 这是零迁移的命门：owner 一旦拿到 u_xxx，personalBase() 就会去读
// users/u_xxx/archives/（空目录），老师登录完会发现「档案全没了」（2026-10-19 线上踩到）。
func TestFirstUserIsOwnerWithEmptyUID(t *testing.T) {
	st := &Storage{dryRun: true}
	ctx := context.Background()

	first, err := upsertUser(ctx, st, "openid-first")
	if err != nil {
		t.Fatalf("第一个用户创建失败：%v", err)
	}
	if first.Role != roleOwner {
		t.Errorf("第一个用户应是 owner，实际 %q", first.Role)
	}
	if first.UID != "" {
		t.Fatalf("owner 的 uid 必须为空（否则读不到老档案），实际 %q", first.UID)
	}

	// owner 的 Storage 走的必须还是老路径
	if !st.ForUser(first.UID).IsOwner() {
		t.Errorf("owner 的 Storage IsOwner() 应为 true")
	}

}

// TestRoleForNewUser：第一个 owner，其余 pending（要 owner approve 才能用）。
// dryRun 的 Storage 写不进用户表，所以这条逻辑单独用纯函数测。
func TestRoleForNewUser(t *testing.T) {
	if got := roleForNewUser(nil); got != roleOwner {
		t.Errorf("空用户表应给 owner，实际 %q", got)
	}
	if got := roleForNewUser([]User{{UID: "u_1", Role: roleOwner}}); got != rolePending {
		t.Errorf("已有用户应给 pending，实际 %q", got)
	}
}
