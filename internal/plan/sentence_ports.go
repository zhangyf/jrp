package plan

import (
	"context"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
)

// SentenceStore 造句出题要读的四份数据。由「用的人」（用例层）定义，
// 根包 *Storage 隐式满足（见根包 sentence_review.go 的编译期断言）。
// 回炉句库是「当天有没有加餐」的开关，必须留在用例里按需读，不能提前求值。
type SentenceStore interface {
	DownloadSentenceBank(ctx context.Context) (*sentence.SentenceBank, error)
	DownloadSentenceHistory(ctx context.Context) (sentence.SentenceHistory, error)
	DownloadSentenceWrong(ctx context.Context) (*sentence.SentenceWrong, error)
	DownloadReviewBank(ctx context.Context) (*sentencereview.ReviewBank, error)
}

// Meta 造句出题要用的外部配置与词性圆级表。
//
// 根包把 resolvePlanConfig（60 秒缓存 / COS 坏了用内置 / source 日志）和
// reviewCircle（读词性表 / 失败打日志后 fail-open）包一层实现注入，
// 两个函数体一行不动 —— 缓存归属、TTL、锁、兜底、fail-open 全留适配层。
// Circle 由用例在「确有回炉句」时才调，保住原来不白读词性表的懒语义。
type Meta interface {
	PlanConfig(ctx context.Context) planconfig.PlanConfig
	Circle(ctx context.Context) map[string]string
}

// Params 造句出题入参。三个入口的差异都收在这里：
//   - Today：目标日期（网页可传未来做明日预告，离线/CLI 传当天）
//   - Arc：回炉句闲置过滤用的档案；nil 表示不过滤（CLI 预检拿不到档案，宁可全列）
//   - BaseCount：基础句数。>0 直接用（离线 Excel 的 n、CLI 的 --count）；
//     <=0 表示用 PlanConfig().SentenceBaseCount（网页端跟配额走）
type Params struct {
	Today     time.Time
	Arc       *archive.Archive
	BaseCount int
}

// Result 造句出题产出。Sentences 是最终句表（基础句 + 回炉加餐，编号连续）。
// 其余字段是本次读到的原始数据和加餐中间量，供 CLI sentence-preview 做
// 圆级分档 / 近 7 天重复 / 错句到期的只读自检；网页端与离线导出只用 Sentences。
type Result struct {
	Sentences   []sentence.PlanSentence
	BaseCount   int                        // 加餐前的基础句数
	ReviewCount int                        // 回炉加餐句数
	CircleRatio int                        // 生效的三圆名额占比（无条件算好，CLI 输出要用）
	Circle      map[string]string          // 本次用的圆级表；没加餐为 nil
	ReviewBank  *sentencereview.ReviewBank // 本次读到的回炉库；没加餐为 nil
	ReviewExtra []sentence.BankSentence    // 本次选中的回炉句
	History     sentence.SentenceHistory   // 读到的轮换账本
	Wrong       *sentence.SentenceWrong    // 读到的错句表
	Bank        *sentence.SentenceBank     // 读到的句库
}

// 读库失败的阶段。入口靠它还原各自原有的错误文案（网页给句库错误加
// 「还没建库？」提示，离线吞掉，CLI 打印后退出），保证错误响应字节不变。
const (
	StageBank    = "bank"
	StageHistory = "history"
	StageWrong   = "wrong"
)

// LoadError 标记哪一步读库失败，Err 是最原始的底层错误。
type LoadError struct {
	Stage string
	Err   error
}

func (e *LoadError) Error() string { return e.Err.Error() }
func (e *LoadError) Unwrap() error { return e.Err }
