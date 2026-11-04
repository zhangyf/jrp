package vision

// Candidate 是从图片里认出来的一个候选词。
// 根包用 `type VisionCandidate = vision.Candidate` 保 HTTP 响应 JSON 字节不变。
type Candidate struct {
	Word string `json:"word"`
	Def  string `json:"def"`
}

// ImageResult 是**一张图**的认词结果。
//
// 按图返回（而不是只给一个扁平列表）是老师 2026-10-21 问出来的：
// 「既然是拍照取词，那就意味着有些词我是不认识的，你不把原始照片给我展示出来，
//
//	我怎么知道你识别出来的单词是准确的还是不准确的呢。」
//
// —— 所以前端按图分块，每块带上原图缩略图，老师对着照片核哪个词认错了。
// 根包用 `type VisionImageResult = vision.ImageResult` 保 JSON 字节不变。
type ImageResult struct {
	Index      int         `json:"index"`
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Model      string      `json:"model,omitempty"`
	Candidates []Candidate `json:"candidates"`
}
