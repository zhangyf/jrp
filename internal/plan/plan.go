// Package plan 存放复习计划（daily / hard / sentences）的纯构造与裁剪规则。
//
// 这 6 个函数原先散落在根包 cmd_gen_plan.go / cmd_export_hard.go /
// api_plan.go / api_record.go，被 CLI、HTTP 服务、离线导出三处共用。
// 2026-11-04 按分层设计 P2 收拢到本包：入出参全是 internal 类型
// （archive.Archive / review.Word / offline.ReviewPlan / offline.PlanWord /
// sentence.SentenceResult），不碰 Storage / COS / HTTP / 全局配置，可独立单测。
//
// ⚠️ 行为不变：本批只搬函数，不改逻辑。BuildHardPlan 的排序与编号、
// Accuracy 用指针保真 0%、PruneSentencePlan 原地复用底层数组裁剪，
// 都逐字保留。改任何一处前先看 plan_test.go。
package plan

import (
	"sort"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/review"
)

// 钉子户分区边界。跟 --min-accuracy 无关：把阈值调到 0.45，
// Mild 桶自然就空了。
const (
	hardSevereMax   = 0.30
	hardModerateMax = 0.45
)

// hardSeverity 按正确率给钉子户分桶。
//
// 分区字面量唯一来源在 internal/offline（渲染侧同包持有 hardCategories），
// 这里直接引用，不另写一份字面量。
func hardSeverity(acc float64) string {
	switch {
	case acc < hardSevereMax:
		return offline.HardSevere
	case acc < hardModerateMax:
		return offline.HardModerate
	default:
		return offline.HardMild
	}
}

// BuildDuePlan 找出 targetDate 到期的词，打上复习分类，按状态优先级排序
// （钉子户 > 待巩固 > 待测试 > 基本掌握 > 抽查），返回带编号的复习计划
// （不含句子 / Excel）。CLI 与 HTTP 服务共用。
//
// hold 里点名的词直接跳过（还没学到的课，2026-11-02 手册补录那批）。传 nil 表示不排除。
func BuildDuePlan(arc *archive.Archive, lang string, targetDate time.Time, hold map[string]bool) *offline.ReviewPlan {
	var dueWords []review.Word
	for _, w := range review.AllWords(arc.Groups) {
		if hold[w.Word] {
			continue
		}
		if review.IsDue(w, targetDate) {
			w.Status = review.GetReviewCategory(w)
			dueWords = append(dueWords, w)
		}
	}

	sort.SliceStable(dueWords, func(i, j int) bool {
		return review.StatusPriority(dueWords[i].Status) < review.StatusPriority(dueWords[j].Status)
	})

	p := &offline.ReviewPlan{
		Date:     targetDate.Format("2006-01-02"),
		Language: lang,
	}
	for i, w := range dueWords {
		p.Words = append(p.Words, offline.PlanWord{
			Number:     i + 1,
			Word:       w.Word,
			Definition: w.Definition,
			Group:      w.Group,
			Status:     w.Status,
		})
	}
	return p
}

// BuildHardPlan 把档案里全部钉子户拉出来 —— 刻意不做 IsDue 过滤，
// 这是跟 BuildDuePlan 的本质区别：gen-plan 只出今天到期的，这里是一次全量普查。
//
// 按正确率升序排。分区边界随正确率单调，所以这个顺序天然给出
// 重度 → 中度 → 轻度 的分区顺序。编号跨分区连续。
func BuildHardPlan(arc *archive.Archive, lang string, targetDate time.Time,
	minAcc float64, minRev int) *offline.ReviewPlan {

	var hard []review.Word
	for _, w := range review.AllWords(arc.Groups) {
		if review.IsHardWord(w, minAcc, minRev) {
			hard = append(hard, w)
		}
	}

	sort.SliceStable(hard, func(i, j int) bool {
		ai, _ := review.Accuracy(hard[i])
		aj, _ := review.Accuracy(hard[j])
		if ai != aj {
			return ai < aj // lower accuracy first
		}
		if hard[i].ErrorCount != hard[j].ErrorCount {
			return hard[i].ErrorCount > hard[j].ErrorCount // more errors first
		}
		return hard[i].Word < hard[j].Word // stable tie-break
	})

	p := &offline.ReviewPlan{
		Date:        targetDate.Format("2006-01-02"),
		Language:    lang,
		Kind:        "hard",
		MinAccuracy: minAcc,
		MinReviews:  minRev,
	}
	for i, w := range hard {
		acc, _ := review.Accuracy(w)
		accVal := acc // take address of a per-iteration copy
		p.Words = append(p.Words, offline.PlanWord{
			Number:      i + 1,
			Word:        w.Word,
			Definition:  w.Definition,
			Group:       w.Group,
			Status:      hardSeverity(acc),
			ErrorCount:  w.ErrorCount,
			ReviewCount: w.ReviewCount,
			Accuracy:    &accVal,
		})
	}
	return p
}
