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
