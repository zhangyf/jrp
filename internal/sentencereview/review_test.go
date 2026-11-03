package sentencereview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
)

// 固定「今天」，不依赖真实时钟、不 sleep。所有天数期望为手写常量（用真实日历
// 独立核算），禁止用被测函数自证。
var testToday = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

func w(last string) review.Word { return review.Word{Word: "ごご(午後)", LastReview: last} }

// ---------- WordIdleDays：跨年、容错、边界 ----------

func TestWordIdleDays(t *testing.T) {
	cases := []struct {
		name string
		last string
		want int
	}{
		{"今天", "11/03", 0},
		{"昨天", "11/02", 1},
		{"刚好30天", "10/04", 30},
		{"今年年初", "01/01", 306},
		{"晚于今天归去年_圣诞", "12/25", 313},
		{"晚于今天归去年_明天", "11/04", 364},
		{"带空格", "  10/04  ", 30},
		{"空值", "", -1},
		{"缺月份", "04", -1},
		{"三段", "10/04/2026", -1},
		{"非数字", "ab/cd", -1},
		{"月越界", "13/01", -1},
		{"月为零", "00/01", -1},
		{"日越界", "10/32", -1},
		{"日为零", "10/00", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WordIdleDays(w(c.last), testToday); got != c.want {
				t.Errorf("WordIdleDays(%q) = %d, want %d", c.last, got, c.want)
			}
		})
	}
}

// 02/30 这种「日历上不存在的日期」：time.Date 会归一到 03/02（今年、早于今天），
// 旧代码不做日历校验、不返回 -1 —— 锁定这个历史行为，不擅自修正。
func TestWordIdleDaysNormalizesInvalidCalendarDay(t *testing.T) {
	got := WordIdleDays(w("02/30"), testToday)
	// 2026-03-02 到 2026-11-03
	if want := 246; got != want {
		t.Errorf("02/30 归一后应为 %d 天，实际 %d", want, got)
	}
}

// ---------- wordIdleMap：nil / 空词 / 非法 / 去重 ----------

func TestWordIdleMap(t *testing.T) {
	if got := wordIdleMap(nil, testToday); len(got) != 0 {
		t.Errorf("nil 档案应返回空 map，实际 %v", got)
	}
	arc := &archive.Archive{Groups: []review.WordGroup{
		{Title: "第1课", Words: []review.Word{
			{Word: "あ", LastReview: "11/01"}, // 2 天
			{Word: "い", LastReview: "10/04"}, // 30 天
			{Word: "", LastReview: "10/01"},  // 空词形跳过
			{Word: "う", LastReview: "bad"},   // 非法 → -1 跳过
			{Word: "あ", LastReview: "09/01"}, // 同词形后写覆盖前值（09/01 → 63 天）
		}},
	}}
	got := wordIdleMap(arc, testToday)
	// 有效词只有 あ / い：空词形跳过、非法日期跳过、重复的 あ 后写覆盖。
	if len(got) != 2 {
		t.Fatalf("应有 2 个有效词，实际 %v", got)
	}
	// 2026-09-01 到 2026-11-03 = 63 天
	if got["あ"] != 63 {
		t.Errorf("同词形应被后写覆盖为 63，实际 %d", got["あ"])
	}
	if got["い"] != 30 {
		t.Errorf("い 应为 30 天，实际 %d", got["い"])
	}
	if _, ok := got[""]; ok {
		t.Error("空词形不该进 map")
	}
	if _, ok := got["う"]; ok {
		t.Error("非法日期不该进 map")
	}
}

// ---------- CircleRank：全档 + 兜底 ----------

func TestCircleRank(t *testing.T) {
	cases := []struct {
		val  string
		want int
	}{
		{"三圆", 0}, {"二圆", 1}, {"一圆", 2},
		{"没标", 3}, {"3圆", 3}, {"三星", 3}, {"", 3},
	}
	for _, c := range cases {
		circle := map[string]string{"x": c.val}
		if got := CircleRank(circle, "x"); got != c.want {
			t.Errorf("CircleRank(%q) = %d, want %d", c.val, got, c.want)
		}
	}
	if got := CircleRank(nil, "x"); got != 3 {
		t.Errorf("nil map 应落「没标」3，实际 %d", got)
	}
	if got := CircleRank(map[string]string{"x": "三圆"}, "y"); got != 3 {
		t.Errorf("查不到的词应落 3，实际 %d", got)
	}
}

// ---------- BuildReviewSentences：空闲/边界 ----------

func bankOf(ss ...ReviewSentence) *ReviewBank {
	return &ReviewBank{Version: 1, Updated: "2026-11-03", Sentences: ss}
}

