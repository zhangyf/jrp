package main

// listening_goal.go —— 满格目标的装载缓存（60 秒），跟 plan_config.go 同套路。
//
// 统计页每次打开都读一次目标值，不能每次都打 COS；缓存 60 秒，
// 改完配置最多 60 秒生效，比重启服务划算。

import (
	"context"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/listening"
)

const listeningGoalTTL = 60 * time.Second

var listeningGoalCache = struct {
	sync.Mutex
	at     time.Time
	loaded bool
	goal   Goal
	source string
}{}

// resolveListeningGoal 拿当前生效的满格目标：COS 优先，坏了就用内置。
func resolveListeningGoal(ctx context.Context, st *Storage) (Goal, string) {
	listeningGoalCache.Lock()
	if listeningGoalCache.loaded && time.Since(listeningGoalCache.at) < listeningGoalTTL {
		g, src := listeningGoalCache.goal, listeningGoalCache.source
		listeningGoalCache.Unlock()
		return g, src
	}
	listeningGoalCache.Unlock()

	cos, err := st.DownloadListeningGoal(ctx)
	goal, source := listening.ChooseGoal(cos, err)

	listeningGoalCache.Lock()
	listeningGoalCache.at, listeningGoalCache.loaded = time.Now(), true
	listeningGoalCache.goal, listeningGoalCache.source = goal, source
	listeningGoalCache.Unlock()
	return goal, source
}
