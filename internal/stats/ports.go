package stats

import (
	"context"
	"time"
)

// ArchiveRef 是统计用例可见的档案引用，不泄漏底层对象存储类型。
// Key 是完整 COS key，Filename 是去掉路径前缀后的文件名，Date 由
// 文件名解析出的档案日期。
type ArchiveRef struct {
	Key      string
	Filename string
	Date     time.Time
}

// StatsArchiveReader 是统计用例对存储的最小依赖面。
// 根包 *Storage 隐式满足（ListArchiveRefs + ReadArchiveBlob）。
type StatsArchiveReader interface {
	ListArchiveRefs(ctx context.Context) ([]ArchiveRef, error)
	ReadArchiveBlob(ctx context.Context, key string) ([]byte, error)
}
