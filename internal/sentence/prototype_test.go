package sentence

import (
	"testing"
)

// ---------- GroupLesson / LessonLabel ----------

func TestGroupLessonAndLabel(t *testing.T) {
	cases := []struct {
		group string
		want  int
	}{
		{"第1课01 基础词（5/25）", 1},
		{"第13课01 基础词（10/25）", 13},
		{"第3课02 应用词（8/12）", 3},
		{"单元末", 0},
		{"复习2026-09-21", 0},
		{"第课", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := GroupLesson(c.group); got != c.want {
			t.Errorf("GroupLesson(%q) = %d, want %d", c.group, got, c.want)
		}
	}
	if LessonLabel(0) != "" || LessonLabel(-1) != "" {
		t.Error("课号 <=0 时应返回空串")
	}
	if got := LessonLabel(21); got != "第21课" {
		t.Errorf("LessonLabel(21) = %q", got)
	}
}

func TestRoundAcc(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0.9166666666666666, 0.92},
		{1, 1},
		{0, 0},
		{0.005, 0.01},
		{0.004, 0},
		{12.345, 12.35},
	}
	for _, c := range cases {
		if got := RoundAcc(c.in); got != c.want {
			t.Errorf("RoundAcc(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestJpLen(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"これは 本です。", 7},
		{"　全角　空格 ", 4}, // 去全角/半角空格后「全角空格」4 字
		{"ab\tcd", 4},
		{"", 0},
		{"あいう", 3},
	}
	for _, c := range cases {
		if got := JpLen(c.in); got != c.want {
			t.Errorf("JpLen(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ---------- ProtoScore：长度三档 + 各词性槽位 + 基本课文加分 ----------

func TestProtoScoreLengthBands(t *testing.T) {
	short := BankSentence{Answer: "はい。"}               // <10 字
	mid := BankSentence{Answer: "これは とても 長い 文で あります。"} // 14-26 字
	long := BankSentence{Answer: "これは 本当に とても とても 長くて 助詞が たくさん 並んで いる 文です。"}
	// 纯长度（pos 不给 → 不进任何词性分支）：short 应被重扣、mid 应加分、long 轻扣
	if !(ProtoScore(mid, "") > ProtoScore(long, "") && ProtoScore(long, "") > ProtoScore(short, "")) {
		t.Errorf("长度档位顺序错：short=%d mid=%d long=%d",
			ProtoScore(short, ""), ProtoScore(mid, ""), ProtoScore(long, ""))
	}
	if ProtoScore(short, "") != -30 {
		t.Errorf("短句应为 -30，实际 %d", ProtoScore(short, ""))
	}
	if ProtoScore(mid, "") != 15 {
		t.Errorf("中长句应为 +15，实际 %d", ProtoScore(mid, ""))
	}
	if ProtoScore(long, "") != -6 {
		t.Errorf("超长句应为 -6，实际 %d", ProtoScore(long, ""))
	}
}

func TestProtoScorePosAndSource(t *testing.T) {
	base := BankSentence{Answer: "李さんは 毎日 コーヒーを 飲みます。"}
	// 名词槽：含「は 」「を 」「に 」等多个助词，应累计加分
	if ProtoScore(base, "名词") <= ProtoScore(base, "") {
		t.Errorf("名词应因槽位加分：%d vs %d", ProtoScore(base, "名词"), ProtoScore(base, ""))
	}
	masu := BankSentence{Answer: "李さんは 毎日 コーヒーを 飲みます。"}
	desu := BankSentence{Answer: "これは 本です。"}
	if ProtoScore(masu, "动词") <= ProtoScore(desu, "动词") {
		t.Errorf("动词应优先ます结尾：%d vs %d", ProtoScore(masu, "动词"), ProtoScore(desu, "动词"))
	}
	iAdj := BankSentence{Answer: "四川料理は 辛いです。"}
	nAdj := BankSentence{Answer: "これは 本です。"}
	if ProtoScore(iAdj, "形容词") <= ProtoScore(nAdj, "形容词") {
		t.Errorf("形容词应优先い形：%d vs %d", ProtoScore(iAdj, "形容词"), ProtoScore(nAdj, "形容词"))
	}
	withSource := base
	withSource.Source = "基本课文"
	if ProtoScore(withSource, "") != ProtoScore(base, "")+5 {
		t.Errorf("基本课文应 +5：%d vs %d", ProtoScore(withSource, ""), ProtoScore(base, ""))
	}
}

// ---------- GroupProtosByLesson：分组 + 基本课文优先 + 长句优先 ----------

func TestGroupProtosByLesson(t *testing.T) {
	ss := []BankSentence{
		{ID: "A1", Lesson: "第3课", Source: "应用课文", Answer: "とても 長い アプリの 文です。"},
		{ID: "B1", Lesson: "第3课", Source: "基本课文", Answer: "ここは デパートです。"},
		{ID: "B2", Lesson: "第3课", Source: "基本课文", Answer: "トイレは どこに ありますか。"},
		{ID: "C1", Lesson: "第5课", Source: "基本课文", Answer: "今 4時です。"},
		{ID: "D1", Lesson: "单元末", Source: "阅读文", Answer: "今日は 金曜日です。"},
		{ID: "E1", Lesson: "第3课", Source: "基本课文", Answer: ""}, // 空 answer 跳过
	}
	by := GroupProtosByLesson(ss)
	if len(by[3]) != 3 || len(by[5]) != 1 || len(by[0]) != 1 {
		t.Fatalf("分组不对：%v", by)
	}
	// 第3课：基本课文在前，两者中长句（B2）在前，最后应用课文 A1
	if by[3][0].ID != "B2" || by[3][1].ID != "B1" || by[3][2].ID != "A1" {
		t.Errorf("第3课排序不对：%+v", by[3])
	}
}

// ---------- PickPrototypes：单词与句式解绑、词性匹配优先、长句优先、n<=0 ----------

func TestPickPrototypes(t *testing.T) {
	// 解绑核心回归：第 1 课的词，也要能挑到高课的复杂长句，而不是被绑在第 1 课的短句上。
	ss := []BankSentence{
		{ID: "short1", Lesson: "第1课", Source: "基本课文", Answer: "これは 本です。"},
		{ID: "long18", Lesson: "第18课", Source: "基本课文", Answer: "李さんは 毎日 日本語で 手紙を 書きます。"},
	}
	by := GroupProtosByLesson(ss)
	got := PickPrototypes(by, 1, 2, "名词")
	if len(got) != 2 {
		t.Fatalf("要 2 条给 %d 条", len(got))
	}
	// 长句（14-26字）应排短句前面，即便长句在别的课
	if got[0].ID != "long18" {
		t.Errorf("解绑后应按长句优先挑高课句，实际 %s", got[0].ID)
	}
	// 课号未知：全库按打分兜底给满
	gotUnknown := PickPrototypes(by, 0, 2, "名词")
	if len(gotUnknown) != 2 {
		t.Errorf("课号未知应给满 2 条，实际 %d", len(gotUnknown))
	}
	if gotUnknown[0].ID != "long18" {
		t.Errorf("课号未知时也应按长句优先，实际 %s", gotUnknown[0].ID)
	}
	// 同分时同课的略优先（tie-break，不再是起始位置的决定因素）
	ss2 := []BankSentence{
		{ID: "same1", Lesson: "第5课", Answer: "これは 机です。"},
		{ID: "same2", Lesson: "第9课", Answer: "あれは 椅子です。"},
	}
	by2 := GroupProtosByLesson(ss2)
	gotTie := PickPrototypes(by2, 5, 1, "名词")
	if len(gotTie) != 1 {
		t.Fatalf("要 1 条给 %d 条", len(gotTie))
	}
	if gotTie[0].ID != "same1" {
		t.Errorf("同分时应同课优先，实际 %s", gotTie[0].ID)
	}
	// n<=0
	if PickPrototypes(by, 4, 0, "名词") != nil {
		t.Error("n=0 应返回 nil")
	}
	if PickPrototypes(by, 4, -1, "名词") != nil {
		t.Error("n<0 应返回 nil")
	}
	// 空库
	if PickPrototypes(map[int][]BankSentence{}, 4, 3, "名词") != nil {
		t.Error("空库应返回 nil")
	}
}

// ---------- ProtoList：全字段映射 ----------

func TestProtoList(t *testing.T) {
	in := []BankSentence{
		{ID: "P1", Lesson: "第4课", Source: "基本课文", Answer: "文です。", Chinese: "是句子。"},
	}
	got := ProtoList(in)
	if len(got) != 1 {
		t.Fatalf("应 1 条，实际 %d", len(got))
	}
	if got[0].ID != "P1" || got[0].Lesson != "第4课" || got[0].Source != "基本课文" ||
		got[0].Answer != "文です。" || got[0].Chinese != "是句子。" {
		t.Errorf("字段映射丢失：%+v", got[0])
	}
	if got := ProtoList(nil); len(got) != 0 {
		t.Errorf("nil 输入应返回空切片，实际 %v", got)
	}
}
