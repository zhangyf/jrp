package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 加词的唯一实现。
//
// CLI 的 add-words 和小程序的「录入新词」都走这里 —— 校验规则（重复词跳过、
// 中文释义撞车点名、版本号、changelog）只有一份，免得两处各写一套漂掉。
// 2026-10-21 从 cmd_add_words.go 抽出来给录入页用。

// WordToAdd 是一条待录入的词。
type WordToAdd struct {
	Word       string `json:"word"`
	Definition string `json:"definition"`
}

// AddWordsOutcome 是加词的结果，CLI 和 API 都拿它出输出。
type AddWordsOutcome struct {
	Added       int      `json:"added"`
	Duplicates  int      `json:"duplicates"`
	OldFilename string   `json:"old_filename"`
	NewFilename string   `json:"new_filename"`
	Version     string   `json:"version"`
	MajorBump   bool     `json:"major_bump"`
	TotalWords  int      `json:"total_words"`
	DupDefs     []string `json:"dup_definitions,omitempty"`
	DupCores    []string `json:"dup_core_definitions,omitempty"`
}

// addWordsToArchive 把 words 加进 group（没有就新建），写回一份新档案。
//
// ⚠️ 会真的上传新档案。档案是版本化的，旧文件仍在 COS 上，加错了可以拿旧版本回滚。
// group 为空时用「未分组」，不新建空标题的组。
func addWordsToArchive(ctx context.Context, st *Storage, group string, words []WordToAdd, forceMajor bool) (*AddWordsOutcome, error) {
	if strings.TrimSpace(group) == "" {
		group = "未分组"
	}

	data, oldFilename, err := st.DownloadLatestArchive(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取最新档案失败：%w", err)
	}
	arc, err := ParseArchive(string(data), st.lang)
	if err != nil {
		return nil, fmt.Errorf("解析档案失败：%w", err)
	}

	// 找或建组
	idx := -1
	for i := range arc.Groups {
		if arc.Groups[i].Title == group {
			idx = i
			break
		}
	}
	if idx < 0 {
		arc.Groups = append(arc.Groups, WordGroup{Title: group})
		idx = len(arc.Groups) - 1
	}
	target := &arc.Groups[idx]

	// 输入内部先去重，保留第一次出现的释义
	seen := make(map[string]bool, len(words))
	deduped := make([]WordToAdd, 0, len(words))
	for _, w := range words {
		w.Word = strings.TrimSpace(w.Word)
		w.Definition = strings.TrimSpace(w.Definition)
		if w.Word == "" || seen[w.Word] {
			continue
		}
		seen[w.Word] = true
		deduped = append(deduped, w)
	}

	var dupDefs, dupCores []string
	added, duplicates := 0, 0
	for _, w := range deduped {
		if existing, _ := FindWord(arc.Groups, w.Word); existing != nil {
			duplicates++
			continue
		}
		// 中文释义撞车：不阻断，只点名，逼着把区分补进括号里
		if others := sameDefinitionWords(arc.Groups, w.Definition, ""); len(others) > 0 {
			dupDefs = append(dupDefs, fmt.Sprintf("新词「%s」的释义与已有「%s」完全相同：%s —— 请加括号区分用法",
				w.Word, strings.Join(others, "、"), w.Definition))
		} else if others := sameCoreWords(arc.Groups, w.Definition, ""); len(others) > 0 {
			dupCores = append(dupCores, fmt.Sprintf("新词「%s」的主释义「%s」与已有「%s」相同 —— 请在括号里写上区分点（用法/搭配/范围）",
				w.Word, definitionCore(w.Definition), strings.Join(others, "、")))
		}
		target.Words = append(target.Words, Word{
			Word:       w.Word,
			Definition: w.Definition,
			Status:     "🔄待测试",
		})
		added++
	}

	if added == 0 {
		return &AddWordsOutcome{
			Added: 0, Duplicates: duplicates, OldFilename: oldFilename,
			NewFilename: oldFilename, TotalWords: CountAllWords(arc.Groups),
		}, nil
	}

	needMajorBump := forceMajor || added >= 20
	oldDate, oldMajor, oldMinor, _ := ParseFilename(oldFilename)
	today := time.Now()
	newMajor, newMinor := NextVersion(oldDate, oldMajor, oldMinor, today, needMajorBump)

	description := fmt.Sprintf("添加%d词", added)
	if duplicates > 0 {
		description += fmt.Sprintf("（跳过%d重复词）", duplicates)
	}
	if needMajorBump && !forceMajor {
		description += "；批量导入触发大版本"
	}
	AddChangelogEntry(arc, today, newMajor, newMinor, description)

	newFilename := ArchiveFilename(st.lang, today, newMajor, newMinor)
	if err := st.UploadArchive(ctx, newFilename, []byte(WriteArchive(arc))); err != nil {
		return nil, fmt.Errorf("上传新档案失败：%w", err)
	}

	return &AddWordsOutcome{
		Added: added, Duplicates: duplicates, OldFilename: oldFilename,
		NewFilename: newFilename, Version: fmt.Sprintf("v%d.%d", newMajor, newMinor),
		MajorBump: needMajorBump, TotalWords: CountAllWords(arc.Groups),
		DupDefs: dupDefs, DupCores: dupCores,
	}, nil
}
