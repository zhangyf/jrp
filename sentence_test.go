package main

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return d
}

// bankWith 造一个每课 n 句的假句库。
func bankWith(lessons []string, n int) *SentenceBank {
	b := &SentenceBank{Version: 1}
	for _, ls := range lessons {
		for i := 0; i < n; i++ {
			b.Sentences = append(b.Sentences, BankSentence{
				ID:      ls + "-" + string(rune('a'+i)),
				Lesson:  ls,
				Type:    "original",
				Answer:  "文" + ls + string(rune('a'+i)) + "です。",
				Chinese: "译文" + ls,
			})
		}
	}
	return b
}

func answers(ps []PlanSentence) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Answer)
	}
	return out
}

func TestBuildSentencePlanSpreadsAcrossLessons(t *testing.T) {
	bank := bankWith([]string{"第1课", "第2课", "第3课", "第4课"}, 10)
	today := mustDate(t, "2026-09-20")

	got := BuildSentencePlan(bank, SentenceHistory{}, &SentenceWrong{}, today, 12)
	if len(got) != 12 {
		t.Fatalf("got %d sentences, want 12", len(got))
	}

	// 跨课摊平：每课都应该出现，且份额接近
	per := map[string]int{}
	for _, p := range got {
		for _, s := range bank.Sentences {
			if s.Answer == p.Answer {
				per[s.Lesson]++
			}
		}
	}
	if len(per) != 4 {
		t.Errorf("只覆盖了 %d 课，want 4：%v", len(per), per)
	}
	for ls, c := range per {
		if c != 3 {
			t.Errorf("第 %s 课出了 %d 句，want 3", ls, c)
		}
	}

	// 序号连续
	for i, p := range got {
		if p.Number != i+1 {
			t.Errorf("序号不连续：%d != %d", p.Number, i+1)
		}
	}
}

func TestBuildSentencePlanAvoidsRecentSevenDays(t *testing.T) {
	bank := bankWith([]string{"第1课"}, 30)
	today := mustDate(t, "2026-09-20")

	// 近 7 天内出过前 10 句
	hist := SentenceHistory{}
	for i := 0; i < 10; i++ {
		hist["2026-09-18"] = append(hist["2026-09-18"], bank.Sentences[i].Answer)
	}

	got := BuildSentencePlan(bank, hist, &SentenceWrong{}, today, 10)
	for _, a := range answers(got) {
		for i := 0; i < 10; i++ {
			if a == bank.Sentences[i].Answer {
				t.Errorf("近 7 天出过的句子又出现了：%s", a)
			}
		}
	}

	// 归一化后相同也算重复（带句号 vs 不带句号）
	// 池 40 句只要 30 句，够避开被 ban 的那句，不该触发放宽复用
	bigBank := bankWith([]string{"第1课"}, 40)
	hist2 := SentenceHistory{"2026-09-15": {"文第1课aです"}}
	got2 := BuildSentencePlan(bigBank, hist2, &SentenceWrong{}, today, 30)
	for _, a := range answers(got2) {
		if normSentence(a) == normSentence("文第1课aです") {
			t.Errorf("归一化后重复的句子又出现了：%s", a)
		}
	}
}

func TestBuildSentencePlanForcesWrongSentences(t *testing.T) {
	bank := bankWith([]string{"第1课"}, 30)
	today := mustDate(t, "2026-09-20")

	wrong := &SentenceWrong{Sentences: []WrongSentence{
		{Answer: "错句甲です。", Chinese: "甲", Status: "open", NextDue: "2026-09-20"},
		{Answer: "错句乙です。", Chinese: "乙", Status: "open", NextDue: "2026-09-18"}, // 逾期
		{Answer: "错句丙です。", Chinese: "丙", Status: "open", NextDue: "2026-09-25"}, // 还没到期
		{Answer: "已归档です。", Chinese: "丁", Status: "archived", NextDue: "2026-09-01"},
	}}

	got := answers(BuildSentencePlan(bank, SentenceHistory{}, wrong, today, 10))

	has := func(s string) bool {
		for _, a := range got {
			if a == s {
				return true
			}
		}
		return false
	}
	if !has("错句甲です。") {
		t.Error("今天到期的错句没被强制插入")
	}
	if !has("错句乙です。") {
		t.Error("逾期的错句没被强制插入")
	}
	if has("错句丙です。") {
		t.Error("还没到期的错句不该出现")
	}
	if has("已归档です。") {
		t.Error("已归档的错句不该出现")
	}

	// 错句不受「近 7 天不重复」限制
	hist := SentenceHistory{"2026-09-19": {"错句甲です。"}}
	got2 := answers(BuildSentencePlan(bank, hist, wrong, today, 10))
	if !has2(got2, "错句甲です。") {
		t.Error("错句应优先于 7 天去重规则")
	}
}

