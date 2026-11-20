// api_practice_test.go —— 练习主链测试：判分 / 记录 / 草稿 / 知识库 / 词条元数据（配 api_practice.go、api_data.go）
//
// 腿一收尾：把原本平铺在根目录的 5 个 *_test.go 按被测域物理合并到本文件。
// 测试函数体逐字保留，仅对 import 取并集去重；被测行为、断言、测试名均不变。
// 每个分区以「原文件名」标注，便于定位；文件头来源清单列出各源顶层声明。
//
// 来源与顶层声明：
// api_grade_test.go: TestGradeAPI
// api_record_test.go: TestPruneSentencePlanPartial, TestPruneSentencePlanAll, TestPruneSentencePlanTwice, TestApplyRecordReviewBoundary
// draft_test.go: TestDraftKey, TestDraftHasContent, TestValidDraftMode
// api_knowledge_test.go: knowledgeTestServer, TestKnowledgeListSortedByLessonNumber, TestKnowledgeAnonymousIsPartialButSharedTokenIsFull, TestKnowledgeUnknownLessonIs404
// api_wordmeta_test.go: TestLookupWordMeta, TestEnrichWordMetaCarriesCircle, TestWordPOSNoteRoundTrip
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/words"
)

// ==========================================================================
// 原 api_grade_test.go
// ==========================================================================

// TestGradeAPI：小程序走 /api/grade 判分，口径必须跟 Go 的 GradeAnswer 一致
// （web 端是浏览器里用 web/grade.js 本地判，两边靠同一套规则对齐）。
func TestGradeAPI(t *testing.T) {
	srv := &server{storage: &Storage{dryRun: true}, lang: "ja"}

	post := func(body string) map[string]interface{} {
		req := httptest.NewRequest(http.MethodPost, "/api/grade", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.withUser(srv.handleGrade)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		var out map[string]interface{}
		if err := jsonUnmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析返回失败：%v", err)
		}
		return out
	}

	// 任一档：写假名或写汉字都算对
	out := post(`{"mode":"either","items":[
		{"word":"すし(寿司)","answer":"すし"},
		{"word":"すし(寿司)","answer":"寿司"},
		{"word":"すし(寿司)","answer":"すし(寿司)"},
		{"word":"すし(寿司)","answer":"さかな"},
		{"word":"すし(寿司)","answer":""}
	]}`)
	got := out["results"].([]interface{})
	want := []bool{true, true, true, false, false}
	if len(got) != len(want) {
		t.Fatalf("结果条数 %d，要 %d", len(got), len(want))
	}
	for i := range want {
		if got[i].(bool) != want[i] {
			t.Errorf("第 %d 题：got %v want %v", i+1, got[i], want[i])
		}
	}
	if out["mode"] != "either" {
		t.Errorf("mode 回显 %v", out["mode"])
	}

	// 假名档（默认）：只写汉字不算对
	out = post(`{"items":[{"word":"すし(寿司)","answer":"寿司"}]}`)
	if out["results"].([]interface{})[0].(bool) {
		t.Error("假名档下只写汉字不该判对")
	}
	if out["mode"] != "kana" {
		t.Errorf("不传 mode 应回落 kana，实际 %v", out["mode"])
	}

	// 空 items 不能 panic
	out = post(`{"mode":"either","items":[]}`)
	if _, ok := out["results"]; !ok {
		t.Error("空 items 也该返回 results 字段")
	}

	// GET 要被拒
	req := httptest.NewRequest(http.MethodGet, "/api/grade", nil)
	w := httptest.NewRecorder()
	srv.withUser(srv.handleGrade)(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 应 405，实际 %d", w.Code)
	}
}

// ==========================================================================
// 原 api_record_test.go
// ==========================================================================

// 造句部分提交：没交的句子必须留在 plan 里（刷新后还在），
// 已交的摘掉；全部交完时 plan 清空（上层据此删 plan 换新的一批）。
// 2026-10-08 之前是只要回写就整批删 plan，部分提交后刷新，没写的句子就没了。
func TestPruneSentencePlanPartial(t *testing.T) {
	p := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
		{Number: 3, Answer: "c"},
		{Number: 4, Answer: "d"},
	}}
	left := plan.PruneSentencePlan(p, []sentence.SentenceResult{
		{Number: 1, Correct: true},
		{Number: 3, Correct: false},
	})
	if left != 2 {
		t.Fatalf("left = %d, want 2", left)
	}
	if p.Sentences[0].Number != 2 || p.Sentences[1].Number != 4 {
		t.Fatalf("剩下的应是 #2 #4（保持原编号）: %+v", p.Sentences)
	}
}

