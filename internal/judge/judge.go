package judge

import (
	"context"
	"sync"
	"time"
)

// 模板装载与判定缓存都是「进程内内存缓存，不是外部 IO」——随用例一起搬进本包，
// 不注入接口（分层设计 5.1）。整个进程只有一份本包，语义跟原来根包的包级单例完全一致：
//   - 模板缓存 60 秒：判定每次输入都可能触发，不能每句都打一次 COS。
//   - 判定缓存按 (模板版本, 原句, 学生写的)：刷新、换设备重开都得到同一个结论。

const promptTTL = 60 * time.Second

var promptCache = struct {
	sync.Mutex
	at      time.Time
	tpl     string
	version string
	source  string // "cos" 或 "builtin"
}{}

// ResolvePrompt 拿当前生效的模板：COS 优先，坏了就用内置。
// 缓存 60 秒 —— 判定在每次输入后都可能触发，不能每句都打一次 COS。
func ResolvePrompt(ctx context.Context, store ConfigStore) (tpl, version, source string) {
	promptCache.Lock()
	if time.Since(promptCache.at) < promptTTL && promptCache.tpl != "" {
		t, v, src := promptCache.tpl, promptCache.version, promptCache.source
		promptCache.Unlock()
		return t, v, src
	}
	promptCache.Unlock()

	tpl, source = ChoosePrompt(store.DownloadJudgePrompt(ctx))
	version = PromptVersion(tpl)

	promptCache.Lock()
	promptCache.at, promptCache.tpl = time.Now(), tpl
	promptCache.version, promptCache.source = version, source
	promptCache.Unlock()
	return tpl, version, source
}

// cacheEntry 连模型名一起存 —— 命中缓存时也要能说出这句是谁判的，
// 否则排查「用的哪个模型」会看到一片空，误以为没走模型。
type cacheEntry struct {
	res   Result
	model string
}

var judgeCache = struct {
	sync.Mutex
	m map[string]cacheEntry
}{m: map[string]cacheEntry{}}

// JudgeWithCache 判定一句，顺带返回这次用的模板版本（前端存进草稿，
// 下次恢复时对不上就作废重判）。返回顺序与根包旧 judgeWithCache 一致：
// (结果, 模板版本, 生效模型名, 错误)。
//
// 主→备切换 / 冷却 / 真发 HTTP 都在 Caller.Judge 的实现里（留根 IO 层，
// 模型配置由实现自己持有）；本函数只编排「取模板 → 算键 → 查缓存 →
// 未命中才问模型 → 写缓存」，完全不认识 llm 包。
func JudgeWithCache(ctx context.Context, store ConfigStore, caller Caller,
	answer, chinese, input string) (Result, string, string, error) {

	tpl, version, _ := ResolvePrompt(ctx, store)
	key := CacheKey(version, answer, input)
	judgeCache.Lock()
	if e, ok := judgeCache.m[key]; ok {
		judgeCache.Unlock()
		return e.res, version, e.model, nil
	}
	judgeCache.Unlock()

	r, model, err := caller.Judge(RenderPrompt(tpl, answer, chinese, input))
	if err != nil {
		return Result{}, version, "", err
	}
	judgeCache.Lock()
	judgeCache.m[key] = cacheEntry{res: r, model: model}
	judgeCache.Unlock()
	return r, version, model, nil
}
