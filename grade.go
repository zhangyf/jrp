package main

import (
	"regexp"
	"strings"
)

// 单词判分。
//
// 词的存储约定是「假名(汉字)」：假名是主写法，汉字放括号里。
// 档案里的真实形态：
//
//	おんがく(音楽)       假名 + 汉字
//	ひるごはん(昼ご飯)   汉字部分本身含假名（混写）
//	かいます(買います)   动词，汉字部分含送假名
//	はじめまして         纯假名，无汉字
//	カレー               纯片假名
//
// 括号一律半角 ()，但解析时全角（）也认。
//
// 四档语义见 GradeMode。**前端 web/grade.js 必须与本文件保持一致** —— 这是修掉
// 「网站认汉字、Excel 只认假名」这条不一致的唯一防线。
type GradeMode string

const (
	// GradeKana 假名档（默认）。只比假名部分；照抄全形「假名(汉字)」也算对；
	// 只写汉字不算对（与 Excel 的 WPS 自动批改结论一致）。
	// 片假名词打成平假名也算对 —— 考的是记不记得住读音，不是书写形式。
	GradeKana GradeMode = "kana"

	// GradeKanji 汉字档。只比括号里的汉字写法（或全形）。没有汉字的词自动退回假名档。
	GradeKanji GradeMode = "kanji"

	// GradeFull 完整档。假名 + 汉字都要写出来才算对。最严。
	GradeFull GradeMode = "full"

	// GradeEither 任一档。写假名或写汉字，任一匹配就算对 —— 最宽。
	// 等价于 kana 方式或 kanji 方式判对即可。
	GradeEither GradeMode = "either"
)

// DefaultGradeMode 默认档位。
const DefaultGradeMode = GradeKana

// ParseGradeMode 从字符串解析档位，无法识别时回落到默认档。
func ParseGradeMode(s string) GradeMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "kanji":
		return GradeKanji
	case "full":
		return GradeFull
	case "either":
		return GradeEither
	default:
		return GradeKana
	}
}

// answerForm 一个词的某一种合法写法。
type answerForm struct {
	Kana  string // 假名部分（无括号时等于整串）
	Kanji string // 括号里的汉字写法，可能为空
	Full  string // Kana + Kanji 连写，即「假名(汉字)」去掉括号后的形态
}

// parenRe 匹配「X(Y)」，X / Y 内部不含括号。半角与全角括号都认。
var parenRe = regexp.MustCompile(`^(.+?)[（(](.+?)[）)]$`)

