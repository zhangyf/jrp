package main

import (
	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/record"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
	"github.com/zhangyf/jrp/internal/stats"
	"github.com/zhangyf/jrp/internal/wordmeta"
)

// LangConfig holds language-specific configuration.
type LangConfig struct {
	Code       string // "ja", "en", "fr"
	Name       string // "日语", "英语", "法语"
	WordColumn string // "日语单词", "英语单词", "法语单词"
	FilePrefix string // "日语学习进度档案", etc.
	COSPrefix  string // "language-review/ja", etc.
}

var LangConfigs = map[string]LangConfig{
	"ja": {Code: "ja", Name: "日语", WordColumn: "日语单词", FilePrefix: "日语学习进度档案", COSPrefix: "language-review/ja"},
	"en": {Code: "en", Name: "英语", WordColumn: "英语单词", FilePrefix: "英语学习进度档案", COSPrefix: "language-review/en"},
	"fr": {Code: "fr", Name: "法语", WordColumn: "法语单词", FilePrefix: "法语学习进度档案", COSPrefix: "language-review/fr"},
}

// Word 和 WordGroup 复用 review 包类型，保留根包调用方的类型名称。
type Word = review.Word
type WordGroup = review.WordGroup

// 档案类型已迁 internal/archive，根包只保留别名 —— 调用方继续用
// Archive / ChangelogEntry 这两个名字，不用跟着改。
type Archive = archive.Archive
type ChangelogEntry = archive.ChangelogEntry

// 出题配额类型已迁 internal/planconfig，根包只保留别名 —— COS 读写、
// 60 秒缓存和 CLI 继续用 PlanConfig 这个名字，序列化形态不变。
type PlanConfig = planconfig.PlanConfig

// 回炉变形句库类型已迁 internal/sentencereview，根包只保留别名 ——
// sentence_review.go 的 *Storage 方法与 cmd_sentence_review.go 的编排
// 继续用 ReviewBank / ReviewSentence 这两个名字，不用跟着改。
type ReviewBank = sentencereview.ReviewBank
type ReviewSentence = sentencereview.ReviewSentence

// 词性表与三圆分级类型已迁 internal/wordmeta，根包只保留别名 ——
// storage_wordmeta.go 的 COS 读写、circle.go 的 applyCircle / unholdLesson、
// 以及十几个引用文件都继续用 WordMeta / WordPOS / CircleItem / CircleInput /
// CircleOutcome 这些名字，序列化形态不变。
type WordMeta = wordmeta.WordMeta
type WordPOS = wordmeta.WordPOS
type CircleItem = wordmeta.CircleItem
type CircleInput = wordmeta.CircleInput
type CircleOutcome = wordmeta.CircleOutcome

// 复习计划数据模型已迁 internal/offline，根包只保留别名 ——
// storage.go 的 COS 存档读写、api_plan.go / api_trial.go / cmd_gen_plan.go
// 的构造逻辑，以及 excel.go 渲染侧都继续用 PlanWord / ReviewPlan 这两个名字，
// JSON tag 与序列化形态不变。
type PlanWord = offline.PlanWord
type ReviewPlan = offline.ReviewPlan

// PlanSentence is a sentence exercise in a review plan.
//
// 定义已迁 internal/sentence，根包只保留别名 —— ReviewPlan / RecordInput /
// excel.go / api_trial.go 继续用 PlanSentence 这个名字，不用跟着改。
type PlanSentence = sentence.PlanSentence

// RecordResult is one word's review result.
//
// 定义已迁 internal/record，根包只保留别名 —— RecordInput.WordResults /
// api_record_test.go 继续用 RecordResult 这个名字，JSON tag 与序列化形态不变。
type RecordResult = record.RecordResult

// RecordInput is the JSON input for the record command.
//
// Hard selects which plan to resolve word numbers against: false (default) reads
// the daily plan, true reads the hard-word plan from export-hard. Existing
// clients (web UI) omit the field and keep the original behaviour.
//
// SentenceResults 以前是死字段（全仓库没有读它的地方），造句对错压根不落库。
// 现在改由 ApplySentenceState 处理，所以类型换成了带 answer/chinese 的
// SentenceResult —— 造句的间隔重复需要原文才能定位。
type RecordInput struct {
	PlanDate        string                    `json:"plan_date"`
	Language        string                    `json:"language"`
	WordResults     []RecordResult            `json:"word_results"`
	SentenceResults []sentence.SentenceResult `json:"sentence_results"`
	Hard            bool                      `json:"hard,omitempty"`

	// 当天每题的详情，回写成功后存成快照供当天回看（只读）。
	// 档案里只有累计值（复习次数/错误数/LastReview），不记当次对错 ——
	// 没有它，老师练完就再也看不到自己写了什么、哪题错了。
	ReviewItems []ReviewItem `json:"review_items,omitempty"`
}

// AddWordsInput is the JSON input for the add-words command.
type AddWordsInput struct {
	Language string `json:"language"`
	Group    string `json:"group"` // e.g. "第8课 生词表（7/13）"
	Words    []struct {
		Word       string `json:"word"`
		Definition string `json:"definition"`
	} `json:"words"`
}

// UpdateDefInput is the JSON input for the update-def command.
type UpdateDefInput struct {
	Language   string `json:"language"`
	Word       string `json:"word"`
	Definition string `json:"definition"`
}

// UpdateWordInput is the JSON input for the update-word command.
type UpdateWordInput struct {
	Language string `json:"language"`
	Word     string `json:"word"`     // current form to locate (exact match)
	NewWord  string `json:"new_word"` // replacement form
}

// 统计相关类型已迁 internal/stats，根包只保留别名 ——
// cmd_stats.go 的 ComputeStats 薄适配、api_stats.go 的 /api/stats、
// 以及序列化继续用这些名字，JSON tag 与形态不变。
type StatsOutput = stats.StatsOutput
type StatsSnapshot = stats.StatsSnapshot
type StatsDetail = stats.StatsDetail
type LessonCount = stats.LessonCount
type JlptProgress = stats.JlptProgress
type JlptLevel = stats.JlptLevel
type HardWordCounts = stats.HardWordCounts
type TopReviewedWord = stats.TopReviewedWord
