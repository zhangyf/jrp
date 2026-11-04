package judge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// 两个缓存都是包级单例，TTL 60 秒会让相邻测试串味。同包测试直接清干净，
// 每个用例从「没读过模板、没判过句」的状态开始。
func resetCaches() {
	promptCache.Lock()
	promptCache.at = time.Time{}
	promptCache.tpl = ""
	promptCache.version = ""
	promptCache.source = ""
	promptCache.Unlock()

	judgeCache.Lock()
	judgeCache.m = map[string]cacheEntry{}
	judgeCache.Unlock()
}

type fakeConfigStore struct {
	text  string
	err   error
	calls int
}

func (f *fakeConfigStore) DownloadJudgePrompt(ctx context.Context) (string, error) {
	f.calls++
	return f.text, f.err
}

type fakeCaller struct {
	mu      sync.Mutex
	res     Result
	model   string
	err     error
	calls   int
	prompts []string
}

func (f *fakeCaller) Judge(prompt string) (Result, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.prompts = append(f.prompts, prompt)
	return f.res, f.model, f.err
}

func (f *fakeCaller) snapshot() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := append([]string(nil), f.prompts...)
	return f.calls, p
}

var _ ConfigStore = (*fakeConfigStore)(nil)
var _ Caller = (*fakeCaller)(nil)

// ---------- ResolvePrompt：fail-open 与 60 秒缓存 ----------

func TestResolvePromptFailOpen(t *testing.T) {
	cases := []struct {
		name string
		text string
		err  error
	}{
		{"COS 读失败", "", errors.New("网络炸了")},
		{"COS 没有模板", "", nil},
		{"COS 模板是空白", "   \n  ", nil},
		{"COS 模板缺占位符", "这句话对吗？{answer}", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetCaches()
			st := &fakeConfigStore{text: c.text, err: c.err}
			tpl, ver, src := ResolvePrompt(context.Background(), st)
			if src != "builtin" {
				t.Fatalf("source = %q，坏模板必须回退内置", src)
			}
			if tpl != BuiltinPrompt {
				t.Error("回退时模板必须是内置全文")
			}
			if ver != PromptVersion(BuiltinPrompt) {
				t.Error("回退时版本必须对内置模板算")
			}
		})
	}
}

func TestResolvePromptCosTemplateAndTTL(t *testing.T) {
	resetCaches()
	good := "你是日语老师。\n参考答案：{answer}\n中文意思：{chinese}\n学生写的：{input}"
	st := &fakeConfigStore{text: good}

	tpl, ver, src := ResolvePrompt(context.Background(), st)
	if src != "cos" || tpl != good {
		t.Fatalf("正常模板应该用 COS 上的：src=%q tpl=%q", src, tpl)
	}
	if ver != PromptVersion(good) || len(ver) != 12 {
		t.Errorf("版本不对：%q", ver)
	}

	// 60 秒内第二次不该再打 COS（判定每次输入都可能触发，不能每句读一次模板）。
	tpl2, ver2, src2 := ResolvePrompt(context.Background(), st)
	if tpl2 != tpl || ver2 != ver || src2 != src {
		t.Error("缓存期内两次结果必须一致")
	}
	if st.calls != 1 {
		t.Errorf("缓存期内应只读一次 COS，实际 %d 次", st.calls)
	}

	// 强制过期后应重读，拿到新内容、新版本。
	promptCache.Lock()
	promptCache.at = time.Now().Add(-2 * promptTTL)
	promptCache.Unlock()
	changed := good + "\n7. 补充一条。"
	st.text = changed
	tpl3, ver3, _ := ResolvePrompt(context.Background(), st)
	if tpl3 != changed || ver3 != PromptVersion(changed) {
		t.Error("过期后应重读 COS 并算新版本")
	}
	if ver3 == ver {
		t.Error("模板改了版本必须变")
	}
	if st.calls != 2 {
		t.Errorf("过期后应再读一次 COS，实际共 %d 次", st.calls)
	}
}

// ---------- JudgeWithCache：命中不调模型、版本换键、错误透传 ----------

func TestJudgeWithCacheHitSkipsCaller(t *testing.T) {
	resetCaches()
	st := &fakeConfigStore{text: "参考答案：{answer}\n学生写的：{input}"}
	caller := &fakeCaller{res: Result{Correct: true, Reason: "只是换了词序"}, model: "primary"}
	ctx := context.Background()

	r1, v1, m1, err := JudgeWithCache(ctx, st, caller, "明日、京都へ行きます", "明天去京都", "あした京都に行く")
	if err != nil || !r1.Correct || r1.Reason != "只是换了词序" || m1 != "primary" {
		t.Fatalf("第一次判定不对：res=%+v model=%q err=%v", r1, m1, err)
	}
	if v1 == "" {
		t.Error("必须回传模板版本（前端存进草稿）")
	}

	r2, v2, m2, err := JudgeWithCache(ctx, st, caller, "明日、京都へ行きます", "明天去京都", "あした京都に行く")
	if err != nil || r2 != r1 || v2 != v1 || m2 != m1 {
		t.Fatalf("刷新/重开必须得到同一个结论：%+v / %+v", r1, r2)
	}
	if n, _ := caller.snapshot(); n != 1 {
		t.Errorf("命中缓存不该再问模型，实际调了 %d 次", n)
	}
}