// 全部交完 → plan 清空，上层删 plan。
func TestPruneSentencePlanAll(t *testing.T) {
	p := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
	}}
	left := plan.PruneSentencePlan(p, []sentence.SentenceResult{
		{Number: 1, Correct: true},
		{Number: 2, Correct: false},
	})
	if left != 0 || len(p.Sentences) != 0 {
		t.Fatalf("全交完应清空: left=%d p=%+v", left, p.Sentences)
	}
}

// 分两次提交同一批：第二次提交时 plan 已是第一次裁剪过的，
// 号码不重叠的提交不该误删剩下的句子。
func TestPruneSentencePlanTwice(t *testing.T) {
	p := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
		{Number: 3, Answer: "c"},
	}}
	if l := plan.PruneSentencePlan(p, []sentence.SentenceResult{{Number: 1, Correct: true}}); l != 2 {
		t.Fatalf("第一次后应剩 2: %d", l)
	}
	if l := plan.PruneSentencePlan(p, []sentence.SentenceResult{{Number: 2, Correct: true}}); l != 1 {
		t.Fatalf("第二次后应剩 1: %d", l)
	}
	if p.Sentences[0].Number != 3 {
		t.Fatalf("最后应只剩 #3: %+v", p.Sentences)
	}
}

func TestApplyRecordReviewBoundary(t *testing.T) {
	const oldFilename = "日语学习进度档案_200102_v3.4.md"
	const oldHistoryRow = "|200102|v3.4|3|0|2|0|1|1|1|固定旧历史|"
	const oldArchive = "## \U0001f4cb 版本历史\n\n" +
		"|日期|版本|总词数|已掌握|基本掌握|待巩固|待测试|累计答错|钉子户|变化内容|\n" +
		"|---|---|---|---|---|---|---|---|---|---|\n" + oldHistoryRow + "\n\n" +
		"# 日语单词学习进度档案\n最后更新：01/02\n\n" +
		"## \U0001f4c5 单词列表（按课分组）\n\n" +
		"### 第2课\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|はし(橋)|桥|6|1|2|01/01|\U0001f7e1基本掌握|\n" +
		"|なつ(夏)|夏天|0|0|0||\U0001f504待测试|\n\n" +
		"### 第1课\n\n" +
		"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
		"|---|---|---|---|---|---|---|\n" +
		"|みず(水)|水|1|0|1|12/31|\U0001f7e1基本掌握|\n"
	const dailyPlan = `{"date":"2020-01-03","language":"ja","words":[
		{"number":41,"word":"みず(水)"},
		{"number":7,"word":"はし(橋)"},
		{"number":23,"word":"なつ(夏)"},
		{"number":88,"word":"档案不存在的词"}],"sentences":[]}`
	const hardPlan = `{"date":"2020-01-03","language":"ja","kind":"hard","words":[
		{"number":23,"word":"なつ(夏)"},
		{"number":7,"word":"みず(水)"},
		{"number":88,"word":"档案不存在的词"},
		{"number":41,"word":"はし(橋)"}],"sentences":[]}`
	cases := []struct {
		name          string
		hard          bool
		expected      []WordGroup
		expectedEntry ChangelogEntry
	}{
		{
			name: "daily", hard: false,
			expected: []WordGroup{
				{Title: "第2课", Words: []Word{
					{Word: "はし(橋)", Definition: "桥", ReviewCount: 7, ErrorCount: 1, ConsecutiveCorrect: 3, LastReview: "01/03", Status: "\U0001f7e2已掌握", Group: "第2课"},
					{Word: "なつ(夏)", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: "\U0001f504待测试", Group: "第2课"},
				}},
				{Title: "第1课", Words: []Word{
					{Word: "みず(水)", Definition: "水", ReviewCount: 2, ErrorCount: 1, ConsecutiveCorrect: 0, LastReview: "01/03", Status: "\U0001f534待巩固", Group: "第1课"},
				}},
			},
			expectedEntry: ChangelogEntry{
				Version: "v1.0", Total: "3", Mastered: "1", Basic: "0", NeedsConsol: "1", Untested: "1", Errors: "2", NailHouse: "1",
				Description: "复习结果：1词写对，1词写错（2词未找到）",
			},
		},
		{
			name: "hard", hard: true,
			expected: []WordGroup{
				{Title: "第2课", Words: []Word{
					{Word: "はし(橋)", Definition: "桥", ReviewCount: 7, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "01/03", Status: "\U0001f7e1基本掌握", Group: "第2课"},
					{Word: "なつ(夏)", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: "\U0001f504待测试", Group: "第2课"},
				}},
				{Title: "第1课", Words: []Word{
					{Word: "みず(水)", Definition: "水", ReviewCount: 2, ErrorCount: 0, ConsecutiveCorrect: 2, LastReview: "01/03", Status: "\U0001f7e1基本掌握", Group: "第1课"},
				}},
			},
			expectedEntry: ChangelogEntry{
				Version: "v1.0", Total: "3", Mastered: "0", Basic: "2", NeedsConsol: "0", Untested: "1", Errors: "2", NailHouse: "1",
				Description: "钉子户专项复习结果：1词写对，1词写错（2词未找到）",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := newMemObjStore()
			// 直接注入现有内存桩，不构造真实客户端；保留写入路径而非 dry-run。
			st := &Storage{lang: "ja", store: mem}
			seed := map[string]string{
				"language-review/ja/archives/" + oldFilename:    oldArchive,
				"language-review/ja/plans/plan_2020-01-03.json": dailyPlan,
				"language-review/ja/plans/hard_2020-01-03.json": hardPlan,
			}
			// 旧档案和计划均用过去固定日期，不依赖执行日或跨午夜的取时。
			for key, content := range seed {
				mem.objs[key] = []byte(content)
				mem.mod[key] = time.Date(2020, time.January, 2, 12, 0, 0, 0, time.UTC)
			}
			result, err := ApplyRecord(context.Background(), st, "ja", RecordInput{
				PlanDate: "2020-01-03", Language: "ja", Hard: tc.hard,
				WordResults: []RecordResult{
					{Number: 41, Correct: false},
					{Number: 999, Correct: true},
					{Number: 7, Correct: true},
					{Number: 88, Correct: false},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				t.Fatal("ApplyRecord 未返回结果")
			}
			if !result.Success || result.Command != "record" || result.PlanDate != "2020-01-03" || result.Hard != tc.hard ||
				result.Correct != 1 || result.Wrong != 1 || result.NotFound != 2 || result.TotalWords != 3 ||
				result.OldFilename != oldFilename || result.Version != "v1.0" {
				t.Fatalf("返回结果不符：%+v", result)
			}
			data, ok := mem.objs["language-review/ja/archives/"+result.NewFilename]
			if !ok || result.NewFilename == oldFilename || len(mem.objs) != 4 {
				t.Fatalf("本次必须仅在内存新增一个档案：filename=%q，对象数=%d", result.NewFilename, len(mem.objs))
			}
			for key, content := range seed {
				if string(mem.objs[key]) != content {
					t.Errorf("原档案或计划被修改：%s", key)
				}
			}
			arc, err := archive.ParseArchive(string(data), "ja")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(arc.Groups, tc.expected) {
				t.Fatalf("词形映射、回写日期或未提交词字段不符：got %+v，预期 %+v", arc.Groups, tc.expected)
			}
			if len(arc.Changelog) != 2 {
				t.Fatalf("历史应为旧行加一条新行：%+v", arc.Changelog)
			}
			expectedOldEntry := ChangelogEntry{
				Date: "200102", Version: "v3.4", Total: "3", Mastered: "0", Basic: "2", NeedsConsol: "0", Untested: "1", Errors: "1", NailHouse: "1", Description: "固定旧历史",
			}
			if arc.Changelog[0] != expectedOldEntry || strings.Count(string(data), oldHistoryRow+"\n") != 1 {
				t.Fatalf("旧历史行必须原样保留一次：%+v", arc.Changelog[0])
			}
			entry := arc.Changelog[1]
			if _, err := time.Parse("060102", entry.Date); err != nil {
				t.Fatalf("新增历史日期无效：%q", entry.Date)
			}
			if result.NewFilename != "日语学习进度档案_"+entry.Date+"_v1.0.md" {
				t.Fatalf("新文件名与历史日期不一致：%q，%q", result.NewFilename, entry.Date)
			}
			// 执行日不固定；统计手写。两种模式的历史钉子户均为1，按准确率分类则均为0。
			entry.Date = ""
			if entry != tc.expectedEntry {
				t.Fatalf("新增历史统计或描述不符：got %+v，预期 %+v", entry, tc.expectedEntry)
			}
		})
	}
}

// ==========================================================================
// 当天分多次练习（2026-11-17）：plan 锁定 + 提交剪枝
// ==========================================================================

// 固定夹具档案：两个从没测过的到期词 + 两个明确的钉子户（正确率 0% / 25%）。
// 这样「重算」和「复用已锁定的 plan」结果一定不同，锁定失效就会被测出来。
const multiSessionArchive = "## \U0001f4cb 版本历史\n\n" +
	"|日期|版本|总词数|已掌握|基本掌握|待巩固|待测试|累计答错|钉子户|变化内容|\n" +
	"|---|---|---|---|---|---|---|---|---|---|\n" +
	"|261117|v1.0|4|0|0|0|4|0|0|固定夹具|\n\n" +
	"# 日语单词学习进度档案\n最后更新：11/17\n\n" +
	"## \U0001f4c5 单词列表（按课分组）\n\n" +
	"### 第1课\n\n" +
	"|日语单词|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n" +
	"|---|---|---|---|---|---|---|\n" +
	"|みず(水)|水|0|0|0||\U0001f504待测试|\n" +
	"|はし(橋)|桥|0|0|0||\U0001f504待测试|\n" +
	"|なつ(夏)|夏天|5|5|0|11/01|\U0001f534待巩固|\n" +
	"|ゆき(雪)|雪|4|3|0|11/01|\U0001f534待巩固|\n"

// multiSessionFixture 造一份「今天」的档案，可选注入当天的 daily / hard plan。
// 返回 server、内存 COS 和日期字符串；直接调 handler（绕过 withUser）。
func multiSessionFixture(t *testing.T, planKind, planJSON string) (*server, *Storage, *memObjStore, string) {
	t.Helper()
	now := time.Now()
	date := now.Format("2006-01-02")
	mem := newMemObjStore()

	arcKey := "language-review/ja/archives/" +
		archive.Filename(LangConfigs["ja"].FilePrefix, now, 1, 0)
	mem.objs[arcKey] = []byte(multiSessionArchive)
	mem.mod[arcKey] = now

	if planJSON != "" {
		// 允许夹具里写 "DATE" 占位符，由这里换成真正的今天（避免调用方在
		// := 右侧引用刚声明的 date 变量）。
		planJSON = strings.Replace(planJSON, "DATE", date, 1)
		key := "language-review/ja/plans/plan_" + date + ".json"
		if planKind == "hard" {
			key = "language-review/ja/plans/hard_" + date + ".json"
		}
		mem.objs[key] = []byte(planJSON)
		mem.mod[key] = now
	}
	st := &Storage{lang: "ja", store: mem}
	return &server{storage: st, lang: "ja"}, st, mem, date
}

// 同一天反复 GET /api/plan 必须复用已存的 plan，不能按档案重算换一批题。
func TestMultiSessionHandlePlanReusesTodayPlan(t *testing.T) {
	srv, st, mem, date := multiSessionFixture(t, "", "")
	// 故意用跟档案算出来完全不同的词和题号，锁定失效会立刻露馅。
	stored := `{"date":"` + date + `","language":"ja","words":` +
		`[{"number":7,"word":"なつ(夏)","definition":"夏天"}],"sentences":[]}`
	key := "language-review/ja/plans/plan_" + date + ".json"
	mem.objs[key] = []byte(stored)
	mem.mod[key] = time.Now()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/plan?mode=words&date="+date, nil)
	srv.handlePlan(rec, req, st)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Words []struct {
			Number int    `json:"number"`
			Word   string `json:"word"`
		} `json:"words"`
		DueCount int `json:"due_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.DueCount != 1 || len(out.Words) != 1 ||
		out.Words[0].Number != 7 || out.Words[0].Word != "なつ(夏)" {
		t.Fatalf("应原样返回已锁定的 #7，实际 %+v", out)
	}
	if string(mem.objs[key]) != stored {
		t.Error("GET 不该覆盖当天 plan（覆盖会把题号重新编号）")
	}
}

// 全交完留下的空 hard plan 也必须锁住：hard 是全量普查，一旦重算就会
// 把刚练完的钉子户又发一遍，老师再提交就是同一天二次改档案。
func TestMultiSessionHandleHardReusesEmptyTodayPlan(t *testing.T) {
	stored := `{"date":"DATE","language":"ja","kind":"hard","words":[],"sentences":[]}`
	srv, st, mem, date := multiSessionFixture(t, "hard", stored)
	key := "language-review/ja/plans/hard_" + date + ".json"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hard?date="+date, nil)
	srv.handleHard(rec, req, st)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		HardCount int `json:"hard_count"`
		Remaining int `json:"remaining"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.HardCount != 0 || out.Remaining != 0 {
		t.Fatalf("空 plan 应锁住（档案里其实有 2 个钉子户），实际 %+v", out)
	}
	if got := string(mem.objs[key]); got != strings.Replace(stored, "DATE", date, 1) {
		t.Errorf("空 plan 被重算覆盖了：%s", got)
	}
}

// 走完整的 /api/record：分两次提交，验证服务端剪枝、题号不重排、
// 返回 words_remaining，以及全交完保留空 plan 当「今天出过题」的标记。
func TestMultiSessionHandleRecordPrunesWordPlan(t *testing.T) {
	srv, st, mem, date := multiSessionFixture(t, "", "")
	stored := `{"date":"` + date + `","language":"ja","words":` +
		`[{"number":1,"word":"みず(水)"},{"number":2,"word":"はし(橋)"},` +
		`{"number":3,"word":"なつ(夏)"},{"number":4,"word":"ゆき(雪)"}],"sentences":[]}`
	key := "language-review/ja/plans/plan_" + date + ".json"
	mem.objs[key] = []byte(stored)
	mem.mod[key] = time.Now()

	post := func(results string) float64 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/record",
			strings.NewReader(`{"plan_date":"`+date+`","word_results":`+results+`}`))
		srv.handleRecord(rec, req, st)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		v, ok := out["words_remaining"].(float64)
		if !ok {
			t.Fatalf("返回里缺 words_remaining：%s", rec.Body.String())
		}
		return v
	}

	// 第一批只交 #1 #3
	if got := post(`[{"number":1,"correct":true},{"number":3,"correct":false}]`); got != 2 {
		t.Fatalf("第一批后应剩 2，实际 %v", got)
	}
	p1, err := st.DownloadPlan(context.Background(), date)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Words) != 2 || p1.Words[0].Number != 2 || p1.Words[0].Word != "はし(橋)" ||
		p1.Words[1].Number != 4 || p1.Words[1].Word != "ゆき(雪)" {
		t.Fatalf("剩下的应是原编号 #2 #4：%+v", p1.Words)
	}

	// 第二批交完，plan 变空但仍锁在当天
	if got := post(`[{"number":2,"correct":true},{"number":4,"correct":true}]`); got != 0 {
		t.Fatalf("第二批后应剩 0，实际 %v", got)
	}
	p2, err := st.DownloadPlan(context.Background(), date)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Words) != 0 || !plan.WordPlanLocked(p2, date) {
		t.Fatalf("全交完应保留一份空的当天 plan：%+v", p2)
	}
}