func TestBuildReviewSentencesEmptyAndBadArgs(t *testing.T) {
	if got := BuildReviewSentences(nil, nil, nil, testToday, 3, nil, 0, nil, 0); got != nil {
		t.Errorf("bank=nil 应返回 nil，实际 %v", got)
	}
	if got := BuildReviewSentences(bankOf(), nil, nil, testToday, 0, nil, 0, nil, 0); got != nil {
		t.Errorf("n=0 应返回 nil，实际 %v", got)
	}
	if got := BuildReviewSentences(bankOf(), nil, nil, testToday, -1, nil, 0, nil, 0); got != nil {
		t.Errorf("n<0 应返回 nil，实际 %v", got)
	}
}

// 五条筛选 + 排序的完整回归：exclude、原型句 exclude、闲置过滤、答案非空、
// 「最久没出过」排序、同原型去重、Type 固定 transformed。
func TestBuildReviewSentencesFiltersAndOrder(t *testing.T) {
	mk := func(id, word, proto, ans string) ReviewSentence {
		return ReviewSentence{ID: id, Word: word, Prototype: proto, Answer: ans, Chinese: "释" + id}
	}
	bank := bankOf(
		mk("R1", "w1", "P1", "あの文です。"), // 从没出过，排最前
		mk("R2", "w2", "P2", "いの文です。"), // 有 lastSeen（较早）
		mk("R3", "w3", "P3", "うの文です。"), // 有 lastSeen（较近）
		mk("R4", "w4", "P4", ""),       // 空 answer → 跳过
		mk("R5", "w5", "P5", "えの文です。"), // 被 exclude
		mk("R6", "w6", "P6", "おの文です。"), // 原型句被 exclude
		mk("R7", "w7", "P1", "かの文です。"), // 与 R1 同原型 → 当天只留一条
	)
	hist := sentence.SentenceHistory{
		"2026-10-20": {"いの文です。"}, // R2 最近 10/20
		"2026-10-28": {"うの文です。"}, // R3 最近 10/28（比 R2 近 → R2 先）
		"bad-date":   {"あの文です。"}, // 非法日期跳过
	}
	exclude := map[string]bool{
		sentence.Norm("えの文です。"): true,
		sentence.Norm("P6"):     true,
	}
	got := BuildReviewSentences(bank, hist, nil, testToday, 10, exclude, 0, nil, 0)
	// R5/R6/R4 被过滤；R7 与 R1 同原型去重 → 剩 R1,R2,R3
	if len(got) != 3 {
		t.Fatalf("应出 3 句，实际 %d：%+v", len(got), got)
	}
	wantIDs := []string{"R1", "R2", "R3"} // 从没出过优先，再按最久没出过
	for i, want := range wantIDs {
		if got[i].ID != want {
			t.Errorf("第 %d 句应为 %s，实际 %s", i, want, got[i].ID)
		}
	}
	for _, g := range got {
		if g.Type != "transformed" {
			t.Errorf("%s 的 Type 应为 transformed，实际 %q", g.ID, g.Type)
		}
		if g.Chinese == "" {
			t.Errorf("%s 的 Chinese 未回填", g.ID)
		}
	}
}

// 闲置过滤：minIdleDays>0 时锚点词刚复习过（< 门槛）就跳过；arc=nil 时不限制。
func TestBuildReviewSentencesIdleFilter(t *testing.T) {
	mk := func(id, word, ans string) ReviewSentence {
		return ReviewSentence{ID: id, Word: word, Answer: ans}
	}
	bank := bankOf(
		mk("R1", "あ", "あの文。"), // 2 天前复习 → 该跳过
		mk("R2", "い", "いの文。"), // 30 天 → 保留
	)
	arc := &archive.Archive{Groups: []review.WordGroup{
		{Words: []review.Word{
			{Word: "あ", LastReview: "11/01"},
			{Word: "い", LastReview: "10/04"},
		}},
	}}
	got := BuildReviewSentences(bank, nil, arc, testToday, 10, nil, 30, nil, 0)
	if len(got) != 1 || got[0].ID != "R2" {
		t.Fatalf("只该保留 R2，实际 %+v", got)
	}
	// arc=nil 时跳过闲置过滤，两条都出
	gotAll := BuildReviewSentences(bank, nil, nil, testToday, 10, nil, 30, nil, 0)
	if len(gotAll) != 2 {
		t.Errorf("arc=nil 时不限制，应出 2 句，实际 %d", len(gotAll))
	}
	// minIdleDays=0 时也不过滤
	gotOff := BuildReviewSentences(bank, nil, arc, testToday, 10, nil, 0, nil, 0)
	if len(gotOff) != 2 {
		t.Errorf("minIdleDays=0 时不限制，应出 2 句，实际 %d", len(gotOff))
	}
}

