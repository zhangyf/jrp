// Package lexicon 存放词汇总表的 DTO、词形拆分规则与拼装逻辑。
//
// 原先 LexiconItem / splitLexiconWord 在根包 api_lexicon.go、buildLexicon
// 在根包 cmd_lexicon.go，被 CLI dump-lexicon、GET /api/lexicon、体验版假数据、
// 活用练习、查释义四处共用。2026-11-04 按分层设计 P2 收拢到本包。
//
// ⚠️ DTO 随迁：LexiconItem 的字段顺序与 json tag 是 /api/lexicon 的响应契约，
// 根包留 type LexiconItem = lexicon.LexiconItem 别名，序列化字节不变。
// 动 struct 前先看 lexicon_test.go 的 JSON 字节对拍。
package lexicon

import (
	"regexp"
	"sort"
	"strings"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/wordmeta"
)

// LexiconItem 是词汇总表的一行。
//
// 老师要的是「看着复习」的浏览表，所以这里把档案里挤在一列的
// 「假名(汉字)」拆成 kana / kanji 两列，再加上词性和阶段。
type LexiconItem struct {
	Number   int    `json:"number"`
	Word     string `json:"word"`      // 原始词形，做键用
	Kana     string `json:"kana"`      // 假名（括号前）
	Kanji    string `json:"kanji"`     // 汉字（半角括号里）
	Note     string `json:"note"`      // 语法注释（全角括号里，如「敬称」「主题助词」）
	WordNote string `json:"word_note"` // 近义区分备注（word_meta.json 的 note，跟练习卡片同一份）
	Def      string `json:"def"`       // 中文释义
	Pos      string `json:"pos"`       // 词性大类
	Sub      string `json:"sub"`       // 细分：一类/二类/三类、い形/な形
	Status   string `json:"status"`    // 🟢已掌握 / 🟡基本掌握 / 🔴待巩固 ...
	Group    string `json:"group"`     // 课分组
	Circle   string `json:"circle"`    // 手册分级：三圆 / 二圆 / 一圆（手册没收录则为空）
	Hold     bool   `json:"hold"`      // true = 挂起，不进每日练习（还没学到）
	Reviews  int    `json:"reviews"`   // 复习总次数
	Errors   int    `json:"errors"`    // 答错次数
	Last     string `json:"last"`      // 最近复习日期
}

// 半角括号 = 汉字标注（如 みず(水)）；全角括号 = 语法注释（如 さん（敬称））。
// 这个区分是看实际数据得来的：930 词里半角 577 个、全角只有 4 个，全角那 4 个
// 括号里是「敬称」「主题助词」这类说明，不是汉字写法。
var (
	lexKanjiRe = regexp.MustCompile(`^(.+?)[(](.+?)[)]$`)
	lexNoteRe  = regexp.MustCompile(`^(.+?)[（](.+?)[）]$`)
)

// SplitLexiconWord 把「假名(汉字)」拆成三份。没有括号时 kanji/note 都为空。
func SplitLexiconWord(w string) (kana, kanji, note string) {
	w = strings.TrimSpace(w)
	if m := lexKanjiRe.FindStringSubmatch(w); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), ""
	}
	if m := lexNoteRe.FindStringSubmatch(w); m != nil {
		return strings.TrimSpace(m[1]), "", strings.TrimSpace(m[2])
	}
	return w, "", ""
}

// BuildLexicon 把档案 + 词性表拼成词汇总表的行。
// /api/lexicon 和 CLI dump-lexicon 共用这一份，两边不会对不上。
func BuildLexicon(arc *archive.Archive, meta *wordmeta.WordMeta) ([]LexiconItem, map[string]int, []string) {
	words := review.AllWords(arc.Groups)
	items := make([]LexiconItem, 0, len(words))
	posSummary := map[string]int{}
	groupSet := map[string]bool{}

	for i, w := range words {
		kana, kanji, note := SplitLexiconWord(w.Word)
		pos := meta.Items[w.Word]
		items = append(items, LexiconItem{
			Number:   i + 1,
			Word:     w.Word,
			Kana:     kana,
			Kanji:    kanji,
			Note:     note,
			WordNote: pos.Note,
			Def:      w.Definition,
			Pos:      pos.Pos,
			Sub:      pos.Sub,
			Status:   w.Status,
			Group:    w.Group,
			Circle:   pos.Circle,
			Hold:     pos.Hold,
			Reviews:  w.ReviewCount,
			Errors:   w.ErrorCount,
			Last:     w.LastReview,
		})

		key := pos.Pos
		if key == "" {
			key = "未标注"
		}
		posSummary[key]++
		if w.Group != "" {
			groupSet[w.Group] = true
		}
	}

	groups := make([]string, 0, len(groupSet))
	for g := range groupSet {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return items, posSummary, groups
}
