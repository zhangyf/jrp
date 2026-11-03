package archive

import (
	"strings"

	"github.com/zhangyf/jrp/internal/review"
)

// ChangelogEntry is one row in the version history table.
type ChangelogEntry struct {
	Date        string
	Version     string
	Total       string
	Mastered    string
	Basic       string
	NeedsConsol string
	Untested    string
	Errors      string
	NailHouse   string
	Description string
}

// Archive represents the full parsed markdown archive.
type Archive struct {
	Language   string
	Changelog  []ChangelogEntry
	Title      string
	LastUpdate string
	Groups     []review.WordGroup
	RawHeader  string // unparsed header text for preservation
	RawFooter  string // unparsed footer text for preservation
}

// AppendWord 把一个词追加进 group（组不存在就新建），返回是否新建了组。
//
// 抽出来是给「三圆手册批量补录」用的：一次要往好几个新建分组里塞词，
// 在调用方写这段「找组/建组」的循环迟早会跟 add-words 那份漂掉。
func (a *Archive) AppendWord(group string, w review.Word) bool {
	if strings.TrimSpace(group) == "" {
		group = "未分组"
	}
	idx := -1
	for i := range a.Groups {
		if a.Groups[i].Title == group {
			idx = i
			break
		}
	}
	created := idx < 0
	if created {
		a.Groups = append(a.Groups, review.WordGroup{Title: group})
		idx = len(a.Groups) - 1
	}
	w.Group = group
	a.Groups[idx].Words = append(a.Groups[idx].Words, w)
	return created
}

// SetDefinition 改某个词的中文释义，返回有没有找到这个词。
//
// 2026-11-02 用来给老词补区分括号 —— 新词跟老词看中文分不出该写哪个时，
// 光给新词加括号不够，老词那头也得写清楚。
func (a *Archive) SetDefinition(word, def string) bool {
	for gi := range a.Groups {
		for wi := range a.Groups[gi].Words {
			if a.Groups[gi].Words[wi].Word == word {
				a.Groups[gi].Words[wi].Definition = def
				return true
			}
		}
	}
	return false
}
