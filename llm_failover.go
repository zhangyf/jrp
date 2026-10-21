package main

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

// 判句模型自动切换（2026-10-15 老师要求）。
//
// 背景：主模型 deepseek-v4-flash-0731 在网关里已经标成 pre-offline；换成原厂直供的
// DeepSeek-V4.1-Flash 后，线上连打两句还是掉了一次（返回 by:"literal" 退回字面）。
// 只有一个模型时它一挂就没得判，所以串成一条链：主失败就换备用，全挂才退回字面。
//
// 三条原则：
//  1. 什么算失败：网络错、非 2xx、空内容、超时，以及**模型胡说解析不出 JSON**。
//     最后一条最容易漏 —— 模型回一段废话也是失败，必须换下一个。
//  2. 冷却：连续失败 2 次的模型 5 分钟内不再试。少了这条，每一句都要先干等它超时，
//     老师那边看到的就是判句一直转圈。
//  3. 全挂不报错：退回字面判定，跟没配模型时一模一样。模型不可用是降级，不是故障。
//
// 主从顺序全部来自环境变量，改链条不用重新编译：
//
//	JRP_LLM_MODEL     主模型
//	JRP_LLM_FALLBACK  备用模型，逗号分隔（去空、去重、自动排除主模型）

const (
	llmFailThreshold = 2
	llmCooldown      = 2 * time.Minute
)

// parseFallbackModels 解析备用模型列表。纯函数，可单测。
func parseFallbackModels(raw, primary string) []string {
	seen := map[string]bool{}
	if p := strings.TrimSpace(primary); p != "" {
		seen[p] = true
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ---------- 健康状态（冷却） ----------

var llmHealth = struct {
	sync.Mutex
	m map[string]*llmModelHealth
}{m: map[string]*llmModelHealth{}}

type llmModelHealth struct {
	fails     int
	coolUntil time.Time
}

// llmModelCooling 这个模型现在该不该跳过。抽成纯函数是为了能单测。
func llmModelCooling(fails int, coolUntil, now time.Time) bool {
	if fails < llmFailThreshold {
		return false
	}
	return now.Before(coolUntil)
}

func llmSkipModel(model string, now time.Time) bool {
	llmHealth.Lock()
	defer llmHealth.Unlock()
	h, ok := llmHealth.m[model]
	if !ok {
		return false
	}
	return llmModelCooling(h.fails, h.coolUntil, now)
}

func llmMarkResult(model string, ok bool, now time.Time) {
	llmHealth.Lock()
	defer llmHealth.Unlock()
	h, found := llmHealth.m[model]
	if !found {
		h = &llmModelHealth{}
		llmHealth.m[model] = h
	}
	if ok {
		h.fails, h.coolUntil = 0, time.Time{}
		return
	}
	h.fails++
	if h.fails >= llmFailThreshold {
		h.coolUntil = now.Add(llmCooldown)
	}
}

// ---------- 切换 ----------

// llmChat 通用的「问模型一句话」，按「主 → 备」顺序问，返回原文和生效的模型名。
//
// 跟 judgeWithFallback 的区别：那边要对答句做判定 JSON 解析，这边只要原文
// （查中文释义这类用途），解析交给调用方。冷却和失败留痕两边共用。
func llmChat(cfg llmConfig, prompt string) (string, string, error) {
	order := append([]string{cfg.Model}, cfg.Fallback...)
	var lastErr error
	tried := 0
	for _, m := range order {
		now := time.Now()
		if llmSkipModel(m, now) {
			continue
		}
		tried++
		c := cfg
		c.Model = m
		reply, err := callLLM(c, prompt)
		if err == nil && strings.TrimSpace(reply) != "" {
			llmMarkResult(m, true, now)
			return strings.TrimSpace(reply), m, nil
		}
		lastErr = err
		llmMarkResult(m, false, now)
		log.Printf("llm-chat: 模型 %s 失败：%v", m, err)
	}
	if tried == 0 {
		return "", "", errors.New("所有模型都在冷却中")
	}
	if lastErr == nil {
		lastErr = errors.New("模型返回空内容")
	}
	return "", "", lastErr
}

// judgeWithFallback 按「主 → 备」顺序问，谁答得出来用谁。
// 返回实际生效的模型名，写进响应里便于排查 —— 老师问「现在用的是哪个」时能一眼看到。
// 全部失败返回 error，调用方退回字面判定。
func judgeWithFallback(cfg llmConfig, prompt string) (judgeResult, string, error) {
	order := append([]string{cfg.Model}, cfg.Fallback...)
	var lastErr error
	tried := 0
	for _, m := range order {
		now := time.Now()
		if llmSkipModel(m, now) {
			continue
		}
		tried++
		c := cfg
		c.Model = m
		reply, err := callLLM(c, prompt)
		if err == nil {
			var res judgeResult
			if res, err = parseJudgeReply(reply); err == nil {
				llmMarkResult(m, true, now)
				return res, m, nil
			}
		}
		lastErr = err
		llmMarkResult(m, false, now)
		// 每次失败都留痕 —— 2026-10-15 网关抖了几分钟，全链冷却，老师判句全退回
		// 字面判定，服务端却一个字都没记，只能靠猜。有了这行，journalctl 一翻就知道
		// 当时是超时、5xx 还是模型胡说。
		log.Printf("judge-failover: 模型 %s 失败：%v", m, err)
	}
	if tried == 0 {
		log.Printf("judge-failover: 全部模型都在冷却中，本句退回字面判定")
		return judgeResult{}, "", errors.New("所有判句模型都在冷却中")
	}
	return judgeResult{}, "", lastErr
}
