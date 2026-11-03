package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
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

// CircleItem 一条待处理的词。
type CircleItem struct {
	Word   string `json:"word"`             // 档案词形：假名(汉字)
	Def    string `json:"def,omitempty"`    // 中文释义（新增时才用；含括号区分点）
	Pos    string `json:"pos,omitempty"`    // 词性大类
	Sub    string `json:"sub,omitempty"`    // 细分：一类/二类/三类、い形/な形
	Circle string `json:"circle"`           // 三圆 / 二圆 / 一圆
	Lesson int    `json:"lesson,omitempty"` // 手册课号，决定新词进哪个组
	Group  string `json:"group,omitempty"`  // 显式指定分组名，优先于 Lesson
}

// CircleInput 一次批量操作的输入。
type CircleInput struct {
	Language string       `json:"language,omitempty"`
	Words    []CircleItem `json:"words"`
	// DefFix 是「只改释义」的老词 —— 给原来没写区分括号的词补上，
	// 免得新词跟老词看中文分不出该写哪个（2026-11-02 补了 10 个）。
	DefFix []CircleItem `json:"def_fix,omitempty"`
}

// CircleOutcome 是执行结果。
type CircleOutcome struct {
	Added       int      `json:"added"`      // 新增进档案的词数
	Duplicates  int      `json:"duplicates"` // 词形已存在，跳过
	Tagged      int      `json:"tagged"`     // 打上/更新圆级标的词数
	Held        int      `json:"held"`       // 被挂起（不进练习）的词数
	DefFixed    int      `json:"def_fixed"`  // 改过释义的老词数
	OldFilename string   `json:"old_filename"`
	NewFilename string   `json:"new_filename"`
	Version     string   `json:"version"`
	TotalWords  int      `json:"total_words"`
	Groups      []string `json:"groups,omitempty"` // 这次动过的分组
	DupDefs     []string `json:"dup_definitions,omitempty"`
	DupCores    []string `json:"dup_core_definitions,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// circleGroupOf 决定一个新词进哪个分组：按手册课号「第N课 手册补录」。
// 课号为 0 又没显式给组名时落「未分组」，跟 add-words 保持一致。
func circleGroupOf(it CircleItem) string {
	if g := strings.TrimSpace(it.Group); g != "" {
		return g
	}
	if it.Lesson > 0 {
		return fmt.Sprintf("第%d课 手册补录", it.Lesson)
	}
	return "未分组"
}

// lessonOfGroup 从分组名里抠课号（「第21课 手册补录」→ 21）。抠不出来返回 0。
// unhold 按课放开时用：老师说「学到第 21 课了」，就把这一课的词放出来。
func lessonOfGroup(title string) int {
	i := strings.Index(title, "第")
	if i < 0 {
		return 0
	}
	rest := title[i+len("第"):]
	j := strings.Index(rest, "课")
	if j <= 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
	if err != nil {
		return 0
	}
	return n
}

// circleMap 摊出「词形 -> 圆级」（三圆 / 二圆 / 一圆），供造句子挑句时加权。
//
// 拿不到词性表（读失败 / 一条都没标）返回 nil —— 调用方按「不分层」处理，
// 出题行为跟加这个功能之前一模一样。宁可少加权，也不能因为读不到词表就少出题。
func circleMap(meta *WordMeta) map[string]string {
	if meta == nil || len(meta.Items) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta.Items))
	for w, p := range meta.Items {
		if c := strings.TrimSpace(p.Circle); c != "" {
			out[w] = c
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// circleRank 圆级分档，数字小的先出：三圆 0、二圆 1、一圆 2、没标 3。
//
// ⚠️ 只认 word_meta 里存的这三个中文值 —— 手册就是这么标的手写分级，
// 写成别的（"3圆"/"三星"）一律落「没标」档，不参与优先。
func circleRank(circle map[string]string, word string) int {
	switch circle[word] {
	case "三圆":
		return 0
	case "二圆":
		return 1
	case "一圆":
		return 2
	}
	return 3
}

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
		_, gi := FindWord(arc.Groups, word)
		if gi >= 0 {
			// 已存在：不重复加，只打标
			out.Duplicates++
		} else if strings.TrimSpace(it.Def) == "" {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("「%s」档案里没有，输入也没给释义 —— 只打了圆级标，没录进词库", word))
		} else {
			group := circleGroupOf(it)
			// 撞车点名（跟 add-words 同一套规矩，不阻断）
			if others := sameDefinitionWords(arc.Groups, it.Def, ""); len(others) > 0 {
				out.DupDefs = append(out.DupDefs, fmt.Sprintf(
					"新词「%s」的释义与已有「%s」完全相同：%s —— 请加括号区分用法",
					word, strings.Join(others, "、"), it.Def))
			} else if others := sameCoreWords(arc.Groups, it.Def, ""); len(others) > 0 {
				out.DupCores = append(out.DupCores, fmt.Sprintf(
					"新词「%s」的主释义「%s」与已有「%s」相同 —— 请在括号里写上区分点",
					word, definitionCore(it.Def), strings.Join(others, "、")))
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
	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, needMajor)

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
	AddChangelogEntry(arc, today, newMajor, newMinor, desc)

	newFilename := ArchiveFilename(st.lang, today, newMajor, newMinor)
	if out.Added > 0 || out.DefFixed > 0 {
		if err := st.UploadArchive(ctx, newFilename, []byte(WriteArchive(arc))); err != nil {
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
	out.TotalWords = CountAllWords(arc.Groups)
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
		if lesson > 0 && lessonOfGroup(g.Title) != lesson {
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
