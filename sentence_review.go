package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/wordmeta"
)

// 回炉变形句库的 COS 读写与圆级加权（编排层，留根包）。
//
// 纯规则层（句库类型、挑句 BuildReviewSentences、闲置天数、圆级分档、
// 挑原型句打分与骨架挑选）已迁 internal/sentence；类型通过 types.go
// 的别名保持 ReviewBank / ReviewSentence 这两个名字不变。
//
// 背景（2026-10-09 老师提的）：档案里有一大批正确率≥80%、但 30 天以上没复习的词。
// 按艾宾浩斯间隔它们还没到期，可实际已经忘得差不多了。直接加进单词复习等于
// 「又背一遍词表」——老师明确不要。所以走造句：拿句库里的原型句，只换掉一个词，
// 助词序列一个字不动，写整句时自然要把那个词写出来。
// 句库由 AI 生成（词性匹配、语义合理性由人/AI 把关），Go 只负责挑，不做换词拼接。

// reviewBankKey 回炉变形句库 = 教材（老师用 go test 生成），全站共享。
func (s *Storage) reviewBankKey() string { return s.sharedBase() + "/plans/sentence_review.json" }

// DownloadReviewBank 读回炉句库。
//
// 句库还没建时返回空库而不是 error —— 没有回炉句只是「今天不加餐」，
// 绝不能因此把整个造句练习弄挂。
func (s *Storage) DownloadReviewBank(ctx context.Context) (*ReviewBank, error) {
	data, err := s.store.GetAll(ctx, s.reviewBankKey())
	if err != nil {
		return &ReviewBank{}, nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &ReviewBank{}, nil
	}
	var b ReviewBank
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("解析回炉句库失败：%w", err)
	}
	return &b, nil
}

func (s *Storage) UploadReviewBank(ctx context.Context, b *ReviewBank) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.reviewBankKey(), []byte(toJSON(b)))
}

// reviewCircle 读词性表摊出「词形 -> 圆级」，给回炉句挑句加权用。
//
// 读失败返回 nil —— 挑句那边按「不分层」处理，出题行为跟没加圆级时一致。
// 拿不到圆级不能变成少出题，这跟 holdSet 的 fail-open 是同一个道理。
func reviewCircle(ctx context.Context, st *Storage) map[string]string {
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil {
		log.Printf("sentence-review: 词性表读不了（%v），回炉句不按圆级分层", err)
		return nil
	}
	return wordmeta.CircleMap(meta)
}

// sentenceMeta 把根包两个带缓存 / 日志的编排函数适配成 internal/plan.Meta。
// 只做转发：resolvePlanConfig 的 60 秒缓存 / COS 坏了用内置 / source 日志，
// reviewCircle 的词性表读取 / fail-open 日志，全部一行不动留在上面两个函数里。
// 用的人（用例层）定义接口，被用的人（根包 IO 层）做适配 —— 跟 P0 的
// StatsArchiveReader 同一形态，第三方类型（*Storage）不渗进用例签名。
type sentenceMeta struct{ st *Storage }

func (m sentenceMeta) PlanConfig(ctx context.Context) planconfig.PlanConfig {
	cfg, _ := resolvePlanConfig(ctx, m.st)
	return cfg
}

func (m sentenceMeta) Circle(ctx context.Context) map[string]string {
	return reviewCircle(ctx, m.st)
}

var (
	_ plan.Meta          = sentenceMeta{}
	_ plan.SentenceStore = (*Storage)(nil)
)
