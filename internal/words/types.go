// Package words 承载「录生词」用例：把一批新词并进档案、点名中文释义撞车、
// 追加 changelog、算新版本并写回。CLI 的 add-words、网页/小程序的「录入新词」
// 都只调这一份实现，去重/撞车/分组/版本规则不会两边各写一套漂掉。
//
// 用例层不 import net/http、flag、*Storage、COS：档案 IO 经 ArchiveStore
// 接口注入，语言相关文本经 Format 传入，时钟经 Params.Now 传入，可脱离外部依赖单测。
package words

import "time"

// WordToAdd 是一条待录入的词。
type WordToAdd struct {
	Word       string `json:"word"`
	Definition string `json:"definition"`
}

// AddWordsOutcome 是加词的结果，CLI 和 API 都拿它出输出。
type AddWordsOutcome struct {
	Added       int      `json:"added"`
	Duplicates  int      `json:"duplicates"`
	OldFilename string   `json:"old_filename"`
	NewFilename string   `json:"new_filename"`
	Version     string   `json:"version"`
	MajorBump   bool     `json:"major_bump"`
	TotalWords  int      `json:"total_words"`
	DupDefs     []string `json:"dup_definitions,omitempty"`
	DupCores    []string `json:"dup_core_definitions,omitempty"`
}

// Format 是写档案需要的语言相关文本。根包的 LangConfig 是全局表且带 COS 配置，
// 用例层不该认识它 —— 入口适配层挑出这三个字段传进来。
type Format struct {
	Name       string // 语言名，如「日语」，写进档案标题
	WordColumn string // 单词列名，如「日语单词」
	FilePrefix string // 档案文件名前缀，如「日语学习进度档案」
}

// Params 是录生词用例的输入。
type Params struct {
	Group      string
	Items      []WordToAdd
	ForceMajor bool
	Lang       string
	Format     Format
	// Now 是注入的时钟。用例在「读档之后、算出 added>0 时」才求值，
	// 精确还原旧实现里 today := time.Now() 的时点（在 EnsureArchive 之后），
	// 避免入口提前取时间把跨午夜边界挪位。单测传固定 clock 即可。
	Now func() time.Time
}
