package main

import (
	"context"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/planconfig"
)

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

	cfg, source := planconfig.ChoosePlanConfig(st.DownloadPlanConfig(ctx))

	planConfigCache.Lock()
	planConfigCache.at, planConfigCache.loaded = time.Now(), true
	planConfigCache.cfg, planConfigCache.source = cfg, source
	planConfigCache.Unlock()
	return cfg, source
}
