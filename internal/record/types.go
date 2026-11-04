package record

// RecordResult 是单词复习结果的一项。
type RecordResult struct {
	Number  int  `json:"number"`
	Correct bool `json:"correct"`
}

// Input 是 Apply 需要的输入子集。
//
// 根包 RecordInput 还带 SentenceResults / ReviewItems / Language 等
// handler 层字段——那些不参与档案回写，且会牵动快照类型 ReviewItem
// （跟 mergeReview / UploadReview 绑在一起），所以不随迁；
// 根包薄适配把 RecordInput 转成这里的 Input 再调 Apply。
type Input struct {
	PlanDate    string
	WordResults []RecordResult
	Hard        bool
}

// Format 是语言相关的档案列名/文件名前缀，由入口层从 LangConfigs 注入。
type Format struct {
	Name       string // "日语"
	WordColumn string // "日语单词"
	FilePrefix string // "日语学习进度档案"
}

// Result 是 ApplyRecord 的返回（原 RecordResultSummary）。
type Result struct {
	Success     bool   `json:"success"`
	Command     string `json:"command"`
	PlanDate    string `json:"plan_date"`
	Correct     int    `json:"correct"`
	Wrong       int    `json:"wrong"`
	NotFound    int    `json:"not_found"`
	OldFilename string `json:"old_filename"`
	NewFilename string `json:"new_filename"`
	Version     string `json:"version"`
	TotalWords  int    `json:"total_words"`
	Hard        bool   `json:"hard,omitempty"`
}
