package plan

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
)

// fakeSentenceStore 是 SentenceStore 的内存替身：四份数据 + 各自可控的读取错误。
// 用例层只认接口，不认 *Storage，所以这里不需要 COS、HTTP、全局配置缓存。
type fakeSentenceStore struct {
	bank   *sentence.SentenceBank
	hist   sentence.SentenceHistory
	wrong  *sentence.SentenceWrong
	review *sentencereview.ReviewBank

	bankErr   error
	histErr   error
	wrongErr  error
	reviewErr error
}

func (f *fakeSentenceStore) DownloadSentenceBank(context.Context) (*sentence.SentenceBank, error) {
	return f.bank, f.bankErr
}
func (f *fakeSentenceStore) DownloadSentenceHistory(context.Context) (sentence.SentenceHistory, error) {
	return f.hist, f.histErr
}
func (f *fakeSentenceStore) DownloadSentenceWrong(context.Context) (*sentence.SentenceWrong, error) {
	return f.wrong, f.wrongErr
}
func (f *fakeSentenceStore) DownloadReviewBank(context.Context) (*sentencereview.ReviewBank, error) {
	return f.review, f.reviewErr
}

// fakeMeta 记录调用次数，用来锁住「配置无条件取一次」「圆级表只在有回炉库时才读」。
type fakeMeta struct {
	cfg    planconfig.PlanConfig
	circle map[string]string

	configCalls int
	circleCalls int
}

func (m *fakeMeta) PlanConfig(context.Context) planconfig.PlanConfig {
	m.configCalls++
	return m.cfg
}
func (m *fakeMeta) Circle(context.Context) map[string]string {
	m.circleCalls++
	return m.circle
}

var (
	_ SentenceStore = (*fakeSentenceStore)(nil)
	_ Meta          = (*fakeMeta)(nil)
)

// composeFixture 造一份足够丰富的数据：句库跨 3 课含变形句、错句表里有一条
// 到期的强制重练、轮换账本有远期记录、回炉库 5 句、圆级表标了三圆/二圆。
func composeFixture() (*fakeSentenceStore, *fakeMeta, planconfig.PlanConfig, time.Time) {
	today := time.Date(2026, 11, 4, 0, 0, 0, 0, time.Local)
	ratio := 40
	cfg := planconfig.PlanConfig{
		SentenceBaseCount:         4,
		SentenceReviewCount:       3,
		SentenceReviewCircleRatio: &ratio,
	}

	bank := &sentence.SentenceBank{
		Version: 1,
		Sentences: []sentence.BankSentence{
			{ID: "a1", Lesson: "第1课", Type: "original", Answer: "これは 本です。", Chinese: "这是书"},
			{ID: "a2", Lesson: "第1课", Type: "original", Answer: "それも 本です。", Chinese: "那也是书"},
			{ID: "a3", Lesson: "第2课", Type: "original", Answer: "わたしは 学生です。", Chinese: "我是学生"},
			{ID: "a4", Lesson: "第2课", Type: "original", Answer: "あの人は 先生です。", Chinese: "他是老师"},
			{ID: "a5", Lesson: "第3课", Type: "original", Answer: "今日は いい天気です。", Chinese: "今天天气好"},
			{ID: "a6", Lesson: "第3课", Type: "original", Answer: "明日は 雨です。", Chinese: "明天下雨"},
			{ID: "t1", Lesson: "第1课", Type: "transformed", Prototype: "これは 本です。", Answer: "これは 雑誌です。", Chinese: "这是杂志"},
		},
	}
	hist := sentence.SentenceHistory{
		"2026-10-01": {"これは 本です。"}, // 远期，不落进近 7 天禁用集
	}
	wrong := &sentence.SentenceWrong{Sentences: []sentence.WrongSentence{
		{Answer: "忘れた文です。", Chinese: "忘掉的句子", Status: "open", NextDue: "2026-11-01", WrongCount: 2},
	}}
	reviewBank := &sentencereview.ReviewBank{
		Version: 1,
		Sentences: []sentencereview.ReviewSentence{
			{ID: "r1", Word: "ほん(本)", Answer: "ほんは 新しいです。", Chinese: "书是新的"},
			{ID: "r2", Word: "がくせい(学生)", Answer: "がくせいは 忙しいです。", Chinese: "学生很忙"},
			{ID: "r3", Word: "せんせい(先生)", Answer: "せんせいは 優しいです。", Chinese: "老师很温柔"},
			{ID: "r4", Word: "てんき(天気)", Answer: "てんきは 悪いです。", Chinese: "天气不好"},
			{ID: "r5", Word: "あめ(雨)", Answer: "あめは 冷たいです。", Chinese: "雨很冷"},
		},
	}
	circle := map[string]string{
		"ほん(本)":    "三圆",
		"がくせい(学生)": "二圆",
	}

	store := &fakeSentenceStore{bank: bank, hist: hist, wrong: wrong, review: reviewBank}
	meta := &fakeMeta{cfg: cfg, circle: circle}
	return store, meta, cfg, today
}

