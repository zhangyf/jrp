package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/wordmeta"
	"github.com/zhangyf/jrp/internal/words"
)

// ============================================================================
// 三圆手册分级（circle）+ 挂起（hold）
// ============================================================================
//
// 2026-11-02 老师拿《标日初上 三圆词汇速记手册》（学员版）来对齐词汇总表：
//  1. 总表里已有的词，按手册标上是几圆词（三圆 / 二圆 / 一圆）
//  2. 手册里有、总表没有的词，补录进词库
//  3. 补录的词「大概率还没学到」，不能涌进每日练习 → Hold
//
// 分级和挂起都存在 word_meta.json（跟 pos/sub/note 同一处），
// ⛔ 不动档案的 7 列 markdown 表格结构 —— 动表格要改所有读写档案的地方，不值。
//
// 词性表 / 圆级纯规则（CircleGroupOf / LessonOfGroup / CircleMap 及 5 个类型）
// 已迁 internal/wordmeta，根包靠 types.go 的别名保持 WordMeta 等名字不变。
// 圆级分档 CircleRank 归 internal/sentence，只吃摊好的 map。
// applyCircle / unholdLesson 要写档案，留根；编排照旧。

// applyCircle 是批量导入/打标的唯一实现。
//
// 行为（按顺序）：
//  1. 档案里已经有的词 → 不重复加（Duplicates++），只打圆级标
//  2. 档案里没有、输入里带 Def 的词 → 按课建组追加，并 Hold
//  3. 档案里没有、输入里没 Def 的词 → 只打标，Warnings 点名（没释义没法录）
//  4. DefFix 里的词 → 改档案释义
//  5. 全程只在最后写一次档案 + 一次 word_meta
//
// ⚠️ 会真的上传。st.dryRun 时不落盘（跟其他 Upload* 一致）。
func applyCircle(ctx context.Context, st *Storage, in *CircleInput, hold bool) (*CircleOutcome, error) {
	arc, oldFilename, err := st.EnsureArchive(ctx, st.lang)
	if err != nil {
		return nil, fmt.Errorf("读取档案失败：%w", err)
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}
	if meta.Items == nil {
		meta.Items = map[string]WordPOS{}
	}

	out := &CircleOutcome{OldFilename: oldFilename}
	groupSet := map[string]bool{}

	// ---- 先改老词释义，再录入新词 ----
	// 顺序反了会出假警告：新词跟老词撞车，但老词这次本来就要补上区分括号。
	// 2026-11-02 第一次 dry-run 就报了 11 条「请在括号里写区分点」，
	// 实际上那 11 个老词的括号在同一个输入里写着，只是还没轮到它改。
	for _, it := range in.DefFix {
		word := strings.TrimSpace(it.Word)
		def := strings.TrimSpace(it.Def)
		if word == "" || def == "" {
			continue
		}
		if arc.SetDefinition(word, def) {
			out.DefFixed++
		} else {
			out.Warnings = append(out.Warnings, fmt.Sprintf("「%s」档案里没有，改不了释义", word))
		}
	}

	// ---- 1/2/3：新增 + 打标 ----
	for _, it := range in.Words {
		word := strings.TrimSpace(it.Word)
		if word == "" {
			continue
		}
		_, gi := review.FindWord(arc.Groups, word)
		if gi >= 0 {
			// 已存在：不重复加，只打标
			out.Duplicates++
		} else if strings.TrimSpace(it.Def) == "" {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("「%s」档案里没有，输入也没给释义 —— 只打了圆级标，没录进词库", word))
		} else {
			group := wordmeta.CircleGroupOf(it)
			// 撞车点名（跟 add-words 同一套规矩，不阻断）
			if others := words.SameDefinitionWords(arc.Groups, it.Def, ""); len(others) > 0 {
				out.DupDefs = append(out.DupDefs, fmt.Sprintf(
					"新词「%s」的释义与已有「%s」完全相同：%s —— 请加括号区分用法",
					word, strings.Join(others, "、"), it.Def))
			} else if others := words.SameCoreWords(arc.Groups, it.Def, ""); len(others) > 0 {
				out.DupCores = append(out.DupCores, fmt.Sprintf(
					"新词「%s」的主释义「%s」与已有「%s」相同 —— 请在括号里写上区分点",
					word, words.DefinitionCore(it.Def), strings.Join(others, "、")))
			}
			arc.AppendWord(group, Word{
				Word:       word,
				Definition: strings.TrimSpace(it.Def),
				Status:     "🔄待测试",
			})
			out.Added++
			groupSet[group] = true
		}

		// 打标：圆级 +（可选）挂起
		p := meta.Items[word]
		if it.Circle != "" {
			p.Circle = it.Circle
		}
		if it.Pos != "" {
			p.Pos = it.Pos
			p.Sub = it.Sub
		}
		if hold && gi < 0 && strings.TrimSpace(it.Def) != "" {
			// 新补录的词才挂起；本来就有的词不动（老师一直在练它们）
			p.Hold = true
		}
		meta.Items[word] = p
		out.Tagged++
		if p.Hold {
			out.Held++
		}
	}

	if len(groupSet) > 0 {
		for g := range groupSet {
			out.Groups = append(out.Groups, g)
		}
		sort.Strings(out.Groups)
	}

	// ---- 5：写回 ----
	needMajor := out.Added >= 20
	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, needMajor)

	desc := fmt.Sprintf("三圆手册对齐：新增%d词/标%d词", out.Added, out.Tagged)
	if out.DefFixed > 0 {
		desc += fmt.Sprintf("/改%d词释义", out.DefFixed)
	}
	if out.Held > 0 {
		desc += fmt.Sprintf("；%d词挂起待学", out.Held)
	}
	if needMajor {
		desc += "；批量导入触发大版本"
	}
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, desc)

	cfg := LangConfigs[st.lang]
	newFilename := archive.Filename(LangConfigs[st.lang].FilePrefix, today, newMajor, newMinor)
	if out.Added > 0 || out.DefFixed > 0 {
		if err := st.UploadArchive(ctx, newFilename, []byte(archive.WriteArchive(arc, cfg.Name, cfg.WordColumn))); err != nil {
			return nil, fmt.Errorf("上传新档案失败：%w", err)
		}
	} else {
		newFilename = oldFilename
	}

	meta.Updated = today.Format("2006-01-02")
	if err := st.UploadWordMeta(ctx, meta); err != nil {
		return nil, fmt.Errorf("上传词性表失败：%w", err)
	}

	out.NewFilename = newFilename
	out.Version = fmt.Sprintf("v%d.%d", newMajor, newMinor)
	out.TotalWords = review.CountAllWords(arc.Groups)
	return out, nil
}

// unholdLesson 放开某一课（把这一课分组里所有词的 hold 清掉）。
// lesson <= 0 表示全部放开。返回清掉的个数。
func unholdLesson(ctx context.Context, st *Storage, lesson int) (int, error) {
	if len(st.lang) == 0 {
		return 0, nil
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil {
		return 0, fmt.Errorf("读取词性表失败：%w", err)
	}
	if meta.Items == nil {
		return 0, nil
	}
	// 先拿到这一课到底有哪些词：hold 名单本身不记课号，得回档案里按分组查。
	arc, _, err := st.EnsureArchive(ctx, st.lang)
	if err != nil {
		return 0, fmt.Errorf("读取档案失败：%w", err)
	}
	n := 0
	for _, g := range arc.Groups {
		if lesson > 0 && wordmeta.LessonOfGroup(g.Title) != lesson {
			continue
		}
		for _, w := range g.Words {
			if p, ok := meta.Items[w.Word]; ok && p.Hold {
				p.Hold = false
				meta.Items[w.Word] = p
				n++
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	meta.Updated = time.Now().Format("2006-01-02")
	if err := st.UploadWordMeta(ctx, meta); err != nil {
		return 0, fmt.Errorf("上传词性表失败：%w", err)
	}
	return n, nil
}
