// 复习计划数据模型：PlanWord / ReviewPlan 原定义在根包 types.go，
// 2026-11-04 迁入本包，供离线 Excel 渲染侧复用（渲染函数下一批再迁）。
//
// ⚠️ ReviewPlan 是存进 COS 的存档 JSON（daily / hard / sentences 三类 plan，
// storage.go 的 UploadPlan / DownloadPlan / UploadHardPlan / UploadSentencePlan
// 都吃它），PlanWord.Accuracy 用 *float64 + omitempty 保真 0%。
// 改字段、顺序或 tag 会静默改变线上存档格式 —— 动之前先看 plan_test.go。
package offline

import "github.com/zhangyf/jrp/internal/sentence"

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
	// 词性 / 备注 / 圆级不在档案里，也不进 plan 存档 —— 只在 API 返回时按词形
	// 从 word_meta.json join 上来（enrichWordMeta），给页面显示用。
	// 塞进 plan 存档会牵动 record 的序号映射和存档体积，没必要。
	Pos  string `json:"pos,omitempty"`
	Sub  string `json:"sub,omitempty"`
	Note string `json:"note,omitempty"`
	// Circle 是三圆手册分级：三圆 / 二圆 / 一圆（手册没收录则空，页面不显示）。
	// 2026-11-03 老师要「单词复习时展示圆级」，跟词性同一路 join。
	Circle string `json:"circle,omitempty"`
}

// ReviewPlan is the JSON saved alongside each Excel for record tracking.
//
// Kind distinguishes plan types: "" (or absent) = daily review plan,
// "hard" = hard-word deep-dive plan produced by export-hard. The two are stored
// under different COS keys so they never overwrite each other.
type ReviewPlan struct {
	Date        string                  `json:"date"`
	Language    string                  `json:"language"`
	Words       []PlanWord              `json:"words"`
	Sentences   []sentence.PlanSentence `json:"sentences"`
	Kind        string                  `json:"kind,omitempty"`
	MinAccuracy float64                 `json:"min_accuracy,omitempty"`
	MinReviews  int                     `json:"min_reviews,omitempty"`
}