// referenceCompose 重演改造前根包三处逐字重复的编排（读四库 → 基础挑句 →
// exclude → 圆级表 → 回炉挑句 → 追加编号），只用于对拍，不进生产代码。
func referenceCompose(f *fakeSentenceStore, m *fakeMeta, cfg planconfig.PlanConfig,
	today time.Time, count int, arc *archive.Archive) []sentence.PlanSentence {

	out := sentence.BuildSentencePlan(f.bank, f.hist, f.wrong, today, count)
	if f.review == nil {
		return out
	}
	exclude := make(map[string]bool, len(out))
	for _, ps := range out {
		exclude[sentence.Norm(ps.Answer)] = true
	}
	extra := sentencereview.BuildReviewSentences(f.review, f.hist, arc, today,
		cfg.SentenceReviewCount, exclude, sentencereview.DefaultReviewIdleDays,
		m.circle, planconfig.PlanCircleRatio(cfg))
	for _, e := range extra {
		out = append(out, sentence.PlanSentence{
			Number:  len(out) + 1,
			Chinese: e.Chinese,
			Answer:  e.Answer,
		})
	}
	return out
}

func assertContinuousNumbers(t *testing.T, got []sentence.PlanSentence) {
	t.Helper()
	for i, p := range got {
		if p.Number != i+1 {
			t.Fatalf("第 %d 句编号 = %d, want %d（编号必须连续）", i, p.Number, i+1)
		}
	}
}

// 行为不变的主证据：把原编排重演一遍逐句对拍。三个 case 覆盖三入口的差异：
// 网页端基础句数跟配置走、CLI 跟 --count 走、带档案时闲置过滤生效。
func TestComposeSentencesMatchesOriginalOrchestration(t *testing.T) {
	arcWithRecent := &archive.Archive{
		Language: "ja",
		Groups: []review.WordGroup{{
			Title: "第1课",
			// 11/02 距 11/04 只闲置 2 天 < 30 → r1 该被过滤掉
			Words: []review.Word{{Word: "ほん(本)", LastReview: "11/02"}},
		}},
	}

	cases := []struct {
		name      string
		baseCount int // 传给 Params
		arc       *archive.Archive
		wantBase  int // 原编排喂给 BuildSentencePlan 的 count
	}{
		{"网页端_基础句数跟配置_无档案", 0, nil, 4},
		{"CLI_基础句数跟传参_无档案", 6, nil, 6},
		{"网页端_带档案_闲置过滤生效", 0, arcWithRecent, 4},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, meta, cfg, today := composeFixture()

			res, err := ComposeSentences(context.Background(), store, meta, Params{
				Today: today, Arc: c.arc, BaseCount: c.baseCount,
			})
			if err != nil {
				t.Fatalf("ComposeSentences: %v", err)
			}

			want := referenceCompose(store, meta, cfg, today, c.wantBase, c.arc)
			if !reflect.DeepEqual(res.Sentences, want) {
				t.Fatalf("句表与改造前编排不一致\n got = %+v\nwant = %+v", res.Sentences, want)
			}
			assertContinuousNumbers(t, res.Sentences)

			if res.BaseCount != len(want)-len(res.ReviewExtra) {
				t.Errorf("BaseCount = %d, want %d", res.BaseCount, len(want)-len(res.ReviewExtra))
			}
			if res.ReviewCount != len(res.ReviewExtra) {
				t.Errorf("ReviewCount = %d, want %d", res.ReviewCount, len(res.ReviewExtra))
			}
			if res.CircleRatio != planconfig.PlanCircleRatio(cfg) || res.CircleRatio != 40 {
				t.Errorf("CircleRatio = %d, want 40", res.CircleRatio)
			}
			if !reflect.DeepEqual(res.Circle, meta.circle) {
				t.Errorf("Circle 未透传: %+v", res.Circle)
			}
			if res.ReviewBank != store.review || res.Bank != store.bank || res.Wrong != store.wrong {
				t.Error("原始四库数据未透传进 Result")
			}
			if !reflect.DeepEqual(res.History, store.hist) {
				t.Error("History 未透传进 Result")
			}
			if meta.configCalls != 1 {
				t.Errorf("PlanConfig 调用 %d 次, want 1", meta.configCalls)
			}
			if meta.circleCalls != 1 {
				t.Errorf("Circle 调用 %d 次, want 1（有回炉库才读词性表）", meta.circleCalls)
			}

			// 末尾的加餐句必须与 ReviewExtra 逐条对应。
			tail := res.Sentences[len(res.Sentences)-len(res.ReviewExtra):]
			for i, e := range res.ReviewExtra {
				if tail[i].Chinese != e.Chinese || tail[i].Answer != e.Answer {
					t.Errorf("第 %d 条加餐句与 ReviewExtra 不符: %+v vs %+v", i, tail[i], e)
				}
			}
		})
	}
}