// hard 提交必须剪 hard plan，绝不能误剪 daily plan（两把 COS key 别串）。
func TestMultiSessionPruneSubmittedWordPlanUsesHardKey(t *testing.T) {
	srv, st, mem, date := multiSessionFixture(t, "", "")
	daily := `{"date":"` + date + `","language":"ja","words":[{"number":1,"word":"みず(水)"}],"sentences":[]}`
	hardP := `{"date":"` + date + `","language":"ja","kind":"hard","words":` +
		`[{"number":5,"word":"なつ(夏)"},{"number":6,"word":"ゆき(雪)"}],"sentences":[]}`
	dailyKey := "language-review/ja/plans/plan_" + date + ".json"
	hardKey := "language-review/ja/plans/hard_" + date + ".json"
	mem.objs[dailyKey] = []byte(daily)
	mem.mod[dailyKey] = time.Now()
	mem.objs[hardKey] = []byte(hardP)
	mem.mod[hardKey] = time.Now()

	got := srv.pruneSubmittedWordPlan(context.Background(), st, RecordInput{
		PlanDate: date, Hard: true,
		WordResults: []RecordResult{{Number: 5, Correct: true}},
	})
	if got != 1 {
		t.Fatalf("hard plan 应剩 1，实际 %d", got)
	}
	hp, err := st.DownloadHardPlan(context.Background(), date)
	if err != nil || len(hp.Words) != 1 || hp.Words[0].Number != 6 {
		t.Fatalf("hard plan 应只剩 #6：%+v err=%v", hp, err)
	}
	if string(mem.objs[dailyKey]) != daily {
		t.Error("daily plan 被误改了")
	}
}