func has2(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestBuildSentencePlanTransformedQuota(t *testing.T) {
	bank := bankWith([]string{"第1课"}, 40)
	for i := 0; i < 10; i++ {
		bank.Sentences = append(bank.Sentences, BankSentence{
			ID:        "T" + string(rune('a'+i)),
			Lesson:    "第1课",
			Type:      "transformed",
			Prototype: "L1-B001",
			Answer:    "变形" + string(rune('a'+i)) + "です。",
			Chinese:   "【变形】译文",
		})
	}
	today := mustDate(t, "2026-09-20")

	got := BuildSentencePlan(bank, SentenceHistory{}, &SentenceWrong{}, today, 20)
	n := 0
	for _, p := range got {
		for _, s := range bank.Sentences {
			if s.Answer == p.Answer && s.Type == "transformed" {
				n++
			}
		}
	}
	if n != 4 {
		t.Errorf("20 句里变形句 %d 句，want 4", n)
	}

	got = BuildSentencePlan(bank, SentenceHistory{}, &SentenceWrong{}, today, 50)
	n = 0
	for _, p := range got {
		for _, s := range bank.Sentences {
			if s.Answer == p.Answer && s.Type == "transformed" {
				n++
			}
		}
	}
	if n != 10 {
		t.Errorf("50 句里变形句 %d 句，want 10", n)
	}
}

func TestBuildSentencePlanReusesWhenPoolSmall(t *testing.T) {
	// 池只有 3 句，但要 10 句 —— 必须放宽复用而不是只给 3 句
	bank := bankWith([]string{"第1课"}, 3)
	today := mustDate(t, "2026-09-20")
	hist := SentenceHistory{"2026-09-19": {
		bank.Sentences[0].Answer, bank.Sentences[1].Answer, bank.Sentences[2].Answer,
	}}

	got := BuildSentencePlan(bank, hist, &SentenceWrong{}, today, 3)
	if len(got) != 3 {
		t.Errorf("句池不足时应复用补足，got %d want 3", len(got))
	}
}

func TestApplySentenceState(t *testing.T) {
	wrong := &SentenceWrong{Sentences: []WrongSentence{
		{Answer: "甲です。", Chinese: "甲", Status: "open", NextDue: "2026-09-20", WrongCount: 1},
		{Answer: "乙です。", Chinese: "乙", Status: "open", NextDue: "2026-09-18", WrongCount: 2},
		{Answer: "丙です。", Chinese: "丙", Status: "open", NextDue: "2026-09-15", WrongCount: 5},
		{Answer: "丁です。", Chinese: "丁", Status: "open", NextDue: "2026-09-19", WrongCount: 1},
	}}
	hist := SentenceHistory{}

	results := []SentenceResult{
		{Number: 1, Correct: false, Answer: "甲です。"},                 // count 1 -> 2 -> +7
		{Number: 2, Correct: false, Answer: "乙です。"},                 // count 2 -> 3 -> +14
		{Number: 3, Correct: false, Answer: "丙です。"},                 // count 5 -> 6 -> +14
		{Number: 4, Correct: true, Answer: "丁です。"},                  // -> archived
		{Number: 5, Correct: false, Answer: "新错句です。", Chinese: "戊"}, // 追加
	}

	w2, h2 := ApplySentenceState(wrong, hist, results, "2026-09-20")

	byAns := map[string]WrongSentence{}
	for _, w := range w2.Sentences {
		byAns[w.Answer] = w
	}

	if got := byAns["甲です。"].NextDue; got != "2026-09-27" {
		t.Errorf("甲 next_due = %s, want 2026-09-27（count=2 → +7）", got)
	}
	if got := byAns["乙です。"].NextDue; got != "2026-10-04" {
		t.Errorf("乙 next_due = %s, want 2026-10-04（count=3 → +14）", got)
	}
	if got := byAns["丙です。"].NextDue; got != "2026-10-04" {
		t.Errorf("丙 next_due = %s, want 2026-10-04（count>=3 → +14）", got)
	}
	if got := byAns["丁です。"].Status; got != "archived" {
		t.Errorf("丁 status = %s, want archived", got)
	}
	ns, ok := byAns["新错句です。"]
	if !ok {
		t.Fatal("新错句没被追加")
	}
	if ns.WrongCount != 1 || ns.NextDue != "2026-09-23" {
		t.Errorf("新错句 = %+v, want count=1 next_due=2026-09-23", ns)
	}
	if ns.Chinese != "戊" {
		t.Errorf("新错句 chinese = %q, want 戊", ns.Chinese)
	}

	if len(h2["2026-09-20"]) != 5 {
		t.Errorf("history 当天应记 5 句，got %d", len(h2["2026-09-20"]))
	}
}

func TestPruneSentenceHistory(t *testing.T) {
	h := SentenceHistory{
		"2026-09-20": {"a"},
		"2026-08-01": {"b"}, // 50 天前
		"2026-08-25": {"c"}, // 26 天前，保留
		"格式不对":       {"d"},
	}
	pruneSentenceHistory(h, "2026-09-20", 30)

	if _, ok := h["2026-08-01"]; ok {
		t.Error("30 天前的记录没被清理")
	}
	if _, ok := h["2026-08-25"]; !ok {
		t.Error("26 天前的记录不该被清理")
	}
	if _, ok := h["格式不对"]; ok {
		t.Error("日期格式不对的记录应被清理")
	}
}

func TestNormSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"李さんは 毎日 コーヒーを 飲みます。", "李さんは毎日コーヒーを飲みます"},
		{"李さんは毎日コーヒーを飲みます", "李さんは毎日コーヒーを飲みます"},
		{"ガレージに車が5台あります。", "ガレージに車が5台あります"},
		{"　全角　空格　", "全角空格"},
		// 全角数字/字母/标点折成半角（2026-09-21：老师 IME 打「２万円」被判错）
		{"修理はどのぐらいかかりますか。２万円ぐらいかかります", "修理はどのぐらいかかりますか2万円ぐらいかかります"},
		{"ＡＢＣ１２３", "ABC123"},
		{"！？：；", ""},   // 全角标点折叠后进入删除集
		{"甲！乙？丙", "甲乙丙"}, // 与删除集联动
	}
	for _, c := range cases {
		if got := normSentence(c.in); got != c.want {
			t.Errorf("normSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 当天这批提交之后，刷新会拿到全新的一批 —— 这是既有行为，不是 bug：
// 提交时 ApplySentenceState 把当天出过的句子记进 history，近 7 天去重会把它们
// 全部挡掉，于是从剩下的句池里再挑 20 句。写测试固定住，别哪天悄悄变了。
func TestSentencePlanRefreshesAfterCommit(t *testing.T) {
	bank := bankWith([]string{"第1课", "第2课", "第3课", "第4课"}, 10) // 共 40 句
	today := mustDate(t, "2026-09-20")
	dateStr := "2026-09-20"

	first := BuildSentencePlan(bank, SentenceHistory{}, &SentenceWrong{}, today, 20)
	if len(first) != 20 {
		t.Fatalf("first batch = %d, want 20", len(first))
	}

	// 未提交只刷新 → 完全相同的一批（纯函数，无随机）
	again := BuildSentencePlan(bank, SentenceHistory{}, &SentenceWrong{}, today, 20)
	for i := range first {
		if i >= len(again) || first[i].Answer != again[i].Answer {
			t.Fatalf("同一天未提交时刷新应返回同一批，第 %d 句变了", i+1)
		}
	}

	// 模拟全部答对提交
	var rs []SentenceResult
	for _, p := range first {
		rs = append(rs, SentenceResult{Number: p.Number, Correct: true, Answer: p.Answer, Chinese: p.Chinese})
	}
	wrong, hist := ApplySentenceState(&SentenceWrong{}, SentenceHistory{}, rs, dateStr)

	second := BuildSentencePlan(bank, hist, wrong, today, 20)
	if len(second) != 20 {
		t.Fatalf("second batch = %d, want 20（句池应还剩 20 句）", len(second))
	}

	seen := map[string]bool{}
	for _, p := range first {
		seen[normSentence(p.Answer)] = true
	}
	for _, p := range second {
		if seen[normSentence(p.Answer)] {
			t.Fatalf("提交后刷新又出到了当天做过的句子：%q", p.Answer)
		}
	}
}

// 句池被 7 天去重榨干后，fill(false) 会放宽复用近 7 天出过的句子（最久没出过优先），
// 保证永远凑得满 20 句 —— 否则老师会看到空白页。
func TestSentencePlanReusesWhenPoolExhausted(t *testing.T) {
	bank := bankWith([]string{"第1课", "第2课"}, 10) // 共 20 句，一次就出完
	today := mustDate(t, "2026-09-20")

	first := BuildSentencePlan(bank, SentenceHistory{}, &SentenceWrong{}, today, 20)
	var rs []SentenceResult
	for _, p := range first {
		rs = append(rs, SentenceResult{Number: p.Number, Correct: true, Answer: p.Answer, Chinese: p.Chinese})
	}
	wrong, hist := ApplySentenceState(&SentenceWrong{}, SentenceHistory{}, rs, "2026-09-20")

	second := BuildSentencePlan(bank, hist, wrong, today, 20)
	if len(second) != 20 {
		t.Fatalf("句池耗尽时应放宽复用凑满 20 句，实际 %d", len(second))
	}
}

// 造句「不提交就不换题」的判定。换题只能发生在回写成功之后
// （api_record.go 里 DeleteSentencePlan 清掉当天 plan），在此之前
// 无论刷新多少次、换什么设备，都必须复用同一批。
func TestSentencePlanLocked(t *testing.T) {
	today := "2026-09-21"
	withSentences := &ReviewPlan{Date: today, Kind: "sentences",
		Sentences: []PlanSentence{{Number: 1, Answer: "あ"}}}

	cases := []struct {
		name string
		old  *ReviewPlan
		date string
		want bool
	}{
		{"当天已有 plan → 复用（不换题）", withSentences, today, true},
		{"对象不存在（下载失败）→ 重新出题", nil, today, false},
		{"空 plan → 重新出题", &ReviewPlan{Date: today, Kind: "sentences"}, today, false},
		{"日期对不上（陈年 plan）→ 重新出题",
			&ReviewPlan{Date: "2026-09-01", Kind: "sentences",
				Sentences: []PlanSentence{{Number: 1, Answer: "あ"}}}, today, false},
	}
	for _, c := range cases {
		if got := sentencePlanLocked(c.old, c.date); got != c.want {
			t.Errorf("%s: sentencePlanLocked() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSortLessons(t *testing.T) {
	ls := []string{"第10课", "第2课", "第1课", "单元末"}
	sortLessons(ls)
	// 单元末没有数字，排最后
	if ls[0] != "第1课" || ls[1] != "第2课" || ls[2] != "第10课" {
		t.Errorf("课程排序错误（应按数字而非字符串）：%v", ls)
	}
	if ls[3] != "单元末" {
		t.Errorf("无数字的课名应排最后：%v", ls)
	}
}