// 圆级分层：三圆占大头（上限非保底），三圆不够由其他档补满，题量不缩水。
func TestBuildReviewSentencesCircleQuota(t *testing.T) {
	mk := func(tag, word string, i int) ReviewSentence {
		return ReviewSentence{ID: "R" + tag + string(rune('a'+i)), Word: word,
			Answer: tag + "の文" + word + "。", Prototype: "P" + word}
	}
	var ss []ReviewSentence
	circle := map[string]string{}
	for i := 0; i < 16; i++ {
		word := "c3_" + string(rune('a'+i))
		circle[word] = "三圆"
		ss = append(ss, mk("3", word, i))
	}
	for i := 0; i < 12; i++ {
		word := "c2_" + string(rune('a'+i))
		circle[word] = "二圆"
		ss = append(ss, mk("2", word, i))
	}
	bank := bankOf(ss...)
	// n=20, ratio=70 → 三圆 14 + 其他 6
	got := BuildReviewSentences(bank, nil, nil, testToday, 20, nil, 0, circle, 70)
	if len(got) != 20 {
		t.Fatalf("应出 20 句，实际 %d", len(got))
	}
	three := 0
	for _, g := range got {
		if len(g.Answer) > 0 && g.Answer[0] == '3' {
			three++
		}
	}
	if three != 14 {
		t.Errorf("70%% 配额应 14 句三圆，实际 %d", three)
	}
	// 三圆池子只有 3 条时，其余名额由其他档补满
	small := bankOf(ss[:3]...).Sentences
	_ = small
	short := bankOf(append([]ReviewSentence{}, ss[16:]...)...).Sentences // 只留二圆 12 条
	shortBank := bankOf(short...)
	gotShort := BuildReviewSentences(shortBank, nil, nil, testToday, 10, nil, 0, circle, 70)
	if len(gotShort) != 10 {
		t.Errorf("三圆不够时应补满 10 句，实际 %d", len(gotShort))
	}
}

// ---------- 类型 JSON 往返（序列化契约不变）----------

func TestReviewBankJSONRoundTrip(t *testing.T) {
	b := bankOf(ReviewSentence{
		ID: "R1", Word: "かな(仮名)", Pos: "名词", Sub: "", Lesson: "第1课",
		Source: "基本课文", SourceDoc: "第1课", Type: "transformed",
		Prototype: "原型", PrototypeID: "P1", Answer: "これは 仮名です。", Chinese: "这是假名。",
	})
	b.Note = "备注"
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var got ReviewBank
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Updated != "2026-11-03" || got.Note != "备注" || len(got.Sentences) != 1 {
		t.Fatalf("Bank 头字段往返丢失：%+v", got)
	}
	s := got.Sentences[0]
	if s.ID != "R1" || s.Word != "かな(仮名)" || s.Prototype != "原型" ||
		s.PrototypeID != "P1" || s.Answer != "これは 仮名です。" || s.Chinese != "这是假名。" {
		t.Errorf("Sentence 全字段往返丢失：%+v", s)
	}
}

// ---------- LoadReviewFile：整库对象 / 裸数组两种格式 ----------

func TestLoadReviewFileTwoFormats(t *testing.T) {
	dir := t.TempDir()
	obj := filepath.Join(dir, "bank.json")
	arr := filepath.Join(dir, "add.json")
	if err := os.WriteFile(obj, []byte(`{"version":2,"sentences":[{"id":"R1","answer":"文。"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(arr, []byte(`[{"id":"R2","answer":"文2。"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadReviewFile(obj)
	if err != nil {
		t.Fatal(err)
	}
	if b.Version != 2 || len(b.Sentences) != 1 || b.Sentences[0].ID != "R1" {
		t.Errorf("整库对象格式解析错：%+v", b)
	}
	b2, err := LoadReviewFile(arr)
	if err != nil {
		t.Fatal(err)
	}
	if b2.Version != 0 || len(b2.Sentences) != 1 || b2.Sentences[0].ID != "R2" {
		t.Errorf("裸数组格式解析错：%+v", b2)
	}
	if _, err := LoadReviewFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("读不存在的文件应返回 error")
	}
}

// ---------- NextReviewIDNum：空洞 / 非法 id / 条数保底 ----------

func TestNextReviewIDNum(t *testing.T) {
	if got := NextReviewIDNum(nil); got != 0 {
		t.Errorf("空列表应为 0，实际 %d", got)
	}
	cases := []struct {
		name string
		ss   []ReviewSentence
		want int
	}{
		{"连续", []ReviewSentence{{ID: "R1"}, {ID: "R2"}}, 2},
		{"有空洞序号更大", []ReviewSentence{{ID: "R1"}, {ID: "R308"}}, 308},
		{"条数比最大序号大", []ReviewSentence{{ID: "R1"}, {ID: ""}, {ID: ""}, {ID: ""}, {ID: ""}}, 5},
		{"非法id忽略", []ReviewSentence{{ID: "X9"}, {ID: "R-3"}, {ID: "R007"}}, 7},
		{"带空格", []ReviewSentence{{ID: "  R42  "}}, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NextReviewIDNum(c.ss); got != c.want {
				t.Errorf("NextReviewIDNum = %d, want %d", got, c.want)
			}
		})
	}
}
