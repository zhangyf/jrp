package worddef

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

// testArchive 走 archive.WriteArchive 生成真实可解析的档案文本（round-trip 由 archive 包自测）。
func testArchive(groups []review.WordGroup) string {
	return archive.WriteArchive(&archive.Archive{
		Language: "ja",
		Title:    "日语单词学习进度档案",
		Changelog: []archive.ChangelogEntry{
			{Date: "03/15", Version: "v1.0", Total: "0", Description: "初始化"},
		},
		Groups: groups,
	}, "日语", "日语单词")
}

// fakeArchive 只实现用例层需要的那一个方法，不碰 COS。
type fakeArchive struct {
	text  string
	err   error
	calls int
}

func (f *fakeArchive) DownloadLatestArchive(ctx context.Context) ([]byte, string, error) {
	f.calls++
	return []byte(f.text), "latest.md", f.err
}

// fakeCaller 只实现用例层需要的那一个方法，不碰 LLM / 冷却 / 网络。
type fakeCaller struct {
	reply   string
	model   string
	err     error
	calls   int
	prompts []string
}

func (f *fakeCaller) Lookup(prompt string) (string, string, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	return f.reply, f.model, f.err
}

func assertItems(t *testing.T, got, want []Item) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("结果不符：\n got %+v\nwant %+v", got, want)
	}
}

// 档案精确命中 + 按假名命中：不带括号的「みず」要补回「みず(水)」，且不调模型。
func TestLookupArchiveHitNoModel(t *testing.T) {
	arc := &fakeArchive{text: testArchive([]review.WordGroup{{Title: "第1课", Words: []review.Word{
		{Word: "みず(水)", Definition: "水"},
		{Word: "たべもの", Definition: "食物"},
	}}})}
	caller := &fakeCaller{}

	got, model := Lookup(context.Background(), arc, caller, "ja",
		[]string{"みず", "たべもの", "みず(水)"})

	assertItems(t, got, []Item{
		{Input: "みず", Word: "みず(水)", Def: "水"},
		{Input: "たべもの", Word: "たべもの", Def: "食物"},
		{Input: "みず(水)", Word: "みず(水)", Def: "水"},
	})
	if model != "" {
		t.Errorf("档案全命中不该有模型名，得到 %q", model)
	}
	if caller.calls != 0 {
		t.Errorf("档案全命中不该调模型，调了 %d 次", caller.calls)
	}
	if arc.calls != 1 {
		t.Errorf("档案应该只读一次，读了 %d 次", arc.calls)
	}
}

// 档案读失败（COS 挂了）→ 全部词转去问模型，降级不是故障。
func TestLookupArchiveReadFailsGoesToModel(t *testing.T) {
	arc := &fakeArchive{err: errors.New("COS 挂了")}
	caller := &fakeCaller{
		reply: `[{"word":"みず","kanji":"水","def":"水"}]`,
		model: "primary",
	}

	got, model := Lookup(context.Background(), arc, caller, "ja", []string{"みず"})

	assertItems(t, got, []Item{{Input: "みず", Word: "みず(水)", Def: "水"}})
	if model != "primary" {
		t.Errorf("模型名应为 primary，得到 %q", model)
	}
	if caller.calls != 1 {
		t.Errorf("应该问一次模型，问了 %d 次", caller.calls)
	}
}

// 模型不可用（没配 / 调用失败 / 全链冷却）：Def 留空，enabled 靠 model=="" 表达，不算错误。
func TestLookupModelUnavailableLeavesBlank(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"没配模型", errors.New("没配置大模型")},
		{"全链失败", errors.New("所有模型都在冷却中")},
	} {
		t.Run(c.name, func(t *testing.T) {
			arc := &fakeArchive{text: testArchive(nil)}
			caller := &fakeCaller{err: c.err}

			got, model := Lookup(context.Background(), arc, caller, "ja",
				[]string{"みず", "たべもの"})

			assertItems(t, got, []Item{
				{Input: "みず", Word: "みず"},
				{Input: "たべもの", Word: "たべもの"},
			})
			if model != "" {
				t.Errorf("模型不可用时不该有模型名，得到 %q", model)
			}
		})
	}
}

// 模型正常回答：释义填上，汉字补成半角括号词形。
func TestLookupModelSuccessAddsKanji(t *testing.T) {
	arc := &fakeArchive{text: testArchive(nil)}
	caller := &fakeCaller{
		reply: `[{"word":"みず","kanji":"水","def":"水"},{"word":"さん","kanji":"","def":"称呼"}]`,
		model: "primary",
	}

	got, model := Lookup(context.Background(), arc, caller, "ja",
		[]string{"みず", "さん"})

	assertItems(t, got, []Item{
		{Input: "みず", Word: "みず(水)", Def: "水"},
		{Input: "さん", Word: "さん", Def: "称呼"},
	})
	if model != "primary" {
		t.Errorf("模型名应为 primary，得到 %q", model)
	}
}

