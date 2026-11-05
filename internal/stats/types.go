package stats

// StatsResult 统计结果。CLI 的 stats 与网站的 /api/stats 共用。
type StatsResult struct {
	Snapshots []StatsSnapshot   `json:"snapshots"`
	Changes   map[string]string `json:"changes"`
	Detail    *StatsDetail      `json:"detail"`
	Jlpt      *JlptProgress     `json:"jlpt"`
}

// StatsOutput 是 stats 命令的旧结果形态（历史遗留，当前统一走 StatsResult）。
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
	VocabTotal int              `json:"vocab_total"` // 词库总量
	VocabFirm  int              `json:"vocab_firm"`  // 熟练词：正确率 ≥80% 且复习过
	Lesson     int              `json:"lesson"`      // 当前课次，从分组名里解析出的最大课号
	N5         JlptLevel        `json:"n5"`
	N4         JlptLevel        `json:"n4"`
	Listening  []ListeningLevel `json:"listening"` // 各等级听力累计秒数（没听过为空数组）
}

type JlptLevel struct {
	Vocab  int `json:"vocab"`
	Lesson int `json:"lesson"`
}

// ListeningLevel 一个等级的听力进度，挂在 JlptProgress.Listening 里，
// 前端在 JLPT 进度卡里按等级画进度条。
//   - Seconds：累计实际播放秒数（float，前端上报带小数）。
//   - GoalMinutes：该等级的满格目标（分钟），后端从 COS 配置注入。
//     前端用 Seconds/60 ÷ GoalMinutes 算百分比，跟词汇/语法进度条同一套画法。
//     没配目标时后端给内置默认（N5=1500 / N4=3000 分钟）。
type ListeningLevel struct {
	Level       string  `json:"level"`
	Seconds     float64 `json:"seconds"`
	GoalMinutes int     `json:"goal_minutes"`
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
