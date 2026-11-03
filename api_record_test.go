package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 造句部分提交：没交的句子必须留在 plan 里（刷新后还在），
// 已交的摘掉；全部交完时 plan 清空（上层据此删 plan 换新的一批）。
// 2026-10-08 之前是只要回写就整批删 plan，部分提交后刷新，没写的句子就没了。
func TestPruneSentencePlanPartial(t *testing.T) {
	plan := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
		{Number: 3, Answer: "c"},
		{Number: 4, Answer: "d"},
	}}
	left := pruneSentencePlan(plan, []SentenceResult{
		{Number: 1, Correct: true},
		{Number: 3, Correct: false},
	})
	if left != 2 {
		t.Fatalf("left = %d, want 2", left)
	}
	if plan.Sentences[0].Number != 2 || plan.Sentences[1].Number != 4 {
		t.Fatalf("剩下的应是 #2 #4（保持原编号）: %+v", plan.Sentences)
	}
}

// 全部交完 → plan 清空，上层删 plan。
func TestPruneSentencePlanAll(t *testing.T) {
	plan := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
	}}
	left := pruneSentencePlan(plan, []SentenceResult{
		{Number: 1, Correct: true},
		{Number: 2, Correct: false},
	})
	if left != 0 || len(plan.Sentences) != 0 {
		t.Fatalf("全交完应清空: left=%d plan=%+v", left, plan.Sentences)
	}
}

// 分两次提交同一批：第二次提交时 plan 已是第一次裁剪过的，
// 号码不重叠的提交不该误删剩下的句子。
func TestPruneSentencePlanTwice(t *testing.T) {
	plan := &ReviewPlan{Sentences: []PlanSentence{
		{Number: 1, Answer: "a"},
		{Number: 2, Answer: "b"},
		{Number: 3, Answer: "c"},
	}}
	if l := pruneSentencePlan(plan, []SentenceResult{{Number: 1, Correct: true}}); l != 2 {
		t.Fatalf("第一次后应剩 2: %d", l)
	}
	if l := pruneSentencePlan(plan, []SentenceResult{{Number: 2, Correct: true}}); l != 1 {
		t.Fatalf("第二次后应剩 1: %d", l)
	}
	if plan.Sentences[0].Number != 3 {
		t.Fatalf("最后应只剩 #3: %+v", plan.Sentences)
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
			arc, err := ParseArchive(string(data), "ja")
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
