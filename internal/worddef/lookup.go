package worddef

import (
	"context"
	"strings"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

// Lookup 批量查「中文释义 + 汉字写法」：先档案，档案没有再问模型。
//
// 抽成用例是因为两处要用，口径必须一模一样：
//   - POST /api/word-def（手打录入时补中文）
//   - POST /api/vision（拍照认完词顺手补）
//
// 返回按入参顺序排好的结果，以及生效的模型名。模型不可用时 Def 留空，不算错误。
// 逐字保留原 lookupWordDefs 的分支与保序口径，只把 *Storage / server / llm 依赖
// 换成注入的 ArchiveStore / Caller，lang 与 ctx 作显式参数传入。
func Lookup(ctx context.Context, store ArchiveStore, caller Caller, lang string, words []string) ([]Item, string) {
	var items []Item
	var todo []string

	// ① 档案优先：精确命中 → 按假名命中（顺带把汉字补上）
	if data, _, err := store.DownloadLatestArchive(ctx); err == nil {
		if arc, err := archive.ParseArchive(string(data), lang); err == nil {
			for _, w := range words {
				if existing, _ := review.FindWord(arc.Groups, w); existing != nil &&
					strings.TrimSpace(existing.Definition) != "" {
					items = append(items, Item{Input: w, Word: existing.Word, Def: strings.TrimSpace(existing.Definition)})
					continue
				}
				if !HasParen(w) {
					if existing := FindWordByKana(arc.Groups, w); existing != nil &&
						strings.TrimSpace(existing.Definition) != "" {
						items = append(items, Item{Input: w, Word: existing.Word, Def: strings.TrimSpace(existing.Definition)})
						continue
					}
				}
				todo = append(todo, w)
			}
		} else {
			todo = words
		}
	} else {
		todo = words
	}

	// ② 剩下的问模型（配置 / 主备链 / 冷却全在 Caller 里，用例层不碰）
	model := ""
	if len(todo) > 0 {
		reply, used, err := caller.Lookup(FillPrompt(Prompt, todo))
		if err == nil {
			model = used
			var got []LLMItem
			if raw, perr := ParseReply(reply); perr == nil {
				got = raw
			}
			byWord := map[string]LLMItem{}
			for _, g := range got {
				byWord[strings.TrimSpace(g.Word)] = g
			}
			for _, w := range todo {
				it := Item{Input: w, Word: w}
				if g, ok := byWord[w]; ok {
					it.Def = strings.TrimSpace(g.Def)
					// 汉字补进半角括号 —— 半角括号在档案里的含义就是「汉字写法」
					if k := strings.TrimSpace(g.Kanji); k != "" && !HasParen(w) {
						it.Word = w + "(" + k + ")"
					}
				}
				items = append(items, it)
			}
		}
		// 模型挂了 / 没配 / 回答里漏了某个词：都按「没有释义」补一条，
		// 前端留给老师自己填，不阻断录入。
	}

	// 保序输出（档案命中和模型回答是分两批来的，要按请求的顺序还回去）
	ordered := make([]Item, 0, len(words))
	byInput := map[string]Item{}
	for _, it := range items {
		byInput[it.Input] = it
	}
	for _, w := range words {
		it, ok := byInput[w]
		if !ok {
			it = Item{Input: w, Word: w}
		}
		ordered = append(ordered, it)
	}
	return ordered, model
}