// ==========================================================================
// 原 draft_test.go
// ==========================================================================

func TestDraftKey(t *testing.T) {
	s := &Storage{lang: "ja"}
	got := s.draftKey("2026-09-20", "words")
	if !strings.HasPrefix(got, "language-review/ja/drafts/") {
		t.Fatalf("草稿键应落在 drafts/ 前缀下，实际 %q", got)
	}
	if !strings.HasSuffix(got, "draft_2026-09-20_words.json") {
		t.Fatalf("草稿键应带日期和模式，实际 %q", got)
	}

	// 同一天的今日练习和钉子户必须各占一个键，否则会互相覆盖
	if s.draftKey("2026-09-20", "words") == s.draftKey("2026-09-20", "hard") {
		t.Fatal("words 与 hard 的草稿键撞了")
	}
	// 隔天的草稿绝不能串到今天
	if s.draftKey("2026-09-20", "words") == s.draftKey("2026-09-21", "words") {
		t.Fatal("相邻两天的草稿键撞了")
	}
}

func TestDraftHasContent(t *testing.T) {
	cases := []struct {
		name string
		d    *Draft
		want bool
	}{
		{"nil 草稿", nil, false},
		{"没有条目", &Draft{}, false},
		{"全空答案", &Draft{Items: []DraftItem{{Number: 1}, {Number: 2}}}, false},
		{"写了答案", &Draft{Items: []DraftItem{{Number: 1, Answer: "きもち"}}}, true},
		{"只点了不会", &Draft{Items: []DraftItem{{Number: 1, Unknown: true}}}, true},
		{"只勾了算对", &Draft{Items: []DraftItem{{Number: 1, Manual: true}}}, true},
	}
	for _, c := range cases {
		if got := c.d.HasContent(); got != c.want {
			t.Errorf("%s: HasContent() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidDraftMode(t *testing.T) {
	ok := []string{"words", "hard", "sentences", "offline", "WORDS", "Hard"}
	for _, m := range ok {
		if !validDraftMode(m) {
			t.Errorf("合法模式 %q 被拒了", m)
		}
	}
	bad := []string{"", "whatever", "../etc", "words/../x", "单词"}
	for _, m := range bad {
		if validDraftMode(m) {
			t.Errorf("非法模式 %q 被放行了，它会污染 COS 键", m)
		}
	}
}

// ==========================================================================
// 原 api_knowledge_test.go
// ==========================================================================

// 知识点 HTTP 测试保留目录排序、匿名/体验版权限、全文访问和错误响应断言。
// markdown 纯测试迁至 internal/knowledge；这里保留相同样本供内存存储夹具使用。

const sampleKnowledgeMD = `# 标准日本语初级上册 第21课 知识点

> **本课核心：动词「た形」。**

## た形速查表

| 类别 | ます形 | た形 |
|---|---|---|
| 一类 | 書きます | 書いた |
| 二类 | 食べます | 食べた |

**口诀：て→た、で→だ。**

### 四大句型

- 动た形 ＋ ことが あります
- 动た形 ＋ 後で

1. 前项是名词时：名词＋の＋後で
2. 前项接た形

段落里的 ` + "`コード`" + ` 要单独成段，别被当成粗体。

落单的星号 * 不该吃掉后面的文字。
`

func knowledgeTestServer(t *testing.T) *server {
	mem := newMemObjStore()
	ctx := context.Background()
	// 课号故意乱序写入：列表必须按数字排，不能按文件名字符串排
	for _, n := range []string{"21", "2", "10", "1"} {
		name := "标准日本语初级上册_第" + n + "课知识点.md"
		if err := mem.PutObject(ctx, "language-review/ja/knowledge/"+name,
			[]byte(sampleKnowledgeMD)); err != nil {
			t.Fatal(err)
		}
	}
	// 这两类不是「按课的知识点」，不能进列表
	mem.PutObject(ctx, "language-review/ja/knowledge/课本原文汇编_第1单元（基本课文）.md", []byte("# 汇编"))
	mem.PutObject(ctx, "language-review/ja/knowledge/造句易错语法速查（第12-13课）.md", []byte("# 速查"))
	return &server{storage: &Storage{store: mem, lang: "ja"}, lang: "ja", token: "tok"}
}

func TestKnowledgeListSortedByLessonNumber(t *testing.T) {
	srv := knowledgeTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleKnowledge(rec, httptest.NewRequest("GET", "/api/knowledge", nil))
	var out struct {
		Success bool `json:"success"`
		Locked  bool `json:"locked"`
		Lessons []struct {
			Lesson int    `json:"lesson"`
			Title  string `json:"title"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || len(out.Lessons) != 4 {
		t.Fatalf("列表条数不对（应只含 4 篇按课知识点）：%+v", out.Lessons)
	}
	want := []int{1, 2, 10, 21}
	for i, w := range want {
		if out.Lessons[i].Lesson != w {
			t.Fatalf("课号没按数字排序：got=%v want=%v", out.Lessons, want)
		}
	}
	if !out.Locked {
		t.Fatal("匿名请求应当 locked=true")
	}
}

func TestKnowledgeAnonymousIsPartialButSharedTokenIsFull(t *testing.T) {
	srv := knowledgeTestServer(t)

	do := func(header http.Header) (locked bool, visible, total int) {
		req := httptest.NewRequest("GET", "/api/knowledge?lesson=21", nil)
		for k, v := range header {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		srv.handleKnowledge(rec, req)
		var out struct {
			Success bool     `json:"success"`
			Locked  bool     `json:"locked"`
			Visible int      `json:"visible"`
			Blocks  []KBlock `json:"blocks"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Success {
			t.Fatalf("请求失败：%s", rec.Body.String())
		}
		return out.Locked, out.Visible, len(out.Blocks)
	}

	locked, visible, total := do(nil)
	if !locked || visible >= total {
		t.Fatalf("匿名应受限：locked=%v visible=%d total=%d", locked, visible, total)
	}
	locked, visible, total = do(http.Header{"Authorization": []string{"Bearer tok"}})
	if locked || visible != total {
		t.Fatalf("共享 token 应给全文：locked=%v visible=%d total=%d", locked, visible, total)
	}

	// owner：uid 恒为空（零迁移约定）。2026-11-03 的真事故 —— 这里曾经把
	// uid=="" 当成匿名挡掉，结果小程序里「每日复习已登录、知识点还是未登录」。
	// 同一个会话走 withUser 的页面全是登录态，只有这条自己认会话的路由被挡。
	sid := "owner-sid-" + time.Now().Format("150405.000000000")
	sessions.Lock()
	sessions.m[sid] = &session{UID: "", Role: "owner", Created: time.Now(), LastSeen: time.Now()}
	sessions.Unlock()
	locked, visible, total = do(http.Header{"Authorization": []string{"Bearer " + sid}})
	if locked || visible != total {
		t.Fatalf("owner（uid 为空）应给全文：locked=%v visible=%d total=%d", locked, visible, total)
	}

	// 体验版：uid 是 trial，凭证是公开的，一律受限
	tsid := "trial-sid-" + time.Now().Format("150405.000000000")
	sessions.Lock()
	sessions.m[tsid] = &session{UID: trialUID, Role: "trial", Created: time.Now(), LastSeen: time.Now()}
	sessions.Unlock()
	locked, visible, total = do(http.Header{"Authorization": []string{"Bearer " + tsid}})
	if !locked || visible >= total {
		t.Fatalf("体验版应受限：locked=%v visible=%d total=%d", locked, visible, total)
	}
}

func TestKnowledgeUnknownLessonIs404(t *testing.T) {
	srv := knowledgeTestServer(t)
	rec := httptest.NewRecorder()
	srv.handleKnowledge(rec, httptest.NewRequest("GET", "/api/knowledge?lesson=99", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的课号应 404，实际 %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.handleKnowledge(rec, httptest.NewRequest("GET", "/api/knowledge?lesson=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非数字课号应 400，实际 %d", rec.Code)
	}
}

// ==========================================================================
// 原 api_wordmeta_test.go
// ==========================================================================

// 词性 / 备注 / 圆级 join 到练习题上（老师 2026-09-28 要的：卡片上不能只有中文释义；
// 圆级是 2026-11-03 加的「单词复习时展示圆级」）。
// 这几样存在独立的 word_meta.json 里，按完整词形「假名(汉字)」匹配。
func TestLookupWordMeta(t *testing.T) {
	meta := &WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "空间上从某处穿过；過ぎます 是时间流逝", Circle: "三圆"},
		"なかなか":        {Pos: "副词", Note: "程度副词，比预想要高", Circle: "二圆"},
		"おふろ(お風呂)":    {Pos: "名词"},
	}}

	// 四样齐全
	pos, sub, note, circle := words.LookupWordMeta(meta, "とおります(通ります)")
	if pos != "动词" || sub != "一类" || note != "空间上从某处穿过；過ぎます 是时间流逝" || circle != "三圆" {
		t.Errorf("词性/细分/备注/圆级应同时取到，got %q %q %q %q", pos, sub, note, circle)
	}

	// 没细分的（名词/副词没有 sub）
	pos, sub, note, circle = words.LookupWordMeta(meta, "なかなか")
	if pos != "副词" || sub != "" || note != "程度副词，比预想要高" || circle != "二圆" {
		t.Errorf("没细分时 sub 应为空，got %q %q %q %q", pos, sub, note, circle)
	}

	// 没备注 → note 空着。这不是缺数据：词义明确的词本来就不用写，
	// 页面会拿释义括号里那截兜底（前端 app.defParts）。
	// 手册没收的词 circle 也空着 —— 页面不显示这一格，不是「没标」。
	pos, sub, note, circle = words.LookupWordMeta(meta, "おふろ(お風呂)")
	if pos != "名词" || sub != "" || note != "" || circle != "" {
		t.Errorf("没备注/没分级的应返回空，got %q %q %q %q", pos, sub, note, circle)
	}

	// 词性表里没有这个词（比如新导入还没标注）→ 全空，页面显示「—」
	pos, sub, note, circle = words.LookupWordMeta(meta, "あたらしい(新しい)")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("未标注的词应返回空，got %q %q %q %q", pos, sub, note, circle)
	}

	// 词形对不上（裸假名 vs 带汉字）不猜 —— 猜错会把另一个词的词性贴上来
	pos, sub, note, circle = words.LookupWordMeta(meta, "とおります")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("裸假名不该命中带汉字的条目，got %q %q %q %q", pos, sub, note, circle)
	}

	// 词性表整个读不到（COS 挂了 / 还没上传）→ 不能炸，整批留空
	pos, sub, note, circle = words.LookupWordMeta(nil, "とおります(通ります)")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("meta 为 nil 应返回空，got %q %q %q %q", pos, sub, note, circle)
	}
	pos, sub, note, circle = words.LookupWordMeta(&WordMeta{}, "とおります(通ります)")
	if pos != "" || sub != "" || note != "" || circle != "" {
		t.Errorf("空表应返回空，got %q %q %q %q", pos, sub, note, circle)
	}
}

