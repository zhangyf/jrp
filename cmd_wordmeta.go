package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
)

// runWordMeta 词性表的上传与查看。
//
//	jrp --lang ja word-meta --file word_meta.json   # 上传（整份覆盖）
//	jrp --lang ja word-meta                         # 下载并打印统计
//
// 词性不在档案里，单独存一份 COS 上的 word_meta.json。
// 上传是整份覆盖 —— 词性表小（930 条约 60KB），没必要做增量合并。
func runWordMeta(fs *flag.FlagSet, lang string) {
	file := fs.String("file", "", "本地 word_meta.json 路径；不给则下载并打印统计")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}
		var m WordMeta
		if err := jsonUnmarshal(data, &m); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			os.Exit(1)
		}
		if len(m.Items) == 0 {
			fmt.Fprintln(os.Stderr, "Error: items is empty, refusing to overwrite")
			os.Exit(1)
		}
		if err := storage.UploadWordMeta(ctx, &m); err != nil {
			fmt.Fprintf(os.Stderr, "Error uploading: %v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "word-meta", "uploaded": len(m.Items),
			"key": storage.wordMetaKey(),
		})
		return
	}

	m, err := storage.DownloadWordMeta(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading: %v\n", err)
		os.Exit(1)
	}
	summary := map[string]int{}
	for _, p := range m.Items {
		key := p.Pos
		if p.Sub != "" {
			key = p.Pos + "·" + p.Sub
		}
		if key == "" {
			key = "未标注"
		}
		summary[key]++
	}
	keys := make([]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, map[string]interface{}{"pos": k, "count": summary[k]})
	}
	outputResult(map[string]interface{}{
		"success": true, "command": "word-meta", "total": len(m.Items),
		"updated": m.Updated, "note": m.Note, "summary": rows,
	})
}
