package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/zhangyf/jrp/internal/lexicon"
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

	items, posSummary, groups := lexicon.BuildLexicon(arc, meta)

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

// BuildLexicon 已迁 internal/lexicon（P2），CLI dump-lexicon 与
// GET /api/lexicon 共用同一份实现。
