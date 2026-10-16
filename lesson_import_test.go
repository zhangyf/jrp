package main

import (
	"strings"
	"testing"
)

const sampleBasicDoc = `# 课本原文汇编 · 第5单元（第17-20课）基本课文

## 第18课 基本课文

### 基本课文 4 句

| 序号 | 日文 | 中文 |
|---|---|---|
| 1 | 携帯電話は とても 小さく なりました。 | 手机变得很小了。 |

### 应用对话（甲/乙 4 组）

| 对话 | 说话人 | 日文 | 中文 |
|---|---|---|---|
| A | 甲 | 風邪は どうですか。 | 感冒怎么样了？ |
| A | 乙 | ゆうべ 薬を 飲みましたが、まだ よく なりません。 | 昨天晚上吃了药，可还没好。 |

## 第19课 基本课文

**页码**：标日初级上册 第226页。

### 基本课文 4 句

| 序号 | 日文 | 中文 |
|---|---|---|
| 1 | 部屋の かぎを 忘れないで ください。 | 请别忘了房间钥匙。 |
| 2 | 李さんは 今日 早く 帰らなければ なりません。 | 小李今天必须早点回家。 |
| 3 | 明日は 残業しなくても/ いいですよ。 | 明天不加班也可以。 |
| 4 | あの 人が 吉田課長ですよ。 | 那个人是吉田科长。 |

### 应用对话（甲/乙 4 组）

| 对话 | 说话人 | 日文 | 中文 |
|---|---|---|---|
| A | 甲 | その 品物に 触らないで ください。 | 请不要触摸那些商品。 |
| A | 乙 | あっ、すみません。 | 啊，对不起。 |

### 本课末尾词

| 日文 | 中文 |
|---|---|
| 初めて（第一次） | 第一次 |

## 第20课 基本课文

（待补 — 用户未提供第20课课本照片）

### 基本课文 4 句

| 序号 | 日文 | 中文 |
|---|---|---|
| 1 | 待补 | 待补 |
`

func TestUnitOfLesson(t *testing.T) {
	cases := map[int]int{1: 1, 4: 1, 5: 2, 12: 3, 17: 5, 19: 5, 20: 5, 21: 6, 0: 0, -3: 0}
	for lesson, want := range cases {
		if got := unitOfLesson(lesson); got != want {
			t.Errorf("unitOfLesson(%d) = %d, want %d", lesson, got, want)
		}
	}
}

func TestExtractLessonRowsPicksOnlyTargetLesson(t *testing.T) {
	rows := extractLessonRows(sampleBasicDoc, 19)
	if len(rows) != 6 {
		t.Fatalf("第19课应抽到 4+2=6 行，实际 %d：%+v", len(rows), rows)
	}
	if rows[0].JP != "部屋の かぎを 忘れないで ください。" {
		t.Errorf("首句不对：%q", rows[0].JP)
	}
	if rows[3].JP != "あの 人が 吉田課長ですよ。" {
		t.Errorf("第4句不对：%q", rows[3].JP)
	}
	if rows[5].JP != "あっ、すみません。" {
		t.Errorf("应用对话最后一句不对：%q", rows[5].JP)
	}
}

// 斜杠是汇编里的手误，入库前要抹平，否则句库存着别扭的原文。
func TestExtractLessonRowsCleansStraySlash(t *testing.T) {
	rows := extractLessonRows(sampleBasicDoc, 19)
	if rows[2].JP != "明日は 残業しなくても いいですよ。" {
		t.Errorf("斜杠没清掉：%q", rows[2].JP)
	}
}

func TestExtractLessonRowsSkipsVocabAndPlaceholder(t *testing.T) {
	rows := extractLessonRows(sampleBasicDoc, 19)
	for _, r := range rows {
		if strings.Contains(r.JP, "（") {
			t.Errorf("生词行混进来了：%q", r.JP)
		}
		if r.JP == "待补" {
			t.Errorf("占位行混进来了")
		}
	}
	// 第20课整课都是待补，不该抽出任何句子。
	if got := extractLessonRows(sampleBasicDoc, 20); len(got) != 0 {
		t.Errorf("待补课应抽 0 行，实际 %d：%+v", len(got), got)
	}
	// 不存在的课号
	if got := extractLessonRows(sampleBasicDoc, 99); len(got) != 0 {
		t.Errorf("不存在的课应抽 0 行，实际 %d", len(got))
	}
}

