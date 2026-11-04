package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zhangyf/jrp/internal/stats"
)

// StatsResult 统计结果类型已迁 internal/stats，根包留别名 ——
// runStats 与 api_stats.go 继续用这个名字，JSON 序列化形态不变。
type StatsResult = stats.StatsResult

// 编译期断言：*Storage 满足统计用例的最小依赖面（ListArchiveRefs + ReadArchiveBlob）。
var _ stats.StatsArchiveReader = (*Storage)(nil)

// ComputeStats 统计最近 days 天。CLI 与 HTTP handler 共用，两边都只读。
// 用例已迁 internal/stats；这里只注入 now 时钟后转发。
func ComputeStats(ctx context.Context, storage *Storage, lang string, days int) (*StatsResult, error) {
	return stats.ComputeStats(ctx, storage, lang, days, time.Now())
}

func runStats(fs *flag.FlagSet, lang string) {
	days := fs.Int("days", 7, "Number of days to look back")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	res, err := ComputeStats(ctx, storage, lang, *days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error computing stats: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "stats",
		"language":  lang,
		"days":      *days,
		"snapshots": res.Snapshots,
		"changes":   res.Changes,
		"detail":    res.Detail,
		// 与 /api/stats 对齐（api_stats.go 里也带 jlpt）。非日语为 nil，序列化成 null。
		"jlpt": res.Jlpt,
	})
}
