package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// runWordNote 维护词条的「备注」—— 主要写跟近义词怎么区分。
//
//	jrp --lang ja word-note --word とおります(通ります) --text "空间上从某处穿过；過ぎます 是时间流逝/超过某点"
//	jrp --lang ja word-note --word とおります(通ります) --clear
//	jrp --lang ja word-note --file notes.json     # {"词形": "备注"} 批量合并
//	jrp --lang ja word-note                       # 列出现有备注
//
// 备注存在 word_meta.json 的 items[词形].note，跟词性同一份表。
// 词义明确、不会跟别的词混的就不用写 —— 空着是正常的，不是缺数据。
// 页面上的兜底：这个词没写备注，就把释义括号里的那截当备注显示
// （见前端 app.defParts），所以老词不用补录也有东西看。
func runWordNote(fs *flag.FlagSet, lang string) {
	word := fs.String("word", "", "词形（档案里的完整形式，如 とおります(通ります)）")
	text := fs.String("text", "", "备注文字")
	clear := fs.Bool("clear", false, "清除这个词的备注")
	file := fs.String("file", "", "JSON 文件：{\"词形\": \"备注\"}，合并进已有的表")
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	m, err := storage.DownloadWordMeta(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading word meta: %v\n", err)
		os.Exit(1)
	}
	if m.Items == nil {
		m.Items = map[string]WordPOS{}
	}

	// 没给任何写操作参数 → 只列出现有备注
	if *word == "" && *file == "" {
		listNotes(m)
		return
	}

	changed := 0
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}
		var incoming map[string]string
		if err := jsonUnmarshal(data, &incoming); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			os.Exit(1)
		}
		for w, note := range incoming {
			p := m.Items[w]
			if p.Note != note {
				changed++
			}
			p.Note = note
			m.Items[w] = p
		}
	}

	if *word != "" {
		w := strings.TrimSpace(*word)
		if _, ok := m.Items[w]; !ok {
			// 词形没命中多半是括号/汉字写法对不上，给几个候选比直接报错有用
			fmt.Fprintf(os.Stderr, "Error: 词表里没有这个词：%s\n", w)
			if near := nearKeys(m.Items, w); len(near) > 0 {
				fmt.Fprintf(os.Stderr, "是不是想找：%s\n", strings.Join(near, " / "))
			}
			os.Exit(1)
		}
		p := m.Items[w]
		if p.Note != *text {
			changed++
		}
		p.Note = ""
		if !*clear {
			p.Note = strings.TrimSpace(*text)
		}
		m.Items[w] = p
	}

	if changed == 0 {
		outputResult(map[string]interface{}{
			"success": true, "command": "word-note", "changed": 0,
			"message": "备注没有变化，未上传",
		})
		return
	}

	m.Updated = time.Now().Format("2006-01-02")
	if err := storage.UploadWordMeta(ctx, m); err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading: %v\n", err)
		os.Exit(1)
	}
	outputResult(map[string]interface{}{
		"success": true, "command": "word-note", "changed": changed,
		"total": len(m.Items), "key": storage.wordMetaKey(),
	})
}

// listNotes 列出现有备注（没备注的词不列，否则 900 多行没法看）。
func listNotes(m *WordMeta) {
	rows := make([]map[string]interface{}, 0)
	keys := make([]string, 0, len(m.Items))
	for k := range m.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := m.Items[k]
		if strings.TrimSpace(p.Note) == "" {
			continue
		}
		rows = append(rows, map[string]interface{}{
			"word": k, "pos": p.Pos, "sub": p.Sub, "note": p.Note,
		})
	}
	outputResult(map[string]interface{}{
		"success": true, "command": "word-note", "count": len(rows),
		"total": len(m.Items), "notes": rows,
	})
}

// nearKeys 找出包含给定子串的词形，最多 5 个（子串从 2 个字符起才有效）。
func nearKeys(items map[string]WordPOS, w string) []string {
	key := strings.TrimSpace(w)
	// 去掉括号里的汉字写法再试，老师经常只打假名
	if i := strings.IndexAny(key, "（("); i > 0 {
		key = strings.TrimSpace(key[:i])
	}
	if len([]rune(key)) < 2 {
		return nil
	}
	var out []string
	for k := range items {
		if strings.Contains(k, key) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
