package llm

import (
	"sync"
	"time"
)

const (
	failThreshold = 2
	cooldown      = 2 * time.Minute
)

// health 按模型名保存进程级共享状态，判句、词义和视觉共用，不按网关或调用方隔离。
// 什么算成功仍由业务调用方判断，本包只负责记录与冷却。
var health = struct {
	sync.Mutex
	m map[string]*modelHealth
}{m: map[string]*modelHealth{}}

type modelHealth struct {
	fails     int
	coolUntil time.Time
}

// modelCooling 判断模型是否仍在冷却，保留纯函数便于测试时间边界。
func modelCooling(fails int, coolUntil, now time.Time) bool {
	if fails < failThreshold {
		return false
	}
	return now.Before(coolUntil)
}

// SkipModel 报告模型是否应被跳过。冷却到期只允许试探，不自动清除失败计数。
func SkipModel(model string, now time.Time) bool {
	health.Lock()
	defer health.Unlock()
	h, ok := health.m[model]
	if !ok {
		return false
	}
	return modelCooling(h.fails, h.coolUntil, now)
}

// MarkResult 记录调用方给出的结果；成功清零，连续失败两次后冷却两分钟。
// now 沿用调用方原有时间点，后续失败会再次从该时间点顺延冷却。
func MarkResult(model string, ok bool, now time.Time) {
	health.Lock()
	defer health.Unlock()
	h, found := health.m[model]
	if !found {
		h = &modelHealth{}
		health.m[model] = h
	}
	if ok {
		h.fails, h.coolUntil = 0, time.Time{}
		return
	}
	h.fails++
	if h.fails >= failThreshold {
		h.coolUntil = now.Add(cooldown)
	}
}
