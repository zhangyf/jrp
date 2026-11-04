package words

import (
	"context"

	"github.com/zhangyf/jrp/internal/archive"
)

// ArchiveStore 是「录生词」用例需要的档案 IO 能力。
//
// 由根包 *Storage 实现（Archive 是 archive.Archive 的类型别名，方法签名天然匹配），
// 单测传内存 fake 即可，用例层不认识 COS / objstore / *Storage。
// 只读/只写各一个方法，是接口隔离：这个用例不需要 LoadArchiveForRead、Draft 等其它能力。
type ArchiveStore interface {
	// EnsureArchive 拿最新档案；目录里没有就造一份空的并落盘。
	// 返回解析好的档案与它的文件名（算版本号要用）。
	EnsureArchive(ctx context.Context, lang string) (*archive.Archive, string, error)
	// UploadArchive 写回一份新档案。dryRun 由实现方内部处理，用例不感知。
	UploadArchive(ctx context.Context, filename string, data []byte) error
}
