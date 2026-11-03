// Package planconfig 出题配额的纯规则层：解析、内置默认、合法性校验与回退选择。
// 只依赖标准库，不读写 COS、不碰 Storage —— 装载缓存与 CLI 编排留在根包。
package planconfig

import (
	"encoding/json"
	"log"
)

// 出题配额：COS 上没配 / 配坏了就用这两个数。
//
// 跟 2026-10-13 之前的硬编码保持一致（20 句课本原文 + 20 句回炉变形句），
// 所以这次改动本身不改变线上行为 —— 外置的是「能不能改」，不是「默认值」。
const (
	DefaultSentenceBaseCount   = 20
	DefaultSentenceReviewCount = 20

	// DefaultCircleRatio 回炉句里三圆词的名额占比（%）。
	// 70 = 「占大头不独占」：20 句里 14 句给三圆，剩 6 句留给二圆 / 一圆 / 没标，
	// 免得低频词永远排不上号。想关掉就 --set sentence_review_circle_ratio=0。
	DefaultCircleRatio = 70
)

// MaxPlanCount 配额上限。写个 5000 上去会一次拖 5000 句，页面直接卡死，
// 这种值一律判坏回退默认。
const MaxPlanCount = 50

// PlanConfig 出题配额。零值表示「没配」，调用方回退内置默认（见 ChoosePlanConfig）。
type PlanConfig struct {
	SentenceBaseCount   int `json:"sentence_base_count"`
	SentenceReviewCount int `json:"sentence_review_count"`

	// SentenceReviewCircleRatio 回炉变形句里三圆词占的名额百分比（0-100）。
	// 用指针是为了分开「没配过」（nil → 用内置 70）和「有意关掉」（0 → 不分层）。
	SentenceReviewCircleRatio *int `json:"sentence_review_circle_ratio,omitempty"`
}

// ParsePlanConfig 解析配置。任何异常都返回零值 —— 零值 = 「没配」。
func ParsePlanConfig(data []byte) (PlanConfig, error) {
	if len(data) == 0 {
		return PlanConfig{}, nil
	}
	var c PlanConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return PlanConfig{}, err
	}
	return c, nil
}

// BuiltinPlanConfig 内置默认配额。
func BuiltinPlanConfig() PlanConfig {
	ratio := DefaultCircleRatio
	return PlanConfig{
		SentenceBaseCount:         DefaultSentenceBaseCount,
		SentenceReviewCount:       DefaultSentenceReviewCount,
		SentenceReviewCircleRatio: &ratio,
	}
}

// PlanCircleRatio 取生效的三圆名额占比。没配 / 越界都用内置 70。
func PlanCircleRatio(c PlanConfig) int {
	if c.SentenceReviewCircleRatio == nil {
		return DefaultCircleRatio
	}
	if v := *c.SentenceReviewCircleRatio; v >= 0 && v <= 100 {
		return v
	}
	return DefaultCircleRatio
}

// ValidCircleRatio 值是否可用。nil（没配）合法，配了就必须在 0-100。
func ValidCircleRatio(c PlanConfig) bool {
	if c.SentenceReviewCircleRatio == nil {
		return true
	}
	v := *c.SentenceReviewCircleRatio
	return v >= 0 && v <= 100
}

// ValidPlanCount 值是否可用。
//
// 0 是合法的 —— 想临时关掉某一类句子（比如不出回炉句）就写 0，
// BuildReviewSentences / BuildSentencePlan 收到 0 就一句不出。
// 负数判坏：那不是「有意关掉」，多半是手滑写错。
func ValidPlanCount(n int) bool { return n >= 0 && n <= MaxPlanCount }

// ChoosePlanConfig 在「COS 上读到的」和「内置的」之间做选择。抽成纯函数是为了能单测 ——
// 护栏全在这里：读失败、空的、解析坏、值越界，一律回退内置，练习绝不卡住。
func ChoosePlanConfig(cos PlanConfig, err error) (PlanConfig, string) {
	if err != nil {
		log.Printf("plan-config: COS 配置读不了（%v），用内置默认", err)
		return BuiltinPlanConfig(), "builtin"
	}
	if cos.SentenceBaseCount == 0 && cos.SentenceReviewCount == 0 {
		return BuiltinPlanConfig(), "builtin" // 没配过
	}
	if !ValidPlanCount(cos.SentenceBaseCount) || !ValidPlanCount(cos.SentenceReviewCount) || !ValidCircleRatio(cos) {
		log.Printf("plan-config: COS 配置越界（base=%d review=%d circle_ratio=%v，允许 count 0-%d / ratio 0-100），用内置默认",
			cos.SentenceBaseCount, cos.SentenceReviewCount, cos.SentenceReviewCircleRatio, MaxPlanCount)
		return BuiltinPlanConfig(), "builtin"
	}
	return cos, "cos"
}
