package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

// runCircle 三圆手册分级相关命令：
//
//	jrp circle import --input 待导入.json   补录新词 + 打圆级标 + 挂起
//	jrp circle tag    --input 打标.json     只打圆级标，不动档案
//	jrp circle unhold --lesson 21           放开某一课的词（学到那课了）
//
// 三个子命令都带 --dry-run：先演练一遍看要改什么，确认了再真写。
func runCircle(fs *flag.FlagSet, lang string) {
	fs.Parse(cmdArgs)
	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "用法：jrp circle <import|tag|unhold> [选项]\n")
		fmt.Fprintf(os.Stderr, "  import --input <file.json>  补录新词 + 打圆级标（新词默认挂起）\n")
		fmt.Fprintf(os.Stderr, "  tag    --input <file.json>  只打圆级标，不动档案\n")
		fmt.Fprintf(os.Stderr, "  unhold --lesson <N>         放开第 N 课的词（0 = 全部放开）\n")
		os.Exit(1)
	}

	sub := flag.NewFlagSet("circle "+args[0], flag.ExitOnError)
	inputPath := sub.String("input", "", "输入 JSON 文件")
	lesson := sub.Int("lesson", 0, "课号（unhold 用，0 = 全部）")
	noHold := sub.Bool("no-hold", false, "新补录的词不挂起（直接进练习）")
	dryRun := sub.Bool("dry-run", false, "演练：只算不写")
	sub.Parse(args[1:])

	st, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		st.SetDryRun(true)
	}
	ctx := context.Background()

	switch args[0] {
	case "unhold":
		n, err := unholdLesson(ctx, st, *lesson)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		what := fmt.Sprintf("第%d课", *lesson)
		if *lesson == 0 {
			what = "全部课程"
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "circle unhold",
			"lesson": *lesson, "unheld": n, "dry_run": *dryRun,
			"message": fmt.Sprintf("%s：放开 %d 个词", what, n),
		})
		return

	case "import", "tag":
		if *inputPath == "" {
			fmt.Fprintf(os.Stderr, "Error: --input 不能为空\n")
			os.Exit(1)
		}
		var in CircleInput
		if err := readJSONFile(*inputPath, &in); err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败：%v\n", *inputPath, err)
			os.Exit(1)
		}
		if len(in.Words) == 0 && len(in.DefFix) == 0 {
			fmt.Fprintf(os.Stderr, "输入里没有词，什么都不做\n")
			os.Exit(1)
		}
		// tag 只打标：把 def 抹掉，applyCircle 就不会往档案里加词
		hold := !*noHold
		if args[0] == "tag" {
			for i := range in.Words {
				in.Words[i].Def = ""
			}
			in.DefFix = nil
			hold = false
		}
		res, err := applyCircle(ctx, st, &in, hold)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		out := map[string]interface{}{
			"success": true, "command": "circle " + args[0],
			"added": res.Added, "duplicates": res.Duplicates, "tagged": res.Tagged,
			"held": res.Held, "def_fixed": res.DefFixed,
			"old_filename": res.OldFilename, "new_filename": res.NewFilename,
			"version": res.Version, "total_words": res.TotalWords,
			"dry_run": *dryRun,
		}
		if len(res.Groups) > 0 {
			out["groups"] = res.Groups
		}
		if len(res.DupDefs) > 0 {
			out["dup_definitions"] = res.DupDefs
		}
		if len(res.DupCores) > 0 {
			out["dup_core_definitions"] = res.DupCores
		}
		if len(res.Warnings) > 0 {
			out["warnings"] = strings.Join(res.Warnings, "\n")
		}
		outputResult(out)
		return

	default:
		fmt.Fprintf(os.Stderr, "Unknown circle subcommand: %s\n", args[0])
		os.Exit(1)
	}
}
