package main

// 活用专项练习（2026-09-28 老师新增板块）。
//
//	GET /api/conjugation?type=verb&count=10 → 动词卡：原词（ます形）→ 原形 + て形
//	GET /api/conjugation?type=adj&count=10  → 形容词卡：原词 → 过去式 / 否定形 / 过去否定形（带です）
//
// 设计要点：
//   - 词源 = 档案全词表 × word_meta 词性，只收 pos 能对上号的词；
//     算不出活用的词（词条形态怪）直接跳过，绝不硬算。
//   - 服务端算好标准答案（可多写法），判分放在前端用 grade.js 的 either 档：
//     写假名或汉字都算对，多写法用「/」分隔交给 parseAnswerForms 拆。
//   - 纯自测：不回写档案、不动 ReviewCount —— 老师明确要求「只练不计档」。
//   - 动词后续要加 ない形/た形，就往 conjVerb 的 tasks 里追加，前端自动跟着渲染。

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ConjTask 是一张卡上的一道填空：label（原形/て形/过去式…）+ 可接受写法列表。
type ConjTask struct {
	Label   string   `json:"label"`
	Answers []string `json:"answers"` // 多写法；前端 join("/") 喂给 gradeAnswer
}

// ConjItem 是一张活用卡。
type ConjItem struct {
	Word       string     `json:"word"` // 档案原词形「かな(漢字)」
	Definition string     `json:"definition"`
	Pos        string     `json:"pos"`
	Sub        string     `json:"sub"`
	Tasks      []ConjTask `json:"tasks"`
}

// irregularIStem ます形词干属于「特殊五段」（原形是 る 行不是 う 行）的词，
// 普通规则算出来的原形是错的，遇到直接跳过。
// ⚠️ 键是「ます形去掉ます」后的词干，不是原形也不是ます形本身：
// くださいます→くださ**い**、ございます→ござ**い** —— 这些词的词干以 い 结尾，
// 恰好落在 い段→う段 规则里，会被算成 くださう／ござう 这类不存在的形。
// （いたします→いたし、まいります→まいり、おります→おり 走普通规则是对的，不在表内。）
var irregularIStem = map[string]bool{
	"ください": true, "なさい": true, "いらっしゃい": true, "おっしゃい": true, "ござい": true,
}

// iToU 动1 ます形→原形的 い段→う段映射。
var iToU = map[rune]rune{
	'い': 'う', 'き': 'く', 'ぎ': 'ぐ', 'し': 'す', 'ち': 'つ',
	'に': 'ぬ', 'ひ': 'ふ', 'び': 'ぶ', 'み': 'む', 'り': 'る',
}

// splitHeadLast 取字符串最后一个 rune，返回「去掉末尾的前缀 + 末尾 rune」。
func splitHeadLast(s string) (string, rune, bool) {
	r, size := utf8.DecodeLastRuneInString(s)
	if size == 0 {
		return "", 0, false
	}
	return s[:len(s)-size], r, true
}

// applyItoU 把末尾的 い段假名换成 う段。末尾不是 い段假名 → ok=false。
func applyItoU(s string) (string, bool) {
	head, last, ok := splitHeadLast(s)
	if !ok {
		return "", false
	}
	u, ok2 := iToU[last]
	if !ok2 {
		return "", false
	}
	return head + string(u), true
}

// teEnd 动1 原形→て形的词尾音便。唯一例外 行く→行って（含汉字写法）。
func teEnd(base string) (string, bool) {
	if base == "いく" || strings.HasSuffix(base, "行く") {
		head, _, _ := splitHeadLast(base)
		return head + "って", true // いく→いって / 行く→行って
	}
	head, last, ok := splitHeadLast(base)
	if !ok {
		return "", false
	}
	switch last {
	case 'う', 'つ', 'る':
		return head + "って", true
	case 'む', 'ぶ', 'ぬ':
		return head + "んで", true
	case 'く':
		return head + "いて", true
	case 'ぐ':
		return head + "いで", true
	case 'す':
		return head + "して", true
	}
	return "", false
}

// formAnswer 组装「かな(漢字)」答案串；没有汉字侧就只给假名。
func formAnswer(kanaForm, kanjiForm string) string {
	if kanjiForm != "" && kanjiForm != kanaForm {
		return kanaForm + "(" + kanjiForm + ")"
	}
	return kanaForm
}