// 圆级必须真的 join 到当天练习的每个词上（2026-11-03）：
// 后端不带上，前端再怎么画也是「—」。
func TestEnrichWordMetaCarriesCircle(t *testing.T) {
	meta := &WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Circle: "三圆"},
		"おふろ(お風呂)":    {Circle: "一圆"},
	}}
	planWords := []PlanWord{{Word: "とおります(通ります)"}, {Word: "おふろ(お風呂)"}, {Word: "なかなか"}}
	for i := range planWords {
		planWords[i].Pos, planWords[i].Sub, planWords[i].Note, planWords[i].Circle =
			words.LookupWordMeta(meta, planWords[i].Word)
	}
	want := []string{"三圆", "一圆", ""}
	for i, w := range planWords {
		if w.Circle != want[i] {
			t.Errorf("words[%d].Circle = %q, want %q", i, w.Circle, want[i])
		}
	}
	// omitempty：手册没收的词不该往接口里塞一个空 circle 字段
	if one := toJSON(planWords[2]); strings.Contains(one, "circle") {
		t.Errorf("没分级的词不该出现 circle 字段，got %s", one)
	}
}

// 备注要能存进 word_meta.json 再读回来，且没写备注的词不该多出一个字段。
func TestWordPOSNoteRoundTrip(t *testing.T) {
	m := WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "空间上穿过"},
		"おふろ(お風呂)":    {Pos: "名词"},
	}}
	s := toJSON(m)

	var back WordMeta
	if err := jsonUnmarshal([]byte(s), &back); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got := back.Items["とおります(通ります)"].Note; got != "空间上穿过" {
		t.Errorf("备注应 round-trip，got %q", got)
	}
	if got := back.Items["おふろ(お風呂)"].Note; got != "" {
		t.Errorf("没备注的词读回来应为空，got %q", got)
	}
	// omitempty：写了备注的才序列化，没写的不该往 900 多条的文件里塞一堆空字段
	if one := toJSON(m.Items["とおります(通ります)"]); !strings.Contains(one, `"note"`) || !strings.Contains(one, "空间上穿过") {
		t.Errorf("写了备注的应序列化出来，got %s", one)
	}
	if one := toJSON(m.Items["おふろ(お風呂)"]); strings.Contains(one, "note") {
		t.Errorf("没备注的词不该出现 note 字段，got %s", one)
	}
}