func TestExtractLessonRowsAcceptsThreeColumnDialogueTable(t *testing.T) {
	doc := `## 第19课 应用课文

### （在滑雪场）

| 说话人 | 日文 | 中文 |
|---|---|---|
| 李 | 小野さん、初めてですから、ちょっと 恐いです。 | 小野女士，我是第一次滑雪，有点害怕。 |
| 小野 | 心配しなくても いいですよ。 | 不用担心。 |
`
	rows := extractLessonRows(doc, 19)
	if len(rows) != 2 {
		t.Fatalf("三列表格应抽 2 行，实际 %d", len(rows))
	}
	if rows[1].CN != "不用担心。" {
		t.Errorf("中文列取错了：%q", rows[1].CN)
	}
}

// 生词表是 2 列「| 日文 | 中文 |」，且常常不在"生词"小节标题下 —— 靠列数挡住。
func TestTableColumnsRejectsTwoColumnVocabTable(t *testing.T) {
	jp, cn := tableColumns(splitCells("| 日文 | 中文 |"))
	if jp != -1 || cn != -1 {
		t.Errorf("两列生词表头不该被认成句子表：jp=%d cn=%d", jp, cn)
	}
	for _, h := range []string{"| 序号 | 日文 | 中文 |", "| 说话人 | 日文 | 中文 |", "| 对话 | 说话人 | 日文 | 中文 |"} {
		jp, cn := tableColumns(splitCells(h))
		if jp < 0 || cn < 0 {
			t.Errorf("表头 %q 应能定位日文/中文列", h)
		}
	}
}

func TestBuildImportPlanAddsWithNextID(t *testing.T) {
	rows := []importedRow{
		{JP: "部屋の かぎを 忘れないで ください。", CN: "请别忘了房间钥匙。"},
		{JP: "あの 人が 吉田課長ですよ。", CN: "那个人是吉田科长。"},
	}
	existing := []BankSentence{
		{ID: "L19B001", Lesson: "第19课", Source: "基本课文", Answer: "部屋の かぎを 忘れないで ください"},
		{ID: "L19B009", Lesson: "第19课", Source: "基本课文", Answer: "早く 帰ります。"},
	}
	plan := buildImportPlan(rows, 19, lessonDocKinds[0], "x.md", existing)
	if len(plan.Added) != 1 || plan.Skipped != 1 {
		t.Fatalf("应新增 1 条、跳过 1 条，实际 new=%d skip=%d", len(plan.Added), plan.Skipped)
	}
	// 已有最大序号 9 → 新条目从 010 开始，不能撞号。
	if plan.Added[0].ID != "L19B010" {
		t.Errorf("ID 应从已有最大号接着编，实际 %q", plan.Added[0].ID)
	}
	if plan.Added[0].Lesson != "第19课" || plan.Added[0].Source != "基本课文" || plan.Added[0].Type != "original" {
		t.Errorf("条目字段不对：%+v", plan.Added[0])
	}
	if plan.Added[0].SourceDoc != "x.md" {
		t.Errorf("source_doc 没写，sentence-lint 会报查不到出处")
	}
}

// 句号差异不算新句子，否则同一句会被录两遍。
func TestBuildImportPlanIgnoresPunctuationDiff(t *testing.T) {
	rows := []importedRow{{JP: "あの 人が 吉田課長ですよ", CN: "x"}}
	existing := []BankSentence{{ID: "L19B001", Lesson: "第19课", Source: "基本课文", Answer: "あの 人が 吉田課長ですよ。"}}
	plan := buildImportPlan(rows, 19, lessonDocKinds[0], "x.md", existing)
	if len(plan.Added) != 0 || plan.Skipped != 1 {
		t.Errorf("仅句号差异应判重复：new=%d skip=%d", len(plan.Added), plan.Skipped)
	}
}

