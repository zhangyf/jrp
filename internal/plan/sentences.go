package plan

import (
	"context"

	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/sentence"
	"github.com/zhangyf/jrp/internal/sentencereview"
)

// ComposeSentences 挑出某天的造句题：基础句库挑句 + 回炉变形句加餐，编号连续。
//
// 原先这段编排在根包逐字重复了三份：网页 /api/plan（api_plan.go）、
// 离线 Excel 导出（api_offline.go 的 BuildSentencePlanFrom）、CLI 预检
// （cmd_sentence.go 的 runSentencePreview）。2026-11-04 按分层设计 P3 收拢到本包。
//
// 三处的差异全部收进 Params（基础句数来源、arc 是否过滤），入口只留
// 各自的编排职责：网页的「当天锁定 + 上传」、离线的纯读返回、CLI 的自检统计。
//
// ⚠️ 行为不变，逐字保留这几条：
//   - 读四库顺序与失败语义：bank/history/wrong 失败透传，回炉库读失败/空
//     一律当「今天没加餐」，基础句照出（fail-open，新功能不能弄没练习）
//   - exclude 用 sentence.Norm 归一化，回炉句追加时编号在基础句之后连续
//   - 圆级表 Meta.Circle 只在确有回炉库时才取，保持不白读词性表的懒语义
func ComposeSentences(ctx context.Context, store SentenceStore, meta Meta, p Params) (Result, error) {
	var res Result

	bank, err := store.DownloadSentenceBank(ctx)
	if err != nil {
		return res, &LoadError{Stage: StageBank, Err: err}
	}
	hist, err := store.DownloadSentenceHistory(ctx)
	if err != nil {
		return res, &LoadError{Stage: StageHistory, Err: err}
	}
	wrong, err := store.DownloadSentenceWrong(ctx)
	if err != nil {
		return res, &LoadError{Stage: StageWrong, Err: err}
	}

	// 配额无条件取一次：网页要用 SentenceBaseCount 挑基础句，CLI 要把
	// CircleRatio 写进自检输出，回炉加餐要用 SentenceReviewCount。
	// resolvePlanConfig 的 60 秒缓存 / 内置兜底 / source 日志全在适配层，
	// 这里只认 planconfig.PlanConfig 这个自有类型。
	cfg := meta.PlanConfig(ctx)
	baseCount := p.BaseCount
	if baseCount <= 0 {
		baseCount = cfg.SentenceBaseCount
	}

	out := sentence.BuildSentencePlan(bank, hist, wrong, p.Today, baseCount)
	res = Result{
		BaseCount:   len(out),
		CircleRatio: planconfig.PlanCircleRatio(cfg),
		History:     hist,
		Wrong:       wrong,
		Bank:        bank,
	}

	// 回炉变形句。拿不到句库 / 解析失败就当今天没有加餐 —— 基础句该怎么出
	// 还怎么出，绝不能因为加餐功能把整个造句练习弄没。
	rb, rerr := store.DownloadReviewBank(ctx)
	if rerr != nil || rb == nil {
		res.Sentences = out
		return res, nil
	}
	res.ReviewBank = rb

	exclude := make(map[string]bool, len(out))
	for _, ps := range out {
		exclude[sentence.Norm(ps.Answer)] = true
	}

	// 懒：到这里才读词性表（reviewCircle 会打日志、失败 fail-open）。
	circle := meta.Circle(ctx)
	extra := sentencereview.BuildReviewSentences(rb, hist, p.Arc, p.Today,
		cfg.SentenceReviewCount, exclude, sentencereview.DefaultReviewIdleDays,
		circle, planconfig.PlanCircleRatio(cfg))
	res.ReviewExtra = extra
	res.Circle = circle
	res.ReviewCount = len(extra)

	for _, e := range extra {
		out = append(out, sentence.PlanSentence{
			Number:  len(out) + 1,
			Chinese: e.Chinese,
			Answer:  e.Answer,
		})
	}
	res.Sentences = out
	return res, nil
}
