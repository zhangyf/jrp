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
type PlanWord struct {
	Number     int    `json:"number"`
	Word       string `json:"word"`
	Definition string `json:"definition"`
	Group      string `json:"group"`
}

// PlanSentence is a sentence exercise in a review plan.
type PlanSentence struct {
	Number  int    `json:"number"`
	Chinese string `json:"chinese"`
	Answer  string `json:"answer"`
}

// ReviewPlan is the JSON saved alongside each Excel for record tracking.
type ReviewPlan struct {
	Date      string         `json:"date"`
	Language  string         `json:"language"`
	Words     []PlanWord     `json:"words"`
	Sentences []PlanSentence `json:"sentences"`
}

// RecordResult is one word's review result.
type RecordResult struct {
	Number  int  `json:"number"`
	Correct bool `json:"correct"`
}

// RecordInput is the JSON input for the record command.
type RecordInput struct {
	PlanDate        string         `json:"plan_date"`
	Language        string         `json:"language"`
	WordResults     []RecordResult `json:"word_results"`
	SentenceResults []RecordResult `json:"sentence_results"`
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