func TestBuildImportPlanDedupesInsideDoc(t *testing.T) {
	rows := []importedRow{
		{JP: "あの 人が 吉田課長ですよ。", CN: "a"},
		{JP: "あの 人が 吉田課長ですよ。", CN: "b"},
	}
	plan := buildImportPlan(rows, 19, lessonDocKinds[0], "x.md", nil)
	if len(plan.Added) != 1 || plan.Skipped != 1 {
		t.Errorf("文档内重复应只留一条：new=%d skip=%d", len(plan.Added), plan.Skipped)
	}
}

// 文档改了原文 → 同位置那条原地覆盖，ID 不变，总数不涨。
// 这是"重复就覆盖"的关键：改过的旧版本不能留在句库里跟新版同时被练到。
func TestBuildImportPlanUpdatesInPlaceWhenTextChanged(t *testing.T) {
	rows := []importedRow{
		{JP: "テレビの 音を 大きく します。", CN: "把电视机的声音开大一点儿。"},
		{JP: "息子は 医者に なりました。", CN: "儿子当医生了。"},
	}
	existing := []BankSentence{
		{ID: "L18B002", Lesson: "第18课", Source: "基本课文", Type: "original", Answer: "テレビの 音を 大きく します"},
		{ID: "L18B003", Lesson: "第18课", Source: "基本课文", Type: "original", Answer: "息子は 医者に なりました"},
		{ID: "L18B004", Lesson: "第18课", Source: "基本课文", Type: "original", Answer: "部屋を きれいに して ください。"},
	}
	plan := buildImportPlan(rows, 18, lessonDocKinds[0], "x.md", existing)
	if len(plan.Added) != 0 {
		t.Errorf("不该新增，实际新增 %d 条", len(plan.Added))
	}
	if len(plan.Updated) != 0 {
		t.Fatalf("这两句归一化后已存在，应判重复而不是覆盖，实际覆盖 %d 条", len(plan.Updated))
	}
	if plan.Skipped != 2 {
		t.Errorf("应跳过 2 条，实际 %d", plan.Skipped)
	}

	// 真·改了内容的场景：第2条被改掉一个词
	rows[1] = importedRow{JP: "娘は 医者に なりました。", CN: "女儿当医生了。"}
	plan = buildImportPlan(rows, 18, lessonDocKinds[0], "x.md", existing)
	if len(plan.Added) != 0 || len(plan.Updated) != 1 {
		t.Fatalf("应原地覆盖 1 条、不新增，实际 add=%d upd=%d", len(plan.Added), len(plan.Updated))
	}
	u := plan.Updated[0]
	if u.ID != "L18B003" {
		t.Errorf("覆盖要保住原 ID，实际 %q", u.ID)
	}
	if u.Before != "息子は 医者に なりました" || u.After != "娘は 医者に なりました。" {
		t.Errorf("before/after 不对：%q → %q", u.Before, u.After)
	}
	if u.Entry.SourceDoc != "x.md" || u.Entry.Type != "original" {
		t.Errorf("覆盖后字段丢了：%+v", u.Entry)
	}
	// 文档里只剩 2 条，句库里同来源有 3 条 → 多出来的那条报 orphan（不自动删）
	if len(plan.Orphans) != 1 || plan.Orphans[0] != "L18B004" {
		t.Errorf("多出来的旧条目应报 orphan，实际 %v", plan.Orphans)
	}
}

// 相似度门槛是量出来的，不是拍的：改一个词约 0.41，同课不相关的两句普遍 <0.25。
func TestSentenceSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want bool // 是否应判为"同一句被改写"
	}{
		{"息子は医者になりました", "娘は医者になりました", true},
		{"テレビの音を大きくします", "テレビの音を小さくします", true},
		{"李さんは今日早く帰らなければなりません", "すみません今日は早く帰ってレポートを書かなければなりません", false},
		{"あの人が吉田課長ですよ", "早く帰ります", false},
		{"部屋をきれいにしてください", "テレビの音を大きくします", false},
	}
	for _, c := range cases {
		got := sentenceSimilarity(c.a, c.b)
		if got >= reviseThreshold != c.want {
			t.Errorf("similarity(%q,%q)=%.2f，门槛 %.2f，期望判为改写=%v",
				c.a, c.b, got, reviseThreshold, c.want)
		}
	}
}

