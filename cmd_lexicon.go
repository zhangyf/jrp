package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
)

// runDumpLexicon 导出词汇总表（跟 GET /api/lexicon 同一份数据、同一个拼装函数），
// 只是从命令行直接拿，不用过鉴权 —— 在服务器上核对词库时省一步登录。
// ⛔ 只读：不写档案、不写 word_meta。
func runDumpLexicon(fs *flag.FlagSet, lang string) {
	outPath := fs.String("output", "", "输出文件路径（默认打印到 stdout）")
	fs.Parse(cmdArgs)

	st, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	arc, err := st.LoadArchiveForRead(ctx, lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading archive: %v\n", err)
		os.Exit(1)
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}
	if meta.Items == nil {
		meta.Items = map[string]WordPOS{}
	}

	items, posSummary, groups := buildLexicon(arc, meta)

	payload := map[string]interface{}{
		"success":      true,
		"count":        len(items),
		"items":        items,
		"pos_summary":  posSummary,
		"groups":       groups,
		"meta_note":    meta.Note,
		"meta_updated": meta.Updated,
		"dry_run":      st.dryRun,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", *outPath, err)
			os.Exit(1)
		}
		defer f.Close()
		enc = json.NewEncoder(f)
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding: %v\n", err)
		os.Exit(1)
	}
}

// buildLexicon 把档案 + 词性表拼成词汇总表的行。
// /api/lexicon 和 CLI dump-lexicon 共用这一份，两边不会对不上。
func buildLexicon(arc *Archive, meta *WordMeta) ([]LexiconItem, map[string]int, []string) {
	words := AllWords(arc.Groups)
	items := make([]LexiconItem, 0, len(words))
	posSummary := map[string]int{}
	groupSet := map[string]bool{}

	for i, w := range words {
		kana, kanji, note := splitLexiconWord(w.Word)
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