// conjVerb 按动词分类算出 原形 / て形 两个答案串（已是「かな(漢字)」格式）。
func conjVerb(kana, kanji, sub string) (base, te string, ok bool) {
	if !strings.HasSuffix(kana, "ます") {
		return "", "", false
	}
	switch sub {
	case "1": // 五段：い段→う段，再按原形词尾音便接て
		stem := strings.TrimSuffix(kana, "ます")
		// 尊敬语特殊五段（〜うではなく〜る）：くださる/なさる/いらっしゃる/おっしゃる/ござる，
		// 普通规则会算成 くださう 这类错形，直接跳过不算。
		if irregularIStem[stem] {
			return "", "", false
		}
		baseKana, ok1 := applyItoU(stem)
		if !ok1 {
			return "", "", false
		}
		teKana, ok2 := teEnd(baseKana)
		if !ok2 {
			return "", "", false
		}
		var baseKanji, teKanji string
		if kanji != "" {
			if bk, ok3 := applyItoU(strings.TrimSuffix(kanji, "ます")); ok3 {
				baseKanji = bk
				teKanji, _ = teEnd(bk)
			}
		}
		return formAnswer(baseKana, baseKanji), formAnswer(teKana, teKanji), true

	case "2": // 一段：去ます＋る／て
		stem := strings.TrimSuffix(kana, "ます")
		if stem == "" {
			return "", "", false
		}
		baseKana, teKana := stem+"る", stem+"て"
		var baseKanji, teKanji string
		if kanji != "" && strings.HasSuffix(kanji, "ます") {
			stemK := strings.TrimSuffix(kanji, "ます")
			baseKanji, teKanji = stemK+"る", stemK+"て"
		}
		return formAnswer(baseKana, baseKanji), formAnswer(teKana, teKanji), true

	case "3": // サ变・カ变
		if strings.HasSuffix(kana, "します") {
			stem := strings.TrimSuffix(kana, "します")
			baseKana, teKana := stem+"する", stem+"して"
			var baseKanji, teKanji string
			if kanji != "" && strings.HasSuffix(kanji, "します") {
				stemK := strings.TrimSuffix(kanji, "します")
				baseKanji, teKanji = stemK+"する", stemK+"して"
			}
			return formAnswer(baseKana, baseKanji), formAnswer(teKana, teKanji), true
		}
		if kana == "きます" && strings.HasPrefix(kanji, "来") { // 来ます 特例
			return formAnswer("くる", "来る"), formAnswer("きて", "来て"), true
		}
		if kana == "します" && kanji == "" { // 裸 します
			return "する", "して", true
		}
	}
	return "", "", false
}

// fixIi い形活用的特例：いい → よい（いい/嫌? 全按 よ 系活用）。
func fixIi(s string) string {
	if strings.HasSuffix(s, "いい") {
		return s[:len(s)-len("いい")] + "よい"
	}
	return s
}

// conjAdjI い形：原词（かな必须以い结尾）→ 过去式／否定形／过去否定形，均带です。
func conjAdjI(kana, kanji string) (past, neg, pastneg string, ok bool) {
	k := fixIi(kana)
	if !strings.HasSuffix(k, "い") || len(k) < len("い")+1 {
		return "", "", "", false
	}
	stem := strings.TrimSuffix(k, "い")
	var stemK string
	if kanji != "" {
		kk := fixIi(kanji)
		if !strings.HasSuffix(kk, "い") {
			return "", "", "", false
		}
		stemK = strings.TrimSuffix(kk, "い")
	}
	return formAnswer(stem+"かったです", suffixK(stemK, "かったです")),
		formAnswer(stem+"くないです", suffixK(stemK, "くないです")),
		formAnswer(stem+"くなかったです", suffixK(stemK, "くなかったです")),
		true
}

// suffixK 汉字侧拼接（没有汉字侧时返回空，由 formAnswer 处理）。
func suffixK(stemK, suffix string) string {
	if stemK == "" {
		return ""
	}
	return stemK + suffix
}

// conjAdjNa ナ形：词干＋でした／じゃありません／じゃありませんでした。
// 否定按老师拍板「按规则判」：じゃ/では × ありません/ないです 全部接受。
func conjAdjNa(kana, kanji string) (past, neg, pastneg string, ok bool) {
	if kana == "" {
		return "", "", "", false
	}
	past = formAnswer(kana+"でした", suffixK(kanji, "でした"))

	negVars := []string{"じゃありません", "ではありません", "じゃないです", "ではないです"}
	pastNegVars := []string{"じゃありませんでした", "ではありませんでした", "じゃなかったです", "ではなかったです"}
	neg = joinVariants(kana, kanji, negVars)
	pastneg = joinVariants(kana, kanji, pastNegVars)
	return past, neg, pastneg, true
}

// joinVariants 把同一词干的多个否定写法拼成「/」分隔的多写法答案串。
func joinVariants(kana, kanji string, suffixes []string) string {
	parts := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		parts = append(parts, formAnswer(kana+s, suffixK(kanji, s)))
	}
	return strings.Join(parts, "/")
}