// 变形句是回炉句库的数据，不能拿课本原文去盖。
func TestBuildImportPlanDoesNotOverwriteTransformed(t *testing.T) {
	rows := []importedRow{{JP: "娘は 医者に なりました。", CN: "女儿当医生了。"}}
	existing := []BankSentence{
		{ID: "L18B003", Lesson: "第18课", Source: "基本课文", Type: "transformed", Prototype: "L18B003", Answer: "息子は 医者に なりました"},
	}
	plan := buildImportPlan(rows, 18, lessonDocKinds[0], "x.md", existing)
	if len(plan.Updated) != 0 {
		t.Errorf("不该覆盖变形句，实际覆盖了 %d 条", len(plan.Updated))
	}
	if len(plan.Added) != 1 {
		t.Errorf("应作为新条目追加，实际 add=%d", len(plan.Added))
	}
}

// 单元与课的对应关系不统一：第2单元只有 5-6 课，第3单元塞了 7-12 课。
// 按 4 课一单元去推会把第7课指到第2单元（那里没第7课），必须按内容找。
func TestResolveLessonDocsFindsRealUnit(t *testing.T) {
	docs := map[string]string{
		lessonDocName(2, lessonDocKinds[0]): "## 第5课 基本课文\n\n## 第6课 基本课文\n",
		lessonDocName(3, lessonDocKinds[0]): "## 第7课 基本课文\n\n## 第8课 基本课文\n",
	}
	fetch := func(name string) (string, bool) {
		c, ok := docs[name]
		return c, ok
	}
	unit, basic, applied, ok := resolveLessonDocs(fetch, 7)
	if !ok {
		t.Fatal("第7课应能定位到文档")
	}
	if unit != 3 {
		t.Errorf("第7课应在第3单元，实际第%d单元", unit)
	}
	if basic != lessonDocName(3, lessonDocKinds[0]) || applied != lessonDocName(3, lessonDocKinds[1]) {
		t.Errorf("文档名不对：%q / %q", basic, applied)
	}
	if _, _, _, ok := resolveLessonDocs(fetch, 99); ok {
		t.Error("不存在的课不该定位成功")
	}
}

func TestResolveLessonDocsAcceptsEmptySection(t *testing.T) {
	docs := map[string]string{
		lessonDocName(5, lessonDocKinds[0]): "## 第18课 基本课文\n\n（待补）\n",
	}
	fetch := func(name string) (string, bool) { c, ok := docs[name]; return c, ok }
	unit, _, _, ok := resolveLessonDocs(fetch, 18)
	if !ok || unit != 5 {
		t.Errorf("段落是待补也应定位到（由调用方看句子数判断空壳），unit=%d ok=%v", unit, ok)
	}
}

func TestHasLessonSection(t *testing.T) {
	if !hasLessonSection(sampleBasicDoc, 19) {
		t.Error("第19课段落应存在")
	}
	if hasLessonSection(sampleBasicDoc, 25) {
		t.Error("第25课不该存在")
	}
}

func TestLessonSectionsIn(t *testing.T) {
	got := lessonSectionsIn(sampleBasicDoc)
	want := []int{18, 19, 20}
	if len(got) != len(want) {
		t.Fatalf("课号列表 %v != %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("课号列表 %v != %v", got, want)
		}
	}
	if got := lessonSectionsIn("# 没有课号的文档\n\n| 1 | a | b |\n"); len(got) != 0 {
		t.Errorf("没有 ## 第N课 的文档应返回空，实际 %v", got)
	}
}

func TestLessonDocName(t *testing.T) {
	if got := lessonDocName(5, lessonDocKinds[0]); got != "课本原文汇编_第5单元（基本课文）.md" {
		t.Errorf("文档名不对：%q", got)
	}
	if got := lessonDocName(5, lessonDocKinds[1]); got != "课本原文汇编_第5单元（应用课文）.md" {
		t.Errorf("文档名不对：%q", got)
	}
}
