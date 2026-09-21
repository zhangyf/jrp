package main

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// LexiconItem 是词汇总表的一行。
//
// 老师要的是「看着复习」的浏览表，所以这里把档案里挤在一列的
// 「假名(汉字)」拆成 kana / kanji 两列，再加上词性和阶段。
type LexiconItem struct {
	Number  int    `json:"number"`
	Word    string `json:"word"`    // 原始词形，做键用
	Kana    string `json:"kana"`    // 假名（括号前）
	Kanji   string `json:"kanji"`   // 汉字（半角括号里）
	Note    string `json:"note"`    // 语法注释（全角括号里，如「敬称」「主题助词」）
	Def     string `json:"def"`     // 中文释义
	Pos     string `json:"pos"`     // 词性大类
	Sub     string `json:"sub"`     // 细分：一类/二类/三类、い形/な形
	Status  string `json:"status"`  // 🟢已掌握 / 🟡基本掌握 / 🔴待巩固 ...
	Group   string `json:"group"`   // 课分组
	Reviews int    `json:"reviews"` // 复习总次数
	Errors  int    `json:"errors"`  // 答错次数
	Last    string `json:"last"`    // 最近复习日期
}

// 半角括号 = 汉字标注（如 みず(水)）；全角括号 = 语法注释（如 さん（敬称））。
// 这个区分是看实际数据得来的：930 词里半角 577 个、全角只有 4 个，全角那 4 个
// 括号里是「敬称」「主题助词」这类说明，不是汉字写法。
var (
	lexKanjiRe = regexp.MustCompile(`^(.+?)[(](.+?)[)]$`)
	lexNoteRe  = regexp.MustCompile(`^(.+?)[（](.+?)[）]$`)
)

// splitLexiconWord 把「假名(汉字)」拆成三份。没有括号时 kanji/note 都为空。
func splitLexiconWord(w string) (kana, kanji, note string) {
	w = strings.TrimSpace(w)
	if m := lexKanjiRe.FindStringSubmatch(w); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), ""
	}
	if m := lexNoteRe.FindStringSubmatch(w); m != nil {
		return strings.TrimSpace(m[1]), "", strings.TrimSpace(m[2])
	}
	return w, "", ""
}

// handleLexicon —— 词汇总表。
//
//	GET /api/lexicon → { success, count, items:[...], pos_summary:{...}, groups:[...] }
//
// 词性来自独立的 word_meta.json，不在档案里。两份数据按词形 join，
// 词性表里没有的词 pos 为空字符串（页面显示「—」，不影响其他列）。
func (s *server) handleLexicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET only",
		})
		return
	}
	ctx := s.ctx()

	data, _, err := s.storage.DownloadLatestArchive(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "读取档案失败：" + err.Error(),
		})
		return
	}
	arc, err := ParseArchive(string(data), s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "解析档案失败：" + err.Error(),
		})
		return
	}

	meta, err := s.storage.DownloadWordMeta(ctx)
	if err != nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}
	if meta.Items == nil {
		meta.Items = map[string]WordPOS{}
	}

	words := AllWords(arc.Groups)
	items := make([]LexiconItem, 0, len(words))
	posSummary := map[string]int{}
	groupSet := map[string]bool{}

	for i, w := range words {
		kana, kanji, note := splitLexiconWord(w.Word)
		pos := meta.Items[w.Word]
		it := LexiconItem{
			Number:  i + 1,
			Word:    w.Word,
			Kana:    kana,
			Kanji:   kanji,
			Note:    note,
			Def:     w.Definition,
			Pos:     pos.Pos,
			Sub:     pos.Sub,
			Status:  w.Status,
			Group:   w.Group,
			Reviews: w.ReviewCount,
			Errors:  w.ErrorCount,
			Last:    w.LastReview,
		}
		items = append(items, it)

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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"count":       len(items),
		"items":       items,
		"pos_summary": posSummary,
		"groups":      groups,
		"meta_note":   meta.Note,
		"meta_updated": meta.Updated,
		"dry_run":     s.dryRun,
	})
}
