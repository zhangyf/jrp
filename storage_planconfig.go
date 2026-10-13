package main

import (
	"context"
)

// 出题配额配置，存在 COS 上 —— 改配额不用改代码、不用重新部署。
//
// 2026-10-13 老师提的：改个「每天几句回炉句」都要重新编译上线 + 推 GitHub，太重。
// judge prompt 已经趟过一遍外置的路子（configs/judge_prompt.txt），这里照抄同一套：
// 读不到 / 解析坏 / 值越界一律回退内置默认值，练习绝不会因为一份坏配置卡住。
//
// 缓存 60 秒（见 plan_config.go 的 resolvePlanConfig），改完配置最多 60 秒生效，
// 不用重启服务。

const planConfigCosKey = "configs/plan_config.json"

// PlanConfig 出题配额。零值表示「没配」，调用方回退内置默认（见 choosePlanConfig）。
type PlanConfig struct {
	SentenceBaseCount   int `json:"sentence_base_count"`
	SentenceReviewCount int `json:"sentence_review_count"`
}

func (s *Storage) planConfigKey() string { return s.cosPrefix() + "/" + planConfigCosKey }

// DownloadPlanConfig 读配置。对象不存在或读失败返回零值（=没配），不返回 error ——
// 「没配」是正常状态，不是故障。
func (s *Storage) DownloadPlanConfig(ctx context.Context) (PlanConfig, error) {
	data, err := s.store.GetAll(ctx, s.planConfigKey())
	if err != nil {
		return PlanConfig{}, nil
	}
	return parsePlanConfig(data)
}

// UploadPlanConfig 写配置。演练模式下不落盘，跟其余 Upload* 保持一致。
func (s *Storage) UploadPlanConfig(ctx context.Context, c PlanConfig) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.planConfigKey(), []byte(toJSON(c)))
}

// DeletePlanConfig 删配置（--reset），删掉即回退内置默认。
// 对象本来就不存在时 COS 返回 404，这里也当成功。
func (s *Storage) DeletePlanConfig(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.planConfigKey())
}