func TestJudgeWithCacheFoldWidthSameKey(t *testing.T) {
	resetCaches()
	st := &fakeConfigStore{text: "参考答案：{answer}\n学生写的：{input}"}
	caller := &fakeCaller{res: Result{Correct: false, Reason: "主体错了"}, model: "m"}
	ctx := context.Background()

	_, _, _, _ = JudgeWithCache(ctx, st, caller, "2万円です", "", "２万円です。")
	_, _, _, _ = JudgeWithCache(ctx, st, caller, "2万円です", "", "2万円です")
	if n, _ := caller.snapshot(); n != 1 {
		t.Errorf("全角半角归一化后是同一句，不能问两次模型，实际 %d 次", n)
	}
}

func TestJudgeWithCacheVersionChangesKey(t *testing.T) {
	resetCaches()
	st := &fakeConfigStore{text: "参考答案：{answer}\n学生写的：{input}"}
	caller := &fakeCaller{res: Result{Correct: true}, model: "m"}
	ctx := context.Background()

	_, v1, _, _ := JudgeWithCache(ctx, st, caller, "甲", "", "乙")

	// 换一份模板（版本变）→ 旧缓存结论必须作废重判。
	promptCache.Lock()
	promptCache.at = time.Time{}
	promptCache.Unlock()
	st.text = "换个说法。\n参考答案：{answer}\n学生写的：{input}"
	_, v2, _, _ := JudgeWithCache(ctx, st, caller, "甲", "", "乙")

	if v1 == v2 {
		t.Fatal("模板换了版本必须变，才能触发重判")
	}
	if n, _ := caller.snapshot(); n != 2 {
		t.Errorf("版本变了应重新问模型，实际共调 %d 次", n)
	}
}

func TestJudgeWithCacheCallerErrorNotCached(t *testing.T) {
	resetCaches()
	st := &fakeConfigStore{text: "参考答案：{answer}\n学生写的：{input}"}
	caller := &fakeCaller{err: errors.New("所有模型都在冷却中")}
	ctx := context.Background()

	r, ver, model, err := JudgeWithCache(ctx, st, caller, "甲", "", "乙")
	if err == nil {
		t.Fatal("模型不可用必须把错误透传（入口据此退回字面判定）")
	}
	if r.Correct || model != "" {
		t.Errorf("失败时结果应为空、模型名为空：res=%+v model=%q", r, model)
	}
	if ver == "" {
		t.Error("失败时仍要回传模板版本，跟旧 judgeWithCache 一致")
	}

	// 失败不写缓存：下一次仍要重试模型链，不能被一次失败永久钉住。
	caller.err = nil
	caller.res = Result{Correct: true}
	_, _, _, err = JudgeWithCache(ctx, st, caller, "甲", "", "乙")
	if err != nil {
		t.Fatalf("重试应成功：%v", err)
	}
	if n, _ := caller.snapshot(); n != 2 {
		t.Errorf("上次失败不该写缓存，应重试，实际共调 %d 次", n)
	}
}

func TestJudgeWithCacheRenderedPrompt(t *testing.T) {
	resetCaches()
	st := &fakeConfigStore{text: "参考：{answer}\n中文：{chinese}\n学生：{input}"}
	caller := &fakeCaller{res: Result{Correct: true}, model: "m"}

	_, _, _, err := JudgeWithCache(context.Background(), st, caller,
		"駅へ行きます", "去车站", "駅に行きます")
	if err != nil {
		t.Fatal(err)
	}
	_, prompts := caller.snapshot()
	if len(prompts) != 1 {
		t.Fatalf("应问一次模型，实际 %d 次", len(prompts))
	}
	p := prompts[0]
	for _, must := range []string{"参考：駅へ行きます", "中文：去车站", "学生：駅に行きます", `"correct"`} {
		if !strings.Contains(p, must) {
			t.Errorf("发给模型的 prompt 缺 %q：\n%s", must, p)
		}
	}

	// 中文为空：整行删掉，不留「中文：」空壳。
	resetCaches()
	caller2 := &fakeCaller{res: Result{Correct: true}, model: "m"}
	_, _, _, _ = JudgeWithCache(context.Background(), &fakeConfigStore{
		text: "参考：{answer}\n中文：{chinese}\n学生：{input}",
	}, caller2, "甲", "", "乙")
	_, prompts2 := caller2.snapshot()
	if strings.Contains(prompts2[0], "中文：") {
		t.Error("中文为空时不该出现「中文：」行")
	}
}
