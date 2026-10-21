package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

// sameDefinitionWords 找出档案里释义与 def 一字不差的其他词（exclude 用来排除自己）。
// 中文释义撞车 = 复习时看中文分不出该写哪个词，所以 add-words / update-def 都要点名。
func sameDefinitionWords(groups []WordGroup, def, exclude string) []string {
	if strings.TrimSpace(def) == "" {
		return nil
	}
	var out []string
	for _, g := range groups {
		for _, w := range g.Words {
			if w.Word == exclude || w.Definition != def {
				continue
			}
			out = append(out, w.Word)
		}
	}
	return out
}

// definitionCore 取释义的「主部」：第一个括号之前的部分，去掉尾部标点。
// 「开(电器/灯)」→「开」、「浴室、洗澡（泡澡）」→「浴室、洗澡」。
func definitionCore(def string) string {
	if i := strings.IndexAny(def, "（("); i >= 0 {
		def = def[:i]
	}
	return strings.TrimRight(strings.TrimSpace(def), "、,，;；/／ ")
}

// definitionTerms 把释义主部切成单个义项：「浴室、洗澡」→ [浴室 洗澡]、
// 「通过，经过」→ [通过 经过]。义项是复习时真正被拿来比对的最小单位。
func definitionTerms(def string) []string {
	core := definitionCore(def)
	if core == "" {
		return nil
	}
	parts := strings.FieldsFunc(core, func(r rune) bool {
		return r == '、' || r == '，' || r == ',' || r == '；' || r == ';' ||
			r == '/' || r == '／'
	})
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func termsOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// sameCoreWords 找出「义项撞车」的词：两个词的中文释义只要共享一个义项，
// 复习时看中文就可能分不出该写哪个（2026-09-28 老师反馈：なかなか「相当，很，非常」
// vs とても「非常」——不是一字不差，但照样没区分度；とおります「通过，经过」
// vs すぎます「过」同理）。两边都写了括号区分点（开(门窗) / 开(电器/灯)）算合格，不报。
func sameCoreWords(groups []WordGroup, def, exclude string) []string {
	terms := definitionTerms(def)
	if len(terms) == 0 {
		return nil
	}
	hasNote := strings.ContainsAny(def, "（(")
	var out []string
	for _, g := range groups {
		for _, w := range g.Words {
			if w.Word == exclude {
				continue
			}
			// 两边都写了括号区分点，说明已经处理过，不再点名
			if hasNote && strings.ContainsAny(w.Definition, "（(") {
				continue
			}
			if termsOverlap(terms, definitionTerms(w.Definition)) {
				out = append(out, w.Word)
			}
		}
	}
	return out
}

func runAddWords(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with words to add (default: stdin)")
	groupName := fs.String("group", "", "Override group name for all words")
	majorBump := fs.Bool("major", false, "Force major version bump")
	fs.Parse(cmdArgs)

	// Read input
	var input AddWordsInput
	if *inputFile != "" {
		if err := readJSONFile(*inputFile, &input); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := jsonDecoder(os.Stdin).Decode(&input); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
	}

	if input.Language == "" {
		input.Language = lang
	}
	if *groupName != "" {
		input.Group = *groupName
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	// 加词的实现在 words_add.go，跟「录入新词」的 API 是同一份
	words := make([]WordToAdd, 0, len(input.Words))
	for _, w := range input.Words {
		words = append(words, WordToAdd{Word: w.Word, Definition: w.Definition})
	}

	res, err := addWordsToArchive(context.Background(), storage, input.Group, words, *majorBump)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	out := map[string]interface{}{
		"success":      true,
		"command":      "add-words",
		"added":        res.Added,
		"duplicates":   res.Duplicates,
		"old_filename": res.OldFilename,
		"new_filename": res.NewFilename,
		"version":      res.Version,
		"major_bump":   res.MajorBump,
		"total_words":  res.TotalWords,
	}
	if len(res.DupDefs) > 0 {
		out["dup_definitions"] = res.DupDefs
	}
	if len(res.DupCores) > 0 {
		out["dup_core_definitions"] = res.DupCores
	}
	outputResult(out)
}
