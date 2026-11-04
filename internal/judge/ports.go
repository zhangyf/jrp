package judge

import (
	"context"
)

// ConfigStore 读 COS 上的判句模板。这是 judge 用例唯一的外部读 IO。
//
// 实现方是根包 *Storage 的 DownloadJudgePrompt。CLI 管理命令用到的
// UploadJudgePrompt / DeleteJudgePrompt 不进用例接口 —— 用例只读不写
// （分层设计 5.1「ConfigStore 只有读」）。
type ConfigStore interface {
	DownloadJudgePrompt(ctx context.Context) (string, error)
}

// Caller 问一次模型链，拿到「解析好的判定结果」和生效模型名。
//
// 主→备切换、失败冷却（llm.SkipModel / llm.MarkResult）、真正发 HTTP
// 都在这个能力的实现里，留在根包 IO 层（llm_failover.go）—— 那份冷却
// 状态是进程内全局，查中文释义（llmChat）也要共用，不能搬进用例层。
// 模型配置（端点 / 密钥 / 主备链 / 超时）由具体实现自己持有，不进接口 ——
// 用例层只认「给 prompt 拿结果」，不认识 llm 包、不认识 *Storage。
type Caller interface {
	Judge(prompt string) (Result, string, error)
}
