package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

const usage = `jrp - Language Review Planner

Usage:
  jrp --lang <ja|en|fr> <command> [options]

Commands:
  import       Import archive content from stdin to COS (initial migration)
  add-words    Add new words to the archive
  gen-plan     Generate today's review Excel plan
  record       Record review results and update archive
  update-def   Update a word's definition
  stats           Show statistics for the last N days
  save-lesson     Save a knowledge document to COS
  list-knowledge  List all knowledge documents in COS
  get-knowledge   Download a knowledge document from COS

Global flags:
  --lang string   Language code: ja (Japanese), en (English), fr (French) (required)

Examples:
  jrp --lang ja import < archive.md
  jrp --lang ja add-words --input words.json
  jrp --lang ja gen-plan --output outputs/review.xlsx
  jrp --lang ja record --input results.json
  jrp --lang ja update-def --input def.json
  jrp --lang ja stats --days 7
  jrp --lang ja save-lesson --file lesson.md --name 第9课知识点.md
  jrp --lang ja list-knowledge
  jrp --lang ja get-knowledge --name 第8课知识点.md
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	// Parse global --lang flag
	var lang string
	var cmd string
	cmdArgs = []string{}

	// Find --lang and command
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--lang" && i+1 < len(os.Args) {
			lang = os.Args[i+1]
			i++
		} else if arg == "-h" || arg == "--help" {
			fmt.Print(usage)
			os.Exit(0)
		} else if !strings.HasPrefix(arg, "--") && cmd == "" {
			cmd = arg
		} else {
			cmdArgs = append(cmdArgs, arg)
		}
	}

	if lang == "" {
		fmt.Fprintln(os.Stderr, "Error: --lang is required")
		fmt.Print(usage)
		os.Exit(1)
	}

	if _, ok := LangConfigs[lang]; !ok {
		fmt.Fprintf(os.Stderr, "Error: unsupported language '%s'. Supported: ja, en, fr\n", lang)
		os.Exit(1)
	}

	if cmd == "" {
		fmt.Print(usage)
		os.Exit(1)
	}

	// Re-parse cmdArgs as flags for the specific command
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)

	switch cmd {
	case "import":
		runImport(fs, lang)
	case "add-words":
		runAddWords(fs, lang)
	case "gen-plan":
		runGenPlan(fs, lang)
	case "record":
		runRecord(fs, lang)
	case "update-def":
		runUpdateDef(fs, lang)
	case "stats":
		runStats(fs, lang)
	case "save-lesson":
		runSaveLesson(fs, lang)
	case "list-knowledge":
		runListKnowledge(fs, lang)
	case "get-knowledge":
		runGetKnowledge(fs, lang)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		fmt.Print(usage)
		os.Exit(1)
	}
}

// cmdArgs holds the command-specific arguments (flags) parsed from the command line.
var cmdArgs []string

// outputResult writes a JSON result to stdout.
func outputResult(result interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(result)
}