// 老师自己带了括号（含全角）就别再补一层括号。
func TestLookupModelAlreadyHasParen(t *testing.T) {
	arc := &fakeArchive{text: testArchive(nil)}
	caller := &fakeCaller{
		reply: `[{"word":"みず（敬称）","kanji":"水","def":"水"}]`,
		model: "primary",
	}

	got, _ := Lookup(context.Background(), arc, caller, "ja", []string{"みず（敬称）"})

	assertItems(t, got, []Item{{Input: "みず（敬称）", Word: "みず（敬称）", Def: "水"}})
}

// 模型回了但 JSON 格式不对：model 仍然生效（原逻辑 model 在解析之前赋值），
// 只是 todo 里的词按「没有释义」补一条，不阻断录入。
func TestLookupModelBadJSONKeepsModel(t *testing.T) {
	arc := &fakeArchive{text: testArchive(nil)}
	caller := &fakeCaller{reply: "模型胡说一通", model: "primary"}

	got, model := Lookup(context.Background(), arc, caller, "ja", []string{"みず"})

	assertItems(t, got, []Item{{Input: "みず", Word: "みず"}})
	if model != "primary" {
		t.Errorf("模型回了就应记下名字（enabled=true），得到 %q", model)
	}
}

// 模型漏答某个词：漏的按「没有释义」补一条。
func TestLookupModelPartialAnswer(t *testing.T) {
	arc := &fakeArchive{text: testArchive(nil)}
	caller := &fakeCaller{
		reply: `[{"word":"みず","kanji":"水","def":"水"}]`,
		model: "primary",
	}

	got, _ := Lookup(context.Background(), arc, caller, "ja",
		[]string{"みず", "たべもの"})

	assertItems(t, got, []Item{
		{Input: "みず", Word: "みず(水)", Def: "水"},
		{Input: "たべもの", Word: "たべもの"},
	})
}

// 档案命中和模型回答混排时，输出严格按入参顺序；档案口径优先于模型。
func TestLookupPreservesOrderMixedSources(t *testing.T) {
	arc := &fakeArchive{text: testArchive([]review.WordGroup{{Title: "第1课", Words: []review.Word{
		{Word: "たべもの", Definition: "食物（档案）"},
	}}})}
	caller := &fakeCaller{
		reply: `[{"word":"みず","kanji":"水","def":"水（模型）"},` +
			`{"word":"たべもの","kanji":"食べ物","def":"食物（模型）"},` +
			`{"word":"さん","kanji":"","def":"称呼"}]`,
		model: "primary",
	}

	got, _ := Lookup(context.Background(), arc, caller, "ja",
		[]string{"みず", "たべもの", "さん"})

	assertItems(t, got, []Item{
		{Input: "みず", Word: "みず(水)", Def: "水（模型）"},
		{Input: "たべもの", Word: "たべもの", Def: "食物（档案）"},
		{Input: "さん", Word: "さん", Def: "称呼"},
	})
	if len(caller.prompts) != 1 || strings.Contains(caller.prompts[0], "- たべもの") {
		t.Errorf("档案命中的词不该进模型 prompt：%+v", caller.prompts)
	}
}

// 档案里释义是空白 → 不算命中，转去问模型（TrimSpace 口径）。
func TestLookupBlankDefinitionGoesToModel(t *testing.T) {
	arc := &fakeArchive{text: testArchive([]review.WordGroup{{Title: "第1课", Words: []review.Word{
		{Word: "みず(水)", Definition: "   "},
	}}})}
	caller := &fakeCaller{
		reply: `[{"word":"みず","kanji":"水","def":"水"}]`,
		model: "primary",
	}

	got, _ := Lookup(context.Background(), arc, caller, "ja", []string{"みず"})

	assertItems(t, got, []Item{{Input: "みず", Word: "みず(水)", Def: "水"}})
	if caller.calls != 1 {
		t.Errorf("空释义应转去问模型，调了 %d 次", caller.calls)
	}
}

// prompt 只带 todo 词，且模板里 {words} 已替换。
func TestLookupPromptOnlyCarriesTodo(t *testing.T) {
	arc := &fakeArchive{text: testArchive([]review.WordGroup{{Title: "第1课", Words: []review.Word{
		{Word: "たべもの", Definition: "食物"},
	}}})}
	caller := &fakeCaller{
		reply: `[{"word":"みず","kanji":"水","def":"水"}]`,
		model: "primary",
	}

	_, _ = Lookup(context.Background(), arc, caller, "ja", []string{"みず", "たべもの"})

	if len(caller.prompts) != 1 {
		t.Fatalf("应该问一次模型，问了 %d 次", caller.calls)
	}
	p := caller.prompts[0]
	if !strings.Contains(p, "- みず") {
		t.Error("todo 词没进 prompt：" + p)
	}
	if strings.Contains(p, "- たべもの") {
		t.Error("档案命中的词不该进 prompt：" + p)
	}
	if strings.Contains(p, "{words}") {
		t.Error("占位符没替换：" + p)
	}
}
