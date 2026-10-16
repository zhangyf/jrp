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

// 不做覆盖：意思一样但句式不同的两句（帰らなければ / 帰らなくては）是两句，
// 都要留在句库里。2026-10-16 老师否掉了"很像就覆盖"的逻辑。
func TestBuildImportPlanNeverOverwritesSimilarSentence(t *testing.T) {
	rows := []importedRow{
		{JP: "李さんは 今日 早く 帰らなくては なりません。", CN: "小李今天得早点回家。"},
	}
	existing := []BankSentence{
		{ID: "L18B002", Lesson: "第18课", Source: "基本课文", Type: "original", Answer: "李さんは 今日 早く 帰らなければ なりません。"},
	}
	plan := buildImportPlan(rows, 18, lessonDocKinds[0], "x.md", existing)
	if len(plan.Added) != 1 {
		t.Fatalf("句式不同的另一句应作为新条目追加，实际 add=%d", len(plan.Added))
	}
	if plan.Added[0].Answer != "李さんは 今日 早く 帰らなくては なりません。" {
		t.Errorf("追加的内容不对：%q", plan.Added[0].Answer)
	}
	if plan.Added[0].ID == "L18B002" {
		t.Error("不该复用旧条目的 ID（那等于覆盖）")
	}
}

// 重复导入同一课不涨条目 —— 这是"入库前先查重"的底线。
func TestBuildImportPlanIsIdempotent(t *testing.T) {
	existing := []BankSentence{
		{ID: "L19B001", Lesson: "第19课", Source: "基本课文", Type: "original", Answer: "部屋の かぎを 忘れないで ください。"},
		{ID: "L19B002", Lesson: "第19课", Source: "基本课文", Type: "original", Answer: "李さんは 今日 早く 帰らなければ なりません。"},
	}
	rows := []importedRow{
		{JP: "部屋の かぎを 忘れないで ください。", CN: "请别忘了房间钥匙。"},
		{JP: "李さんは 今日 早く 帰らなければ なりません。", CN: "小李今天必须早点回家。"},
	}
	plan := buildImportPlan(rows, 19, lessonDocKinds[0], "x.md", existing)
	if len(plan.Added) != 0 || plan.Skipped != 2 {
		t.Errorf("整课重复导入应全部跳过：new=%d skip=%d", len(plan.Added), plan.Skipped)
	}
}

// save-lesson 自动入库走的就是这两个函数（resolveImportDocs + collectImport），
// 这里用内存文档跑一遍：查重只对"一模一样"生效，重复导不涨条目。
func TestResolveAndCollectImport(t *testing.T) {
	basic := "## 第19课 基本课文\n\n### 基本课文 4 句\n\n| 序号 | 日文 | 中文 |\n|---|---|---|\n" +
		"| 1 | 部屋の かぎを 忘れないで ください。 | 请别忘了房间钥匙。 |\n" +
		"| 2 | あの 人が 吉田課長ですよ。 | 那个人是吉田科长。 |\n"
	applied := "## 第19课 应用课文\n\n| 说话人 | 日文 | 中文 |\n|---|---|---|\n" +
		"| 李 | 小野さん、初めてですから、ちょっと 恐いです。 | 小野女士，我是第一次滑雪，有点害怕。 |\n"
	docs := map[string]string{
		lessonDocName(5, lessonDocKinds[0]): basic,
		lessonDocName(5, lessonDocKinds[1]): applied,
	}
	fetch := func(n string) (string, bool) { c, ok := docs[n]; return c, ok }

	existing := []BankSentence{
		{ID: "L19B001", Lesson: "第19课", Source: "基本课文", Type: "original", Answer: "部屋の かぎを 忘れないで ください。"},
	}
	unit, list, found := resolveImportDocs(fetch, 19, "")
	if !found || unit != 5 || len(list) != 2 {
		t.Fatalf("应定位到两份文档，实际 unit=%d found=%v docs=%d", unit, found, len(list))
	}

	all, added, reports := collectImport(fetch, existing, 19, list)
	if len(added) != 2 {
		t.Fatalf("3 句里 1 句重复 → 应只加 2 条，实际 %d（%+v）", len(added), reports)
	}
	if len(all) != 3 {
		t.Errorf("句库应从 1 条变 3 条，实际 %d", len(all))
	}

	again, added2, _ := collectImport(fetch, all, 19, list)
	if len(added2) != 0 || len(again) != 3 {
		t.Errorf("再导一次应 0 新增、条数不变，实际 new=%d total=%d", len(added2), len(again))
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
