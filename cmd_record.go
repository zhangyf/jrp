package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zhangyf/jrp/internal/record"
)

func runRecord(fs *flag.FlagSet, lang string) {
	inputFile := fs.String("input", "", "JSON file with review results (default: stdin)")
	hardFlag := fs.Bool("hard", false, "Resolve word numbers against the hard-word plan (from export-hard) instead of the daily plan")
	fs.Parse(cmdArgs)

	// Read input
	var input RecordInput
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

	// The CLI flag wins when set; otherwise the JSON field applies.
	if *hardFlag {
		input.Hard = true
	}

	if input.Language == "" {
		input.Language = lang
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	result, err := ApplyRecord(context.Background(), storage, lang, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	outputResult(result)
}

// RecordResultSummary is the outcome of applying review results to the archive.
//
// 定义已迁 internal/record（那边叫 Result），根包只保留别名 ——
// CLI / HTTP 两入口继续用 RecordResultSummary 这个名字，序列化形态不变。
type RecordResultSummary = record.Result

// 编译期断言：*Storage 隐式满足 record 用例层的最小接口。
var (
	_ record.ArchiveStore = (*Storage)(nil)
	_ record.PlanStore    = (*Storage)(nil)
)

// ApplyRecord downloads the plan + latest archive, applies word/sentence
// results, bumps the version, and uploads the updated archive to COS.
//
// 现在只是一个薄适配：把根包 RecordInput（还带 handler 层的
// SentenceResults / ReviewItems / Language 字段）转成 record.Input 子集，
// 从 LangConfigs 取 Format、注入当前时钟，再调 record.Apply。
// 签名刻意不变，cmd_serve.go 的 HTTP handler 零改动。
func ApplyRecord(ctx context.Context, storage *Storage, lang string, input RecordInput) (*RecordResultSummary, error) {
	cfg := LangConfigs[lang]
	in := record.Input{
		PlanDate:    input.PlanDate,
		WordResults: input.WordResults,
		Hard:        input.Hard,
	}
	format := record.Format{
		Name:       cfg.Name,
		WordColumn: cfg.WordColumn,
		FilePrefix: cfg.FilePrefix,
	}
	return record.Apply(ctx, storage, storage, lang, format, in, time.Now())
}
