package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// 出题配额：COS 上没配 / 配坏了就用这两个数。
//
// 跟 2026-10-13 之前的硬编码保持一致（20 句课本原文 + 20 句回炉变形句），
// 所以这次改动本身不改变线上行为 —— 外置的是「能不能改」，不是「默认值」。
const (
	defaultSentenceBaseCount   = 20
	defaultSentenceReviewCount = 20
)

// maxPlanCount 配额上限。写个 5000 上去会一次拖 5000 句，页面直接卡死，
// 这种值一律判坏回退默认。
const maxPlanCount = 50

// parsePlanConfig 解析配置。任何异常都返回零值 —— 零值 = 「没配」。
func parsePlanConfig(data []byte) (PlanConfig, error) {
	if len(data) == 0 {
		return PlanConfig{}, nil
	}
	var c PlanConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return PlanConfig{}, err
	}
	return c, nil
}

// builtinPlanConfig 内置默认配额。
func builtinPlanConfig() PlanConfig {
	return PlanConfig{
		SentenceBaseCount:   defaultSentenceBaseCount,
		SentenceReviewCount: defaultSentenceReviewCount,
	}
}

// validPlanCount 值是否可用。
//
// 0 是合法的 —— 想临时关掉某一类句子（比如不出回炉句）就写 0，
// BuildReviewSentences / BuildSentencePlan 收到 0 就一句不出。
// 负数判坏：那不是「有意关掉」，多半是手滑写错。
func validPlanCount(n int) bool { return n >= 0 && n <= maxPlanCount }

// choosePlanConfig 在「COS 上读到的」和「内置的」之间做选择。抽成纯函数是为了能单测 ——
// 护栏全在这里：读失败、空的、解析坏、值越界，一律回退内置，练习绝不卡住。
func choosePlanConfig(cos PlanConfig, err error) (PlanConfig, string) {
	if err != nil {
		log.Printf("plan-config: COS 配置读不了（%v），用内置默认", err)
		return builtinPlanConfig(), "builtin"
	}
	if cos.SentenceBaseCount == 0 && cos.SentenceReviewCount == 0 {
		return builtinPlanConfig(), "builtin" // 没配过
	}
	if !validPlanCount(cos.SentenceBaseCount) || !validPlanCount(cos.SentenceReviewCount) {
		log.Printf("plan-config: COS 配置越界（base=%d review=%d，允许 0-%d），用内置默认",
			cos.SentenceBaseCount, cos.SentenceReviewCount, maxPlanCount)
		return builtinPlanConfig(), "builtin"
	}
	return cos, "cos"
}

// ---------- 装载（带缓存） ----------

const planConfigTTL = 60 * time.Second

var planConfigCache = struct {
	sync.Mutex
	at     time.Time
	loaded bool // 光看 cfg 区分不出「缓存过 {0,0}」和「没缓存过」
	cfg    PlanConfig
	source string // "cos" 或 "builtin"
}{}

// resolvePlanConfig 拿当前生效的配额：COS 优先，坏了就用内置。
// 缓存 60 秒 —— 出题在每次打开页面 / 每次生成离线 Excel 时都会走这里，
// 不能每次都打 COS。副作用是改完配置最多 60 秒才生效，这比重启服务划算。
func resolvePlanConfig(ctx context.Context, st *Storage) (PlanConfig, string) {
	planConfigCache.Lock()
	if planConfigCache.loaded && time.Since(planConfigCache.at) < planConfigTTL {
		c, src := planConfigCache.cfg, planConfigCache.source
		planConfigCache.Unlock()
		return c, src
	}
	planConfigCache.Unlock()

	cfg, source := choosePlanConfig(st.DownloadPlanConfig(ctx))

	planConfigCache.Lock()
	planConfigCache.at, planConfigCache.loaded = time.Now(), true
	planConfigCache.cfg, planConfigCache.source = cfg, source
	planConfigCache.Unlock()
	return cfg, source
}
