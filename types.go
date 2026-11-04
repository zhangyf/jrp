package main

import (
	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
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
type RecordResult struct {
	Number  int  `json:"number"`
	Correct bool `json:"correct"`
}

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

// StatsOutput is the result of the stats command.
type StatsOutput struct {
	Language  string            `json:"language"`
	Days      int               `json:"days"`
	Snapshots []StatsSnapshot   `json:"snapshots"`
	Changes   map[string]string `json:"changes"`
}

type StatsSnapshot struct {
	Date        string `json:"date"`
	Version     string `json:"version"`
	Total       int    `json:"total"`
	Mastered    int    `json:"mastered"`
	Basic       int    `json:"basic"`
	NeedsConsol int    `json:"needs_consol"`
	Untested    int    `json:"untested"`
	Errors      int    `json:"errors"`
}

// StatsDetail is extracted from the latest archive and provides per-lesson
// breakdown, accuracy distribution, and hard-word counts so callers never
// need to parse the archive markdown manually.
type StatsDetail struct {
	ByLesson             []LessonCount     `json:"by_lesson"`
	AccuracyDistribution map[string]int    `json:"accuracy_distribution"`
	HardWords            HardWordCounts    `json:"hard_words"`
	TopReviewed          []TopReviewedWord `json:"top_reviewed"`
}

type LessonCount struct {
	Lesson string `json:"lesson"`
	Count  int    `json:"count"`
}

// JlptProgress 日语能力考（JLPT）等级目标进度。
//
// 门槛值是常用的参考口径而不是官方标准：N5 约 800 词、语法到标日初级第 22 课；
// N4 约 1500 词、语法到标日初级下册第 48 课。放在后端算，是为了让 CLI 和网站
// 共用同一个口径，改门槛只改一处。
type JlptProgress struct {
	VocabTotal int       `json:"vocab_total"` // 词库总量
	VocabFirm  int       `json:"vocab_firm"`  // 熟练词：正确率 ≥80% 且复习过
	Lesson     int       `json:"lesson"`      // 当前课次，从分组名里解析出的最大课号
	N5         JlptLevel `json:"n5"`
	N4         JlptLevel `json:"n4"`
}

type JlptLevel struct {
	Vocab  int `json:"vocab"`
	Lesson int `json:"lesson"`
}

type HardWordCounts struct {
	Severe   int `json:"severe"`
	Moderate int `json:"moderate"`
	Mild     int `json:"mild"`
	Total    int `json:"total"`
}

type TopReviewedWord struct {
	Word     string  `json:"word"`
	Reviews  int     `json:"reviews"`
	Errors   int     `json:"errors"`
	Accuracy float64 `json:"accuracy"`
}
