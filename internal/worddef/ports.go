package worddef

import "context"

// ArchiveStore 是查释义用例唯一需要的档案能力：读最新档案原文。
// 接口由消费方（用例层）定义，根包 *Storage 隐式实现，用例层不认识 *Storage / COS。
type ArchiveStore interface {
	DownloadLatestArchive(ctx context.Context) ([]byte, string, error)
}

// Caller 把「模型是否可用、主备链、冷却、真正发请求」全部收口在根包 IO 层。
//
// 用例层不 import internal/llm，也不认识 llm.Config / LoadConfig / llmChat。
// 没配模型或全链失败都返回 err；模型回了但内容格式不对，由用例层自己解析。
// 这与 internal/judge.Caller 是对称形态：配置与网关留在适配层。
type Caller interface {
	Lookup(prompt string) (reply string, model string, err error)
}
