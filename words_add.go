package main

import (
	"context"
	"time"

	"github.com/zhangyf/jrp/internal/words"
)

// 加词的唯一实现已迁 internal/words：CLI 的 add-words、网页/小程序的「录入新词」
// 都经下面的薄适配层调到同一份 words.Add，去重/撞释义/分组/版本规则只有一份。
//
// 这里只做两件事：把 *Storage + 全局 LangConfigs + time.Now 适配成用例层要的
// ArchiveStore / Format / clock；保留根包调用方习惯的两个类型名。
// 2026-10-21 从 cmd_add_words.go 抽出；2026-11-04 规则核心迁入 internal/words。

// WordToAdd / AddWordsOutcome 保留根包名字，让 api_words.go / cmd_add_words.go /
// bootstrap_test.go 三处调用点零改动。
type WordToAdd = words.WordToAdd
type AddWordsOutcome = words.AddWordsOutcome

// *Storage 隐式满足 words.ArchiveStore（EnsureArchive / UploadArchive），
// 这条断言让接口不匹配在编译期就暴露，而不是上线后才发现。
var _ words.ArchiveStore = (*Storage)(nil)

// addWordsToArchive 是「录生词」用例在根包入口的薄适配层。
//
// ⚠️ 会真的上传新档案。档案是版本化的，旧文件仍在 COS 上，加错了可以拿旧版本回滚。
func addWordsToArchive(ctx context.Context, st *Storage, group string, items []WordToAdd, forceMajor bool) (*AddWordsOutcome, error) {
	cfg := LangConfigs[st.lang]
	return words.Add(ctx, st, words.Params{
		Group:      group,
		Items:      items,
		ForceMajor: forceMajor,
		Lang:       st.lang,
		Format: words.Format{
			Name:       cfg.Name,
			WordColumn: cfg.WordColumn,
			FilePrefix: cfg.FilePrefix,
		},
		// 时钟传函数而非提前取值：用例在「读档之后、added>0 时」才求值，
		// 精确还原旧实现里 today := time.Now() 的时点，跨午夜边界不挪位。
		Now: time.Now,
	})
}