// 读三库失败的阶段要能还原入口各自的原有错误文案，并且失败时不去取配置 / 圆级表。
func TestComposeSentencesLoadErrorStages(t *testing.T) {
	sentinel := errors.New("cos down")
	cases := []struct {
		name  string
		set   func(*fakeSentenceStore)
		stage string
	}{
		{"句库", func(f *fakeSentenceStore) { f.bankErr = sentinel }, StageBank},
		{"轮换账本", func(f *fakeSentenceStore) { f.histErr = sentinel }, StageHistory},
		{"错句表", func(f *fakeSentenceStore) { f.wrongErr = sentinel }, StageWrong},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, meta, _, today := composeFixture()
			c.set(store)

			_, err := ComposeSentences(context.Background(), store, meta, Params{Today: today})
			var le *LoadError
			if !errors.As(err, &le) {
				t.Fatalf("err = %v, want *LoadError", err)
			}
			if le.Stage != c.stage {
				t.Errorf("Stage = %q, want %q", le.Stage, c.stage)
			}
			if !errors.Is(err, sentinel) {
				t.Error("Unwrap 没透出原始底层错误")
			}
			if meta.configCalls != 0 || meta.circleCalls != 0 {
				t.Error("读库失败时不应去取配置或词性表")
			}
		})
	}
}

// 回炉库读失败 / 返回 nil 一律当「今天不加餐」：基础句照出，fail-open 不许把练习弄没。
func TestComposeSentencesReviewFailOpen(t *testing.T) {
	cases := []struct {
		name string
		set  func(*fakeSentenceStore)
	}{
		{"读取报错", func(f *fakeSentenceStore) { f.reviewErr = errors.New("cos down") }},
		{"返回空指针", func(f *fakeSentenceStore) { f.review = nil }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, meta, cfg, today := composeFixture()
			c.set(store)

			res, err := ComposeSentences(context.Background(), store, meta, Params{Today: today})
			if err != nil {
				t.Fatalf("ComposeSentences: %v", err)
			}
			want := sentence.BuildSentencePlan(store.bank, store.hist, store.wrong,
				today, cfg.SentenceBaseCount)
			if !reflect.DeepEqual(res.Sentences, want) {
				t.Fatalf("fail-open 句表变了\n got = %+v\nwant = %+v", res.Sentences, want)
			}
			assertContinuousNumbers(t, res.Sentences)
			if res.ReviewCount != 0 || res.ReviewBank != nil || len(res.ReviewExtra) != 0 {
				t.Errorf("不该有加餐: %+v", res)
			}
			if res.Circle != nil {
				t.Error("没加餐时 Circle 应为 nil")
			}
			if meta.circleCalls != 0 {
				t.Errorf("回炉库没拿到时读了词性表 %d 次（破坏懒语义）", meta.circleCalls)
			}
			if meta.configCalls != 1 {
				t.Errorf("配置仍应无条件取一次，实际 %d 次", meta.configCalls)
			}
		})
	}
}

// 空回炉库 ≠ 没有回炉库：原编排照样读词性表、只是挑出 0 句。这条别「顺手优化」掉。
func TestComposeSentencesEmptyReviewBankStillLoadsCircle(t *testing.T) {
	store, meta, cfg, today := composeFixture()
	store.review = &sentencereview.ReviewBank{}

	res, err := ComposeSentences(context.Background(), store, meta, Params{Today: today})
	if err != nil {
		t.Fatalf("ComposeSentences: %v", err)
	}
	if res.ReviewBank == nil {
		t.Error("空回炉库也应记进 Result（原行为按 rb != nil 判定）")
	}
	if res.ReviewCount != 0 || len(res.ReviewExtra) != 0 {
		t.Errorf("空库不该挑出加餐句: %+v", res)
	}
	if meta.circleCalls != 1 {
		t.Errorf("有回炉库就该读词性表，实际 %d 次", meta.circleCalls)
	}
	want := sentence.BuildSentencePlan(store.bank, store.hist, store.wrong,
		today, cfg.SentenceBaseCount)
	if !reflect.DeepEqual(res.Sentences, want) {
		t.Fatalf("句表变了\n got = %+v\nwant = %+v", res.Sentences, want)
	}
}

// 句库缺失时 BuildSentencePlan 返回 nil：不能凭空造题，也不能 panic。
func TestComposeSentencesNilBank(t *testing.T) {
	store, meta, _, today := composeFixture()
	store.bank = nil
	store.review = nil

	res, err := ComposeSentences(context.Background(), store, meta, Params{Today: today, BaseCount: 4})
	if err != nil {
		t.Fatalf("ComposeSentences: %v", err)
	}
	if len(res.Sentences) != 0 || res.BaseCount != 0 {
		t.Errorf("句库为空不该出题: %+v", res)
	}
	if res.CircleRatio != 40 {
		t.Errorf("CircleRatio 仍应算好给 CLI: %d", res.CircleRatio)
	}
}
