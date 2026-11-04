package record

import (
	"context"

	"github.com/zhangyf/jrp/internal/offline"
)

// ArchiveStore 是 Apply 对档案存储的最小依赖面。
// 根包 *Storage 隐式满足。
type ArchiveStore interface {
	DownloadLatestArchive(ctx context.Context) ([]byte, string, error)
	UploadArchive(ctx context.Context, filename string, data []byte) error
}

// PlanStore 是 Apply 对复习计划存储的最小依赖面。
// 根包 *Storage 隐式满足。
type PlanStore interface {
	DownloadPlan(ctx context.Context, date string) (*offline.ReviewPlan, error)
	DownloadHardPlan(ctx context.Context, date string) (*offline.ReviewPlan, error)
}
