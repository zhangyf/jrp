package main

// storage_listening.go —— 听力满格目标的 COS 读写。
//
// 规则（解析/校验/回退）在 internal/listening（goal.go），本文件只做 IO，
// 跟 storage_planconfig.go 同一套分工。key 是共享配置（configs/ 下，全站一份），
// 不随用户变 —— 满格目标是教材口径，不是某个人的进度。

import (
	"context"

	"github.com/zhangyf/jrp/internal/listening"
)

// listeningGoalCosKey 满格目标在 COS 上的位置（相对 sharedBase）。
const listeningGoalCosKey = "configs/listening_goal.json"

// Goal 类型别名，跟根包其他 DTO 一致（消费方继续用短名）。
type Goal = listening.Goal

func (s *Storage) listeningGoalKey() string {
	return s.sharedBase() + "/" + listeningGoalCosKey
}

// DownloadListeningGoal 读满格目标。对象不存在或读失败返回零值（=没配），不返回 error。
func (s *Storage) DownloadListeningGoal(ctx context.Context) (Goal, error) {
	data, err := s.store.GetAll(ctx, s.listeningGoalKey())
	if err != nil {
		return Goal{}, nil
	}
	return listening.ParseGoal(data)
}

// UploadListeningGoal 写满格目标。演练模式下不落盘。
func (s *Storage) UploadListeningGoal(ctx context.Context, g Goal) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.listeningGoalKey(), []byte(toJSON(g)))
}

// DeleteListeningGoal 删满格目标（--reset），删掉即回退内置默认。404 当成功。
func (s *Storage) DeleteListeningGoal(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	return s.deleteObjectIgnore404(ctx, s.listeningGoalKey())
}
