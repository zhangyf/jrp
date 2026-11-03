// Package conjugation 提供纯活用规则，仅依赖标准库。
// 不依赖 HTTP、存储、鉴权或词库解析；调用方传入拆好的假名、汉字和词性分类。
package conjugation

import (
	"strings"
	"unicode/utf8"
)

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

// iToA 动1 ます形→ない形 的 い段→あ段映射。
// ⚠️「い」的对应是「わ」不是「あ」：買います→か**わ**ない（❌かあない）。
// 课本原话：「最后一个音为い时，把い变成わ后再加ない（注意不能将い变为あ）」。
var iToA = map[rune]rune{
	'い': 'わ', 'き': 'か', 'ぎ': 'が', 'し': 'さ', 'ち': 'た',
	'に': 'な', 'ひ': 'は', 'び': 'ば', 'み': 'ま', 'り': 'ら',
}

// applyItoA 把末尾的 い段假名换成相应的 あ段（い→わ）。末尾不是 い段假名 → ok=false。
func applyItoA(s string) (string, bool) {
	head, last, ok := splitHeadLast(s)
	if !ok {
		return "", false
	}
	a, ok2 := iToA[last]
	if !ok2 {
		return "", false
	}
	return head + string(a), true
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

// teToTa て形词尾→た形词尾：て→た、で→だ（って→った、んで→んだ…）。
func teToTa(te string) (string, bool) {
	head, last, ok := splitHeadLast(te)
	if !ok {
		return "", false
	}
	switch last {
	case 'て':
		return head + "た", true
	case 'で':
		return head + "だ", true
	}
	return "", false
}

// taEnd 动1 原形→た形的词尾音便。
//
// ⚠️ た形跟て形是**同一套音便**（促音便/拨音便/い音便），只是 て→た、で→だ。
// 所以这里复用 teEnd 再换尾音，绝不把音便规则抄第二份 ——
// 抄两份的话哪天改规则（比如补一个例外）一定漏掉另一边。
// 例外 行く→行った 也跟着 teEnd 走，不用单独写。
func taEnd(base string) (string, bool) {
	te, ok := teEnd(base)
	if !ok {
		return "", false
	}
	return teToTa(te)
}

// formAnswer 组装「かな(漢字)」答案串；没有汉字侧就只给假名。
func formAnswer(kanaForm, kanjiForm string) string {
	if kanjiForm != "" && kanjiForm != kanaForm {
		return kanaForm + "(" + kanjiForm + ")"
	}
	return kanaForm
}

// Verb 按动词分类算出 原形 / て形 / た形 三个答案串（已是「かな(漢字)」格式）。
//
// た形是 2026-11-03 老师要求加的：音便跟て形同源，所以每类只多一行，
// 规则仍然只有一份（见 taEnd）。
func Verb(kana, kanji, sub string) (base, te, ta string, ok bool) {
	if !strings.HasSuffix(kana, "ます") {
		return "", "", "", false
	}
	switch sub {
	case "1": // 五段：い段→う段，再按原形词尾音便接て
		stem := strings.TrimSuffix(kana, "ます")
		// 尊敬语特殊五段（〜うではなく〜る）：くださる/なさる/いらっしゃる/おっしゃる/ござる，
		// 普通规则会算成 くださう 这类错形，直接跳过不算。
		if irregularIStem[stem] {
			return "", "", "", false
		}
		baseKana, ok1 := applyItoU(stem)
		if !ok1 {
			return "", "", "", false
		}
		teKana, ok2 := teEnd(baseKana)
		if !ok2 {
			return "", "", "", false
		}
		taKana, okT := taEnd(baseKana)
		if !okT {
			return "", "", "", false
		}
		var baseKanji, teKanji, taKanji string
		if kanji != "" {
			if bk, ok3 := applyItoU(strings.TrimSuffix(kanji, "ます")); ok3 {
				baseKanji = bk
				teKanji, _ = teEnd(bk)
				taKanji, _ = taEnd(bk)
			}
		}
		return formAnswer(baseKana, baseKanji), formAnswer(teKana, teKanji), formAnswer(taKana, taKanji), true

	case "2": // 一段：去ます＋る／て／た
		stem := strings.TrimSuffix(kana, "ます")
		if stem == "" {
			return "", "", "", false
		}
		baseKana, teKana, taKana := stem+"る", stem+"て", stem+"た"
		var baseKanji, teKanji, taKanji string
		if kanji != "" && strings.HasSuffix(kanji, "ます") {
			stemK := strings.TrimSuffix(kanji, "ます")
			baseKanji, teKanji, taKanji = stemK+"る", stemK+"て", stemK+"た"
		}
		return formAnswer(baseKana, baseKanji), formAnswer(teKana, teKanji), formAnswer(taKana, taKanji), true

	case "3": // サ变・カ变
		if strings.HasSuffix(kana, "します") {
			stem := strings.TrimSuffix(kana, "します")
			baseKana, teKana, taKana := stem+"する", stem+"して", stem+"した"
			var baseKanji, teKanji, taKanji string
			if kanji != "" && strings.HasSuffix(kanji, "します") {
				stemK := strings.TrimSuffix(kanji, "します")
				baseKanji, teKanji, taKanji = stemK+"する", stemK+"して", stemK+"した"
			}
			return formAnswer(baseKana, baseKanji), formAnswer(teKana, teKanji), formAnswer(taKana, taKanji), true
		}
		if kana == "きます" && strings.HasPrefix(kanji, "来") { // 来ます 特例
			return formAnswer("くる", "来る"), formAnswer("きて", "来て"), formAnswer("きた", "来た"), true
		}
		if kana == "します" && kanji == "" { // 裸 します
			return "する", "して", "した", true
		}
	}
	return "", "", "", false
}

// VerbNai 按动词分类算出 原形 / ない形 两个答案串（2026-10-13 老师新增题型）。
// 原形直接复用 Verb —— 变形规则只留一份，不在两处维护。
func VerbNai(kana, kanji, sub string) (base, nai string, ok bool) {
	base, _, _, ok = Verb(kana, kanji, sub)
	if !ok {
		return "", "", false
	}
	switch sub {
	case "1": // 五段：词干末音 い段→あ段（い→わ）＋ない
		stem := strings.TrimSuffix(kana, "ます")
		// ⚠️ あります 是五段，但否定形不规则：不是「あらない」，是「ない」。
		// word_meta 里它标的是「一类」，靠规则躲不过去，必须特判。
		if kana == "あります" {
			return base, "ない", true
		}
		naiKana, ok2 := applyItoA(stem)
		if !ok2 {
			return "", "", false
		}
		var naiKanji string
		if kanji != "" && strings.HasSuffix(kanji, "ます") {
			if ak, ok3 := applyItoA(strings.TrimSuffix(kanji, "ます")); ok3 {
				naiKanji = ak + "ない"
			}
		}
		return base, formAnswer(naiKana+"ない", naiKanji), true

	case "2": // 一段：去ます＋ない
		stem := strings.TrimSuffix(kana, "ます")
		if stem == "" {
			return "", "", false
		}
		var naiKanji string
		if kanji != "" && strings.HasSuffix(kanji, "ます") {
			naiKanji = strings.TrimSuffix(kanji, "ます") + "ない"
		}
		return base, formAnswer(stem+"ない", naiKanji), true

	case "3": // サ变・カ变
		if strings.HasSuffix(kana, "します") {
			stem := strings.TrimSuffix(kana, "します")
			var naiKanji string
			if kanji != "" && strings.HasSuffix(kanji, "します") {
				naiKanji = strings.TrimSuffix(kanji, "します") + "しない"
			}
			return base, formAnswer(stem+"しない", naiKanji), true
		}
		if kana == "きます" && strings.HasPrefix(kanji, "来") { // 来ます → こない
			return base, formAnswer("こない", "来ない"), true
		}
		if kana == "します" && kanji == "" { // 裸 します
			return base, "しない", true
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

// AdjI い形：原词（かな必须以い结尾）→ 过去式／否定形／过去否定形，均带です。
func AdjI(kana, kanji string) (past, neg, pastneg string, ok bool) {
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

// AdjNa ナ形：词干＋でした／じゃありません／じゃありませんでした。
// 否定按老师拍板「按规则判」：じゃ/では × ありません/ないです 全部接受。
func AdjNa(kana, kanji string) (past, neg, pastneg string, ok bool) {
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

// VerbClass 把词性表里的两种写法统一成 "1"/"2"/"3"。
//
// ⚠️ 词性表有两套写法：2026-09 之前的导入用中文「一类/二类/三类」，
// 第16课起（新增 word-meta 时）改用数字 1/2/3。两套都得认，
// 否则词池会从 111 个动词缩到 6 个（踩过）。
func VerbClass(pos, sub string) string {
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

// AdjClass 形容词同理：い形/1 → "i"，な形/2 → "na"。
func AdjClass(pos, sub string) string {
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

// SkipWord 挡掉「形式上像动词、但不适合出活用题」的条目：
//   - 〜ています / 〜でいます：那是「动词+ている」的活用形，不是辞书形（例：にすんでいます）
//   - 寒暄固定说法：いってきます / いってまいります / よろしくおねがいします
//     （课本词表里标成动词，但出「原形/て形」的题很怪）
func SkipWord(word, kana string) bool {
	if strings.HasSuffix(kana, "ています") || strings.HasSuffix(kana, "でいます") {
		return true
	}
	switch word {
	case "いってきます", "いってまいります", "よろしくおねがいします":
		return true
	}
	return false
}
