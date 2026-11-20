package plan

import (
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/sentence"
)

// SentencePlanLocked 判断 COS 上已存的造句 plan 能不能直接复用 —— 「不提交就不换题」。
//
// 三个条件缺一不可：
//   - 读到了对象（下载失败时 old 是 nil，必须重新出题）
//   - 日期对得上（陈年 plan 绝不能当成今天的）
//   - 里面真有句子（空 plan 等于没出过题）
func SentencePlanLocked(old *offline.ReviewPlan, date string) bool {
	return old != nil && old.Date == date && len(old.Sentences) > 0
}

// WordPlanLocked 判断 COS 上已存的单词 / 钉子户 plan 能不能直接复用。
//
// 跟 SentencePlanLocked 有两点不同，都是刻意的：
//   - 空 plan 也算锁定。单词 / 钉子户全交完时不删 plan，留一份空对象当
//     「今天已经出过题」的标记；否则 /api/hard 会立刻重算同一批钉子户
//     （hard 没有 IsDue 过滤），老师一刷新就把刚练完的词又练一遍 = 二次回写。
//   - 造句恰好相反：全交完删 plan 换新一批，所以 SentencePlanLocked 要求
//     len(Sentences) > 0。两条路别互相套用。
//
// 下载失败时 old 是 nil，必须重新出题；陈年 plan 的 Date 对不上也算不锁。
func WordPlanLocked(old *offline.ReviewPlan, date string) bool {
	return old != nil && old.Date == date
}

// PruneWordPlan 把已提交的题从单词 / 钉子户 plan 里摘掉，返回还剩几个。
//
// 题号保持原编号、绝不重排：record.Apply 靠 plan 的 number → word 映射回写
// 档案，重排会让下一批提交对上另一个词。原地复用底层数组，口径跟
// PruneSentencePlan 一致。submitted 里对不上的题号（如 999）直接忽略。
func PruneWordPlan(p *offline.ReviewPlan, submitted []int) int {
	if p == nil {
		return 0
	}
	done := make(map[int]bool, len(submitted))
	for _, n := range submitted {
		done[n] = true
	}
	left := p.Words[:0]
	for _, w := range p.Words {
		if !done[w.Number] {
			left = append(left, w)
		}
	}
	p.Words = left
	return len(left)
}

// HardPlanRegistrationBlocked 判断 CLI `export-hard --register` 该不该拒绝写
// plans/hard_<date>.json。
//
// 背景：这个 key 早年是 CLI 离线闭环专属（导出 xlsx 顺手登记 plan，供
// `record --hard` 映射题号）。后来网页 /api/hard 复用同一个 key，
// 「三种模式统一」之后它变成「当天锁定 + 提交剪枝」的可变进度：
//   - 部分提交：/api/record 摘掉已答题号且刻意不重排，剩 1..N 里的缺号
//   - 全部交完：留一份空 Words 当「今天已出过题」的锁（见 WordPlanLocked）
//
// 此时再用 CLI 的全量普查覆盖，等于把老师当天剩下的进度抹掉。所以写之前
// 必须确认这份 plan 还是「没被动过的全量」。任一命中即拒绝：
//  1. 读到了当天 plan 且 Words 为空（全交完）
//  2. 题号不是连续 1..N（被剪过号）
//  3. 与本次重新算出的全量逐项不一致（词数 / 词形 / 阈值口径不同）
//
// 第 3 条让「换 --min-accuracy 再 --register」也拒绝，避免用另一份口径盖掉
// 当天进度；同一口径重复 --register 是幂等的，允许。
// old == nil（对象不存在）或日期对不上都放行：没有当天进度可覆盖。
func HardPlanRegistrationBlocked(old, full *offline.ReviewPlan, date string) bool {
	if old == nil || old.Date != date {
		return false
	}
	if len(old.Words) == 0 {
		return true
	}
	for i, w := range old.Words {
		if w.Number != i+1 {
			return true
		}
	}
	if full == nil {
		return false
	}
	if len(old.Words) != len(full.Words) {
		return true
	}
	for i, w := range old.Words {
		if w.Number != full.Words[i].Number || w.Word != full.Words[i].Word {
			return true
		}
	}
	return false
}

// CountByStatus 按状态计数，空状态不计入。
func CountByStatus(words []offline.PlanWord) map[string]int {
	out := map[string]int{}
	for _, w := range words {
		if w.Status == "" {
			continue
		}
		out[w.Status]++
	}
	return out
}

// PruneSentencePlan 把已提交的句子从 plan 里摘掉，返回还剩几句。
// 部分提交时剩下的句子留在 plan 里，刷新页面还是这批（题号保持原编号，
// 前端草稿按 number+原句指纹回填，对得上）。
func PruneSentencePlan(p *offline.ReviewPlan, results []sentence.SentenceResult) int {
	submitted := make(map[int]bool, len(results))
	for _, sr := range results {
		submitted[sr.Number] = true
	}
	left := p.Sentences[:0]
	for _, ps := range p.Sentences {
		if !submitted[ps.Number] {
			left = append(left, ps)
		}
	}
	p.Sentences = left
	return len(left)
}