// conjVerbClass 把词性表里的两种写法统一成 "1"/"2"/"3"。
//
// ⚠️ 词性表有两套写法：2026-09 之前的导入用中文「一类/二类/三类」，
// 第16课起（新增 word-meta 时）改用数字 1/2/3。两套都得认，
// 否则词池会从 111 个动词缩到 6 个（踩过）。
func conjVerbClass(pos, sub string) string {
	if pos != "动词" && pos != "动" {
		return ""
	}
	switch sub {
	case "1", "一类":
		return "1"
	case "2", "二类":
		return "2"
	case "3", "三类":
		return "3"
	}
	return ""
}

// conjAdjClass 形容词同理：い形/1 → "i"，な形/2 → "na"。
func conjAdjClass(pos, sub string) string {
	if pos != "形容词" && pos != "形" {
		return ""
	}
	switch sub {
	case "1", "い形":
		return "i"
	case "2", "な形":
		return "na"
	}
	return ""
}

// conjSkipWord 挡掉「形式上像动词、但不适合出活用题」的条目：
//   - 〜ています / 〜でいます：那是「动词+ている」的活用形，不是辞书形（例：にすんでいます）
//   - 寒暄固定说法：いってきます / いってまいります / よろしくおねがいします
//     （课本词表里标成动词，但出「原形/て形」的题很怪）
func conjSkipWord(word, kana string) bool {
	if strings.HasSuffix(kana, "ています") || strings.HasSuffix(kana, "でいます") {
		return true
	}
	switch word {
	case "いってきます", "いってまいります", "よろしくおねがいします":
		return true
	}
	return false
}

// handleConjugation —— 活用专项出题。只读，不写档案。
func (s *server) handleConjugation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET only",
		})
		return
	}
	typ := r.URL.Query().Get("type")
	if typ != "verb" && typ != "adj" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "type 必须是 verb 或 adj",
		})
		return
	}
	count := 10
	if n, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil {
		if n < 1 {
			n = 1
		}
		if n > 30 {
			n = 30
		}
		count = n
	}

	data, _, err := s.storage.DownloadLatestArchive(bgctx())
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
	meta, err := s.storage.DownloadWordMeta(bgctx())
	if err != nil || meta.Items == nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}

	pool := make([]ConjItem, 0, 128)
	seenKana := map[string]bool{}
	for _, w := range AllWords(arc.Groups) {
		pos, has := meta.Items[w.Word]
		if !has {
			continue
		}
		kana, kanji, _ := splitLexiconWord(w.Word)
		// 档案里有「あおい」和「あおい(青い)」这类同词异形（历史导入留下的），
		// 出题按假名去重，免得同一批里出现两张一样的卡。
		if seenKana[kana] {
			continue
		}

		switch typ {
		case "verb":
			cls := conjVerbClass(pos.Pos, pos.Sub)
			if cls == "" || conjSkipWord(w.Word, kana) {
				continue
			}
			base, te, ok := conjVerb(kana, kanji, cls)
			if !ok {
				continue
			}
			pool = append(pool, ConjItem{
				Word: w.Word, Definition: w.Definition, Pos: pos.Pos, Sub: pos.Sub,
				Tasks: []ConjTask{
					{Label: "原形", Answers: []string{base}},
					{Label: "て形", Answers: []string{te}},
				},
			})
			seenKana[kana] = true

		case "adj":
			cls := conjAdjClass(pos.Pos, pos.Sub)
			if cls == "" {
				continue
			}
			var past, neg, pastneg string
			var ok bool
			if cls == "i" {
				past, neg, pastneg, ok = conjAdjI(kana, kanji)
			} else {
				past, neg, pastneg, ok = conjAdjNa(kana, kanji)
			}
			if !ok {
				continue
			}
			pool = append(pool, ConjItem{
				Word: w.Word, Definition: w.Definition, Pos: pos.Pos, Sub: pos.Sub,
				Tasks: []ConjTask{
					{Label: "过去式", Answers: []string{past}},
					{Label: "否定形", Answers: []string{neg}},
					{Label: "过去否定形", Answers: []string{pastneg}},
				},
			})
			seenKana[kana] = true
		}
	}

	if len(pool) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   "档案里没有可用于活用练习的词条（需要 word_meta 里标注了词性的动词/形容词）",
		})
		return
	}

	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if count > len(pool) {
		count = len(pool)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "type": typ, "count": count,
		"pool": len(pool), "items": pool[:count], "dry_run": s.dryRun,
	})
}