// parseAnswerForms 把一个词拆成若干合法写法。
//
//	おんがく(音楽)      -> {Kana:おんがく, Kanji:音楽, Full:おんがく音楽}
//	はじめまして        -> {Kana:はじめまして, Kanji:"", Full:はじめまして}
//	いい(良い)/よい(良い) -> 两种形态
func parseAnswerForms(word string) []answerForm {
	var out []answerForm
	seen := make(map[string]bool)
	for _, part := range strings.Split(word, "/") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var f answerForm
		if m := parenRe.FindStringSubmatch(part); m != nil {
			f.Kana = strings.TrimSpace(m[1])
			f.Kanji = strings.TrimSpace(m[2])
			f.Full = f.Kana + f.Kanji
		} else {
			f.Kana = part
			f.Full = part
		}
		key := f.Kana + "\x00" + f.Kanji
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

var (
	// Go 的 \s 只认 ASCII 空白，全角空格 U+3000 走 stripReplacer 单独剥。
	spaceRe       = regexp.MustCompile(`\s+`)
	stripReplacer = strings.NewReplacer(
		"(", "", ")", "",
		"（", "", "）", "",
		"・", "", "･", "",
		"\u3000", "", // 全角空格，IME 输入时容易混进来
	)
)

// normalizeAnswer 归一化：去掉所有空白、括号字符、中黑点。
//
// 注意只删括号「字符」，不删括号「内容」—— 所以 としょかん(図書館) 归一化后是
// としょかん図書館，正好等于答案的 Full 形态。
func normalizeAnswer(s string) string {
	s = spaceRe.ReplaceAllString(s, "")
	s = stripReplacer.Replace(s)
	return strings.TrimSpace(s)
}

// inputVariants 把老师的输入拆成若干「候选答案」。
//
// 多写法的词（そう/ああ）在档案里用「/」分隔，但日语 IME 打不出半角斜杠 ——
// IME 里「/」键打出来是中黑点「・」，也有人打顿号「、」。这些分隔符把输入
// 拆开逐段判，任何一段命中任一写法即算对；不含分隔符时整串就是唯一候选。
// 拆完全为空（比如只打了个「・」）返回 nil，按没写处理。
func inputVariants(input string) []string {
	fields := strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case '/', '／', '・', '･', '、': // 半角斜杠 / 全角斜杠 / 全角中黑点 / 半角中黑点 / 顿号
			return true
		}
		return false
	})
	var out []string
	for _, f := range fields {
		if n := normalizeAnswer(f); n != "" {
			out = append(out, n)
		}
	}
	// 兜底：万一哪天词本身含中黑点（「あい・うえ」类复合词），老逻辑靠
	// 「删点整串比」通过 —— 整串归一化结果也放进候选，别让拆段把它挤掉。
	if n := normalizeAnswer(input); n != "" {
		out = append(out, n)
	}
	return out
}

// toHiragana 片假名转平假名，用于假名档的放宽比对。
func toHiragana(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0x30A1 && r <= 0x30F6 { // ァ .. ヶ
			b.WriteRune(r - 0x60)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// matchKanaWay 假名方式的比对：写假名（片假名折叠后也算）或照抄全形都算对。
func matchKanaWay(f answerForm, nin, ninH string) bool {
	if normalizeAnswer(f.Kana) == nin {
		return true
	}
	if toHiragana(normalizeAnswer(f.Kana)) == ninH {
		return true
	}
	return f.Kanji != "" && normalizeAnswer(f.Full) == nin
}

// matchKanjiWay 汉字方式的比对：写汉字或照抄全形都算对。
func matchKanjiWay(f answerForm, nin string) bool {
	return f.Kanji != "" &&
		(normalizeAnswer(f.Kanji) == nin || normalizeAnswer(f.Full) == nin)
}

// GradeAnswer 判定手写答案是否正确。
//
// input 为空一律判错 —— 空白表示「没写」，调用方应在提交前就把它过滤掉，
// 遵循「未出现的词完全不处理」的原则。
func GradeAnswer(input, word string, mode GradeMode) bool {
	forms := parseAnswerForms(word)
	if len(forms) == 0 {
		return false
	}
	variants := inputVariants(input)
	if len(variants) == 0 {
		return false
	}

	// 无汉字的词在汉字档 / 完整档 / 任一档下没有意义，退回假名档。
	hasKanji := false
	for _, f := range forms {
		if f.Kanji != "" {
			hasKanji = true
			break
		}
	}
	if mode != GradeKana && !hasKanji {
		mode = GradeKana
	}

	// 输入的每一段（可能由「・」「、」等分隔出多段）逐个跟全部写法比。
	for _, nin := range variants {
		ninH := toHiragana(nin)
		for _, f := range forms {
			switch mode {
			case GradeKanji:
				if matchKanjiWay(f, nin) {
					return true
				}
			case GradeFull:
				// 假名和汉字都得写出来
				if f.Kanji != "" && normalizeAnswer(f.Full) == nin {
					return true
				}
			case GradeEither:
				if matchKanaWay(f, nin, ninH) || matchKanjiWay(f, nin) {
					return true
				}
			default: // GradeKana
				if matchKanaWay(f, nin, ninH) {
					return true
				}
			}
		}
	}
	return false
}
