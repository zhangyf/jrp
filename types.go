package main

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

// Word represents a single vocabulary entry.
type Word struct {
	Word               string // target language word (e.g. すし)
	Definition         string // Chinese definition (e.g. 寿司)
	ReviewCount        int    // total review attempts
	ErrorCount         int    // total wrong answers
	ConsecutiveCorrect int    // consecutive correct since last error
	LastReview         string // last review date MM/DD
	Status             string // 🟢已掌握 / 🟡基本掌握 / 🔴待巩固 / 🔄待测试
	Group              string // group title (e.g. "第1课01 基础词（5/25）")
}

// WordGroup is a titled section of words in the archive.
type WordGroup struct {
	Title string
	Words []Word
}

// ChangelogEntry is one row in the version history table.
type ChangelogEntry struct {
	Date        string
	Version     string
	Total       string
	Mastered    string
	Basic       string
	NeedsConsol string
	Untested    string
	Errors      string
	NailHouse   string
	Description string
}

// Archive represents the full parsed markdown archive.
type Archive struct {
	Language   string
	Changelog  []ChangelogEntry
	Title      string
	LastUpdate string
	Groups     []WordGroup
	RawHeader  string // unparsed header text for preservation
	RawFooter  string // unparsed footer text for preservation
}

// PlanWord is a word entry in a review plan.
//
// ErrorCount/ReviewCount/Accuracy are only populated for hard-word plans
// (export-hard); they are omitted from daily review plans so that existing
// consumers (web UI, record) see byte-identical JSON.
//
// Accuracy is a POINTER on purpose: a genuine 0% accuracy (wrong every single
// time) is the most important case in a hard-word list, and a plain float64 with
// omitempty would silently drop it. ErrorCount stays a plain int because a hard
// word always has at least one error, but ReviewCount/Accuracy must survive.
type PlanWord struct {
	Number      int      `json:"number"`
	Word        string   `json:"word"`
	Definition  string   `json:"definition"`
	Group       string   `json:"group"`
	Status      string   `json:"status"` // review category: ☠️钉子户 / 🔴待巩固 / 🔄待测试 / 🟡基本掌握 / 🟢抽查
	ErrorCount  int      `json:"error_count,omitempty"`
	ReviewCount int      `json:"review_count,omitempty"`
	Accuracy    *float64 `json:"accuracy,omitempty"`
}

// PlanSentence is a sentence exercise in a review plan.
type PlanSentence struct {
	Number  int    `json:"number"`
	Chinese string `json:"chinese"`
	Answer  string `json:"answer"`
}

// ReviewPlan is the JSON saved alongside each Excel for record tracking.
//
// Kind distinguishes plan types: "" (or absent) = daily review plan,
// "hard" = hard-word deep-dive plan produced by export-hard. The two are stored
// under different COS keys so they never overwrite each other.
type ReviewPlan struct {
	Date        string         `json:"date"`
	Language    string         `json:"language"`
	Words       []PlanWord     `json:"words"`
	Sentences   []PlanSentence `json:"sentences"`
	Kind        string         `json:"kind,omitempty"`
	MinAccuracy float64        `json:"min_accuracy,omitempty"`
	MinReviews  int            `json:"min_reviews,omitempty"`
}

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
type RecordInput struct {
	PlanDate        string         `json:"plan_date"`
	Language        string         `json:"language"`
	WordResults     []RecordResult `json:"word_results"`
	SentenceResults []RecordResult `json:"sentence_results"`
	Hard            bool           `json:"hard,omitempty"`
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

// StatsOutput is the result of the stats command.
type StatsOutput struct {
	Language     string            `json:"language"`
	Days         int               `json:"days"`
	Snapshots    []StatsSnapshot   `json:"snapshots"`
	Changes      map[string]string `json:"changes"`
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
	ByLesson             []LessonCount   `json:"by_lesson"`
	AccuracyDistribution map[string]int  `json:"accuracy_distribution"`
	HardWords            HardWordCounts  `json:"hard_words"`
	TopReviewed          []TopReviewedWord `json:"top_reviewed"`
}

type LessonCount struct {
	Lesson string `json:"lesson"`
	Count  int    `json:"count"`
}

type HardWordCounts struct {
	Severe   int `json:"severe"`
	Moderate int `json:"moderate"`
	Mild     int `json:"mild"`
	Total    int `json:"total"`
}

type TopReviewedWord struct {
	Word       string  `json:"word"`
	Reviews    int     `json:"reviews"`
	Errors     int     `json:"errors"`
	Accuracy   float64 `json:"accuracy"`
}
