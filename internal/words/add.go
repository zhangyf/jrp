package words

import (
	"context"
	"fmt"
	"strings"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

// pendingWrite 是 applyTo 算出来的待写回内容；nil 表示本次不用写档。
type pendingWrite struct {
	filename string
	data     []byte
}

// Add 是「录生词」用例的完整编排：读档案 → 规则 → 写回。
// 三端入口（HTTP handleAddWords / CLI runAddWords）都只调这一份。
//
// ⚠️ 会真的上传新档案。档案是版本化的，旧文件仍在 COS 上，加错了可以拿旧版本回滚。
func Add(ctx context.Context, store ArchiveStore, p Params) (*AddWordsOutcome, error) {
	// EnsureArchive：新账号录第一个词时目录里还没有档案，直接给他造一份空的。
	// 之前这里报「读取最新档案失败：no archives found」，新用户一个词都录不进去。
	arc, oldFilename, err := store.EnsureArchive(ctx, p.Lang)
	if err != nil {
		return nil, err
	}

	out, write := applyTo(arc, oldFilename, p)
	if write != nil {
		if err := store.UploadArchive(ctx, write.filename, write.data); err != nil {
			return nil, fmt.Errorf("上传新档案失败：%w", err)
		}
	}
	return out, nil
}

// applyTo 是纯规则核心：把 words 并进 group（没有就新建），算出去重/撞车/版本/结果，
// 并给出待写回的新档案字节。不碰任何 IO —— 单测直接喂一个 *archive.Archive 即可。
func applyTo(arc *archive.Archive, oldFilename string, p Params) (*AddWordsOutcome, *pendingWrite) {
	group := p.Group
	if strings.TrimSpace(group) == "" {
		group = "未分组"
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
		arc.Groups = append(arc.Groups, review.WordGroup{Title: group})
		idx = len(arc.Groups) - 1
	}
	target := &arc.Groups[idx]

	// 输入内部先去重，保留第一次出现的释义
	seen := make(map[string]bool, len(p.Items))
	deduped := make([]WordToAdd, 0, len(p.Items))
	for _, w := range p.Items {
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
		if existing, _ := review.FindWord(arc.Groups, w.Word); existing != nil {
			duplicates++
			continue
		}
		// 中文释义撞车：不阻断，只点名，逼着把区分补进括号里
		if others := SameDefinitionWords(arc.Groups, w.Definition, ""); len(others) > 0 {
			dupDefs = append(dupDefs, fmt.Sprintf("新词「%s」的释义与已有「%s」完全相同：%s —— 请加括号区分用法",
				w.Word, strings.Join(others, "、"), w.Definition))
		} else if others := SameCoreWords(arc.Groups, w.Definition, ""); len(others) > 0 {
			dupCores = append(dupCores, fmt.Sprintf("新词「%s」的主释义「%s」与已有「%s」相同 —— 请在括号里写上区分点（用法/搭配/范围）",
				w.Word, DefinitionCore(w.Definition), strings.Join(others, "、")))
		}
		target.Words = append(target.Words, review.Word{
			Word:       w.Word,
			Definition: w.Definition,
			Status:     "🔄待测试",
		})
		added++
	}

	if added == 0 {
		return &AddWordsOutcome{
			Added: 0, Duplicates: duplicates, OldFilename: oldFilename,
			NewFilename: oldFilename, TotalWords: review.CountAllWords(arc.Groups),
		}, nil
	}

	needMajorBump := p.ForceMajor || added >= 20
	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	// 时钟在此刻求值：精确对齐旧实现里 today := time.Now() 的位置
	// （EnsureArchive 之后、added>0 分支内），避免入口提前取时间挪动跨午夜边界。
	today := p.Now()
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, today, needMajorBump)

	description := fmt.Sprintf("添加%d词", added)
	if duplicates > 0 {
		description += fmt.Sprintf("（跳过%d重复词）", duplicates)
	}
	if needMajorBump && !p.ForceMajor {
		description += "；批量导入触发大版本"
	}
	archive.AddChangelogEntry(arc, today, newMajor, newMinor, description)

	newFilename := archive.Filename(p.Format.FilePrefix, today, newMajor, newMinor)
	data := []byte(archive.WriteArchive(arc, p.Format.Name, p.Format.WordColumn))

	return &AddWordsOutcome{
		Added: added, Duplicates: duplicates, OldFilename: oldFilename,
		NewFilename: newFilename, Version: fmt.Sprintf("v%d.%d", newMajor, newMinor),
		MajorBump: needMajorBump, TotalWords: review.CountAllWords(arc.Groups),
		DupDefs: dupDefs, DupCores: dupCores,
	}, &pendingWrite{filename: newFilename, data: data}
}
