// cmd_admin.go —— 计划 / 配置 / 模板 / 知识 / 记录 / 账号与运维类命令
//
// 腿一 R1：把原本平铺在根目录的 9 个 cmd_*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；命令行为、输出、存档字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找命令实现先看下面的来源清单。
//
// 来源与顶层声明：
// cmd_gen_plan.go              runGenPlan, dueHold
// cmd_planconfig.go            runPlanConfig
// cmd_judgeprompt.go           runJudgePrompt
// cmd_knowledge.go             runListKnowledge, runGetKnowledge
// cmd_record.go                runRecord, RecordResultSummary, ApplyRecord
// cmd_user.go                  runUser
// cmd_encrypt_env.go           runEncryptEnv
// cmd_decrypt_env.go           runDecryptEnv
// cmd_circle.go                runCircle
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/judge"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/plan"
	"github.com/zhangyf/jrp/internal/planconfig"
	"github.com/zhangyf/jrp/internal/record"
	"github.com/zhangyf/jrp/internal/wordmeta"
)

// ==========================================================================
// 原 cmd_gen_plan.go
// ==========================================================================

func runGenPlan(fs *flag.FlagSet, lang string) {
	outputPath := fs.String("output", "", "Output Excel file path (default: outputs/review_<date>_vA.B.xlsx)")
	sentencesFile := fs.String("sentences", "", "JSON file with sentence exercises [{\"chinese\":\"...\",\"answer\":\"...\"}]")
	dateFlag := fs.String("date", "", "Target date for review plan (YYYY-MM-DD). Defaults to today.")
	sentencesOnly := fs.Bool("sentences-only", false, "Sentences-only mode: no word section at all, drill sentences only (e.g. 50 sentences)")
	fs.Parse(cmdArgs)

	// Determine target date
	var targetDate time.Time
	var err error
	if *dateFlag != "" {
		targetDate, err = time.Parse("2006-01-02", *dateFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing --date (expected YYYY-MM-DD): %v\n", err)
			os.Exit(1)
		}
	} else {
		targetDate = time.Now()
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Download latest archive
	data, archiveFilename, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading latest archive: %v\n", err)
		os.Exit(1)
	}

	// Parse version and date from archive filename (e.g. 日语学习进度档案_260717_v1.6.md → v1.6)
	var arcMajor, arcMinor int
	var arcDate time.Time
	if d, maj, min, perr := archive.ParseFilename(archiveFilename); perr == nil {
		arcMajor, arcMinor = maj, min
		arcDate = d
	} else {
		// Fallback: assume v1.0 on target date
		arcMajor, arcMinor = 1, 0
		arcDate = targetDate
	}

	// Parse archive
	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing archive: %v\n", err)
		os.Exit(1)
	}

	// If the latest archive is from a previous day (no archive exists for the
	// target date yet), initialize today's v1.0 archive so that the review plan
	// uses v1.0 instead of inheriting the previous day's version number. A
	// subsequent `record` will then bump to v1.1 on the same day.
	if !archive.SameDay(arcDate, targetDate) {
		arcMajor, arcMinor = 1, 0
		archive.AddChangelogEntry(arc, targetDate, arcMajor, arcMinor, "新日初始化（gen-plan）")
		cfg := LangConfigs[lang]
		newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
		newFilename := archive.Filename(LangConfigs[lang].FilePrefix, targetDate, arcMajor, arcMinor)
		if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to initialize today's archive: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Initialized new day archive: %s\n", newFilename)
		}
	}

	// Find due words, assign categories, sort, and build the plan (reused by server)
	dateStr := targetDate.Format("2006-01-02")
	meta, _ := storage.DownloadWordMeta(ctx)
	plan := plan.BuildDuePlan(arc, lang, targetDate, wordmeta.HoldSet(meta))

	// Sentences-only mode: drop the entire word section. Requires --sentences.
	if *sentencesOnly {
		if *sentencesFile == "" {
			fmt.Fprintf(os.Stderr, "Error: --sentences-only requires --sentences <file.json>\n")
			os.Exit(1)
		}
		plan.Words = nil
		plan.Kind = "sentences"
	}

	if len(plan.Words) == 0 && !*sentencesOnly {
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "gen-plan",
			"date":      dateStr,
			"due_count": 0,
			"message":   "No words due for review on the target date",
		})
		return
	}

	// Load sentence exercises from file (provided by AI).
	// Defensive nil-guard: BuildDuePlan always returns a fresh ReviewPlan with
	// nil Sentences, but explicit clearing prevents any future code path from
	// accidentally carrying over stale sentences from a previous invocation.
	plan.Sentences = nil
	if *sentencesFile != "" {
		var sentences []struct {
			Chinese string `json:"chinese"`
			Answer  string `json:"answer"`
		}
		if err := readJSONFile(*sentencesFile, &sentences); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading sentences file: %v\n", err)
			os.Exit(1)
		}
		for i, s := range sentences {
			plan.Sentences = append(plan.Sentences, PlanSentence{
				Number:  i + 1,
				Chinese: s.Chinese,
				Answer:  s.Answer,
			})
		}
	}

	// 句子轮换是一个版本事件，但只应在「当天已经出过带句子的复习文件」之后
	// 再次带 --sentences 生成时才 bump。判断依据：当天 changelog 里是否已有
	// 一条含「句子」标记的记录。
	//
	// 新的一天第一次带 --sentences 时，当天档案还是「新日初始化」的 v1.0，不该
	// bump（否则每天第一个复习文件会错误地从 v1.1 起步）。此时用当前版本号并
	// 写一条含「句子」标记的 changelog，供同一天第二次带句子时识别 bump。
	if *sentencesFile != "" {
		dayStr := targetDate.Format("060102")
		rotated := false
		for _, e := range arc.Changelog {
			if e.Date == dayStr && strings.Contains(e.Description, "句子") {
				rotated = true
				break
			}
		}

		desc := fmt.Sprintf("生成复习文件（含句子%d句）", len(plan.Sentences))
		if rotated {
			arcMinor++
			desc = fmt.Sprintf("重新生成复习文件（句子轮换，共%d句）", len(plan.Sentences))
		}
		if *sentencesOnly {
			desc = fmt.Sprintf("生成纯句子复习文件（共%d句）", len(plan.Sentences))
		}

		archive.AddChangelogEntry(arc, targetDate, arcMajor, arcMinor, desc)
		cfg := LangConfigs[lang]
		newContent := archive.WriteArchive(arc, cfg.Name, cfg.WordColumn)
		newFilename := archive.Filename(LangConfigs[lang].FilePrefix, targetDate, arcMajor, arcMinor)
		if err := storage.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to record sentence generation: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Recorded sentence generation: %s\n", newFilename)
		}
	}

	// Generate Excel
	if *outputPath == "" {
		if *sentencesOnly {
			*outputPath = fmt.Sprintf("outputs/review_%s_sentences_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
		} else {
			*outputPath = fmt.Sprintf("outputs/review_%s_v%d.%d.xlsx", dateStr, arcMajor, arcMinor)
		}
	}

	// Ensure output directory exists
	if dir := filepath.Dir(*outputPath); dir != "" {
		os.MkdirAll(dir, 0755)
	}

	if err := offline.GenerateExcel(plan, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating Excel: %v\n", err)
		os.Exit(1)
	}

	// Sentences are only needed locally for the Excel file; the COS plan JSON
	// is consumed by `record` to map word numbers → text and does not need
	// sentences. Stripping them prevents cross-contamination when `serve`
	// regenerates and uploads the same plan key.
	// Exception: in sentences-only mode the sentences ARE the plan, keep them.
	sentenceCount := len(plan.Sentences)
	if !*sentencesOnly {
		plan.Sentences = nil
	}

	// Upload plan JSON to COS. Sentences-only uses its own key so the daily
	// word plan (needed by `record` to resolve numbers → words) is never
	// overwritten by a word-less plan.
	if *sentencesOnly {
		if err := storage.UploadSentencePlan(ctx, plan); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to upload sentence plan: %v\n", err)
		}
	} else if err := storage.UploadPlan(ctx, plan); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload plan: %v\n", err)
	}

	// Upload Excel to COS (backup)
	if *sentencesOnly {
		if err := storage.UploadSentenceExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
		}
	} else if err := storage.UploadExcel(ctx, dateStr, arcMajor, arcMinor, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to upload Excel backup: %v\n", err)
	}

	outputResult(map[string]interface{}{
		"success":        true,
		"command":        "gen-plan",
		"date":           dateStr,
		"kind":           plan.Kind,
		"due_count":      len(plan.Words),
		"sentence_count": sentenceCount,
		"excel_path":     *outputPath,
		"plan_words":     plan.Words,
	})
}

// dueHold 读词性表拿挂起名单。读失败返回 nil —— 照样出题，
// 不因为一份展示增强用的配置读不到就把整个计划清空。
func dueHold(ctx context.Context, st *Storage) *WordMeta {
	m, err := st.DownloadWordMeta(ctx)
	if err != nil {
		return nil
	}
	return m
}

// BuildDuePlan 已迁 internal/plan（P2），CLI gen-plan 与 HTTP /api/plan、
// /api/export、明日预告共用同一份实现。

// ==========================================================================
// 原 cmd_planconfig.go
// ==========================================================================

// runPlanConfig 管理出题配额（存在 COS 上，改它不用重新编译部署）。
//
//	jrp --lang ja plan-config                              看当前生效的配额（来源 + COS 原值）
//	jrp --lang ja plan-config --set sentence_review_count=24   改回炉句配额
//	jrp --lang ja plan-config --set sentence_review_count=24,sentence_base_count=20
//	jrp --lang ja plan-config --reset                      删掉 COS 配置，回退内置 20+20
//	jrp --lang ja plan-config --dry-run                    演练，不落盘
//
// 可改的键只有 sentence_base_count（课本原句数）和 sentence_review_count（回炉变形句数），
// 允许 0 到 50：写 0 等于临时关掉这一类句子，超过 50 会让页面一次拖几百句直接卡死。
//
// ⚠️ 这里一律直读 COS，不走 resolvePlanConfig 的 60 秒缓存 ——
// 否则刚 --set 完立刻 --show 会看到上一轮的旧值，看着像没改成功。
func runPlanConfig(fs *flag.FlagSet, lang string) {
	set := fs.String("set", "", "设置配额，key=value，多个用逗号分隔")
	reset := fs.Bool("reset", false, "删除 COS 上的配置，回退内置默认")
	dryRun := fs.Bool("dry-run", false, "演练：只校验不上传")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		storage.SetDryRun(true)
	}
	ctx := context.Background()

	if *reset {
		if err := storage.DeletePlanConfig(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 删除失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "plan-config",
			"reset":     true,
			"dry_run":   *dryRun,
			"effective": planconfig.BuiltinPlanConfig(),
			"source":    "builtin",
			"hint":      "COS 上的配置已删除，线上最多 60 秒后回退到内置默认",
		})
		return
	}

	if *set != "" {
		cur, err := storage.DownloadPlanConfig(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取现有配置失败：%v\n", err)
			os.Exit(1)
		}
		if cur.SentenceBaseCount == 0 && cur.SentenceReviewCount == 0 {
			cur = planconfig.BuiltinPlanConfig() // 没配过：以内置默认当起点，只改指定的键
		}
		next := cur
		for _, kv := range strings.Split(*set, ",") {
			kv = strings.TrimSpace(kv)
			if kv == "" {
				continue
			}
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "Error: 写法不对（%q），应该是 key=value\n", kv)
				os.Exit(1)
			}
			key := strings.TrimSpace(parts[0])
			v, perr := strconv.Atoi(strings.TrimSpace(parts[1]))
			if perr != nil {
				fmt.Fprintf(os.Stderr, "Error: %s 的值不是整数（%q）\n", key, parts[1])
				os.Exit(1)
			}
			if key == "sentence_review_circle_ratio" {
				if v < 0 || v > 100 {
					fmt.Fprintf(os.Stderr, "Error: %s=%d 越界，允许 0-100（0=不按圆级分层）\n", key, v)
					os.Exit(1)
				}
			} else if !planconfig.ValidPlanCount(v) {
				fmt.Fprintf(os.Stderr, "Error: %s=%d 越界，允许 0-%d\n", key, v, planconfig.MaxPlanCount)
				os.Exit(1)
			}
			switch key {
			case "sentence_base_count":
				next.SentenceBaseCount = v
			case "sentence_review_count":
				next.SentenceReviewCount = v
			case "sentence_review_circle_ratio":
				r := v
				next.SentenceReviewCircleRatio = &r
			default:
				fmt.Fprintf(os.Stderr, "Error: 不能改 %q，只能改 sentence_base_count / sentence_review_count / sentence_review_circle_ratio\n", key)
				os.Exit(1)
			}
		}
		if err := storage.UploadPlanConfig(ctx, next); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "plan-config",
			"dry_run":   *dryRun,
			"before":    cur,
			"effective": next,
			"source":    "cos",
			"hint":      "已写入 COS，线上最多 60 秒后生效，不用重启服务",
		})
		return
	}

	// 默认：查看
	cos, err := storage.DownloadPlanConfig(ctx)
	eff, source := planconfig.ChoosePlanConfig(cos, err)
	// hint 跟着 source 走 —— 写死一句「没配」的话，配好了也会显示这句，反而误导。
	hint := "COS 上的配置正在生效（改完最多 60 秒后线上跟着变）"
	if source == "builtin" {
		hint = "COS 上没配或配坏了，用的是内置默认 20+20"
	}
	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "plan-config",
		"cos_raw":   cos,
		"effective": eff,
		"source":    source,
		"limits":    map[string]int{"min": 0, "max": planconfig.MaxPlanCount},
		"cos_key":   storage.planConfigKey(),
		"hint":      hint,
	})
}

// ==========================================================================
// 原 cmd_judgeprompt.go
// ==========================================================================

// runJudgePrompt 管理造句判定的 prompt 模板（存在 COS 上，改它不用重新部署）。
//
//	jrp --lang ja judge-prompt             看当前生效的模板（来源 + 版本）
//	jrp --lang ja judge-prompt --builtin   打印内置模板（重定向到文件当起点改）
//	jrp --lang ja judge-prompt --file x.txt  上传，校验通过才写
//	jrp --lang ja judge-prompt --reset     删掉 COS 模板，回退内置
//	jrp --lang ja judge-prompt --dry-run   演练，不落盘
//
// 上传前会校验：模板必须含 {answer} 和 {input}，缺一个直接中止 ——
// 写一份缺占位符的模板上去，模型就看不到要判的句子，判分会全线失灵。
func runJudgePrompt(fs *flag.FlagSet, lang string) {
	file := fs.String("file", "", "上传本地模板文件（内容含 {answer} / {chinese} / {input}）")
	builtin := fs.Bool("builtin", false, "打印内置模板到标准输出")
	reset := fs.Bool("reset", false, "删除 COS 上的模板，回退到内置")
	dryRun := fs.Bool("dry-run", false, "演练：只校验不上传")
	_ = fs.Parse(cmdArgs)

	// 内置模板纯本地，不用连 COS
	if *builtin {
		fmt.Print(judge.BuiltinPrompt)
		if !strings.HasSuffix(judge.BuiltinPrompt, "\n") {
			fmt.Println()
		}
		return
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		storage.SetDryRun(true)
	}
	ctx := context.Background()

	// 上传
	if *file != "" {
		data, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取文件失败：%v\n", err)
			os.Exit(1)
		}
		text := strings.TrimSpace(string(data))
		if err := judge.ValidatePrompt(text); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 模板不能用（%v）—— 没写进 COS\n", err)
			os.Exit(1)
		}
		if err := storage.UploadJudgePrompt(ctx, text); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已上传模板（%d 字符，版本 %s），即时生效，不用重启服务\n",
			len(text), judge.PromptVersion(text))
		if !strings.Contains(strings.ToLower(text), "correct") {
			fmt.Println("注意：模板里没提 JSON 输出，判定前会自动补上输出格式要求")
		}
		return
	}

	// 删除
	if *reset {
		if err := storage.DeleteJudgePrompt(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 删除失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已删除 COS 模板，回退内置（版本 %s）\n", judge.PromptVersion(judge.BuiltinPrompt))
		return
	}

	// 查看
	cosText, err := storage.DownloadJudgePrompt(ctx)
	tpl, source := judge.ChoosePrompt(cosText, err)
	fmt.Printf("来源：%s\n版本：%s\n字符数：%d\n\n", source, judge.PromptVersion(tpl), len(tpl))
	fmt.Println(tpl)
	if source == "builtin" && strings.TrimSpace(cosText) != "" {
		fmt.Println("\n（COS 上有一份模板，但校验没过，当前实际用的是内置模板）")
	}
}

// ==========================================================================
// 原 cmd_knowledge.go
// ==========================================================================

// runListKnowledge lists all knowledge documents stored in COS.
func runListKnowledge(fs *flag.FlagSet, lang string) {
	fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	objs, err := storage.ListKnowledge(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing knowledge documents: %v\n", err)
		os.Exit(1)
	}

	type knowledgeItem struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var items []knowledgeItem
	for _, obj := range objs {
		items = append(items, knowledgeItem{
			Name: filepath.Base(obj.Key),
			Size: obj.Size,
		})
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "list-knowledge",
		"language":  lang,
		"count":     len(items),
		"knowledge": items,
	})
}

// runGetKnowledge downloads and prints a specific knowledge document from COS.
func runGetKnowledge(fs *flag.FlagSet, lang string) {
	name := fs.String("name", "", "Knowledge document filename to retrieve")
	fs.Parse(cmdArgs)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: --name is required")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	data, err := storage.DownloadKnowledge(ctx, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading knowledge document: %v\n", err)
		os.Exit(1)
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"command":  "get-knowledge",
		"language": lang,
		"name":     *name,
		"size":     len(data),
		"content":  string(data),
	})
}

// ==========================================================================
// 原 cmd_record.go
// ==========================================================================

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

// ==========================================================================
// 原 cmd_user.go
// ==========================================================================

// runUser 管理登录账号。owner 绑定的兜底手段 —— 万一第一个扫码的不是本人，
// 或者要把儿子从 pending 放进来，不用改代码、不用动 COS 控制台。
//
//	jrp --lang ja user list
//	jrp --lang ja user approve u_7f3a2b9c      批准（pending → member）
//	jrp --lang ja user set-role u_7f3a2b9c owner
//	jrp --lang ja user disable u_7f3a2b9c      踢掉（存量会话立刻失效）
//	jrp --lang ja user rename u_7f3a2b9c 老师
func runUser(fs *flag.FlagSet, lang string) {
	_ = fs.Parse(cmdArgs)
	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: 需要子命令：list | approve <uid> | set-role <uid> <role> | disable <uid> | rename <uid> <名字> | delete <uid>")
		os.Exit(1)
	}

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	users, err := storage.ListUsers(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读用户表失败：%v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		rows := make([]map[string]interface{}, 0, len(users))
		for _, u := range users {
			rows = append(rows, map[string]interface{}{
				"uid": u.UID, "role": u.Role, "name": u.Name,
				"last_login": u.LastLogin, "is_owner": u.UID == "",
			})
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "user", "language": lang,
			"count": len(users), "users": rows,
			"note": "还没人扫码登录过就是空的；第一个登录的人自动是 owner",
		})
		return
	case "approve", "set-role", "disable", "rename", "delete":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Error: %s 需要 <uid>\n", args[0])
			os.Exit(1)
		}
		uid := args[1]
		u, ok := findUserByUID(users, uid)
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: 没有这个用户：%s（先 user list 看看）\n", uid)
			os.Exit(1)
		}
		switch args[0] {
		case "approve":
			u.Role = roleMember
		case "disable":
			u.Role = roleDisabled
		case "set-role":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Error: set-role 需要 <role>：owner | member | pending | disabled")
				os.Exit(1)
			}
			r := strings.ToLower(args[2])
			if r != roleOwner && r != roleMember && r != rolePending && r != roleDisabled {
				fmt.Fprintf(os.Stderr, "Error: 不认识的角色 %q\n", args[2])
				os.Exit(1)
			}
			u.Role = r
		case "rename":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Error: rename 需要 <名字>")
				os.Exit(1)
			}
			u.Name = strings.Join(args[2:], " ")
		case "delete":
			// 只删账号，users/<uid>/ 下的学习数据保留 —— 误删还能把人加回来接着用。
			// ⚠️ owner（uid 为空）删不得：删了就没人能再批准任何人，只能上服务器改 COS。
			if u.UID == "" {
				fmt.Fprintln(os.Stderr, "Error: 不能删除站点所有者")
				os.Exit(1)
			}
			users = removeUser(users, uid)
			if err := storage.SaveUsers(ctx, users); err != nil {
				fmt.Fprintf(os.Stderr, "Error: 写回失败：%v\n", err)
				os.Exit(1)
			}
			outputResult(map[string]interface{}{
				"success": true, "command": "user", "language": lang,
				"uid": uid, "action": "delete",
				"next_step": "账号已删除，学习数据保留；他再用微信扫会重新变成待批准",
			})
			return
		}
		u.LastLogin = time.Now().Format("2006-01-02 15:04:05")
		for i := range users {
			if users[i].UID == u.UID {
				users[i] = u
			}
		}
		if err := storage.SaveUsers(ctx, users); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 写回失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success": true, "command": "user", "language": lang,
			"uid": u.UID, "role": u.Role, "name": u.Name,
			"next_step": "角色变更对存量会话立即生效（每个请求都会重新查用户表）",
		})
		return
	}
	fmt.Fprintf(os.Stderr, "Error: 不认识的子命令 %q\n", args[0])
	os.Exit(1)
}

// ==========================================================================
// 原 cmd_encrypt_env.go
// ==========================================================================

// runEncryptEnv encrypts the plaintext .env into .env.enc (AES-256-GCM), keyed
// to this machine/user/skillDir so it can be read back by `jrp` and cos_node.mjs.
// The old .env.enc (if any) is backed up with a timestamp before overwrite.
//
// This command does not need --lang: it only touches local files and must work
// even when the existing .env.enc is undecryptable (the situation it fixes).
func runEncryptEnv(fs *flag.FlagSet, lang string) {
	fs.Parse(cmdArgs)

	skillDir := cosSkillDir()
	envPath := filepath.Join(skillDir, ".env")
	encPath := filepath.Join(skillDir, ".env.enc")

	plaintext, err := os.ReadFile(envPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read %s: %v\n", envPath, err)
		os.Exit(1)
	}
	if len(plaintext) == 0 {
		fmt.Fprintf(os.Stderr, "Error: %s is empty\n", envPath)
		os.Exit(1)
	}

	enc, err := encryptEnvFile(string(plaintext), skillDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: encrypt failed: %v\n", err)
		os.Exit(1)
	}

	// Back up the existing .env.enc before overwrite.
	if _, err := os.Stat(encPath); err == nil {
		bak := encPath + ".bak." + time.Now().Format("20060102_150405")
		if err := os.Rename(encPath, bak); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot back up old .env.enc: %v\n", err)
			os.Exit(1)
		}
	}

	if err := os.WriteFile(encPath, enc, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write %s: %v\n", encPath, err)
		os.Exit(1)
	}

	// Round-trip verification: re-read and decrypt with the same key.
	back, err := os.ReadFile(encPath)
	if err == nil {
		if _, derr := decryptEnvFile(back, skillDir); derr != nil {
			fmt.Fprintf(os.Stderr, "Error: round-trip verification failed: %v\n", derr)
			os.Exit(1)
		}
	}

	outputResult(map[string]interface{}{
		"success":   true,
		"action":    "encrypt-env",
		"encPath":   encPath,
		"bytes":     len(enc),
		"roundTrip": true,
	})
}

// ==========================================================================
// 原 cmd_decrypt_env.go
// ==========================================================================

// runDecryptEnv prints (or writes out) the plaintext .env that is currently
// stored encrypted in .env.enc. It is the counterpart of `encrypt-env` and
// exists so credentials can be *migrated* out of a directory that is about to
// be replaced (e.g. a marketplace-managed skill dir):
//
//	# read the old location (default: ~/.workbuddy/skills/tencentcloud-cos)
//	jrp decrypt-env --out ~/.workbuddy/cos-credentials/.env
//
//	# then re-encrypt under the new location
//	JRP_COS_SKILL_DIR=~/.workbuddy/cos-credentials jrp encrypt-env
//
// The key is derived from the skill dir, so a .env.enc can only be decrypted
// by pointing JRP_COS_SKILL_DIR at the directory it was encrypted for.
// Secret values are written to a file rather than stdout by default because
// stdout is frequently captured into logs; `--print` forces stdout output.
func runDecryptEnv(fs *flag.FlagSet, lang string) {
	outPath := fs.String("out", "", "Write plaintext .env to this path (recommended; 0600)")
	printOut := fs.Bool("print", false, "Also print plaintext to stdout (leaks into logs)")
	fs.Parse(cmdArgs)

	skillDir := cosSkillDir()
	encPath := filepath.Join(skillDir, ".env.enc")

	enc, err := os.ReadFile(encPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read %s: %v\n", encPath, err)
		os.Exit(1)
	}

	plaintext, err := decryptEnvFile(enc, skillDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: decrypt failed (wrong JRP_COS_SKILL_DIR?): %v\n", err)
		os.Exit(1)
	}

	if *outPath != "" {
		if err := os.MkdirAll(filepath.Dir(*outPath), 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot create dir: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*outPath, []byte(plaintext), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot write %s: %v\n", *outPath, err)
			os.Exit(1)
		}
	}

	if *printOut {
		fmt.Print(plaintext)
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"action":   "decrypt-env",
		"skillDir": skillDir,
		"encPath":  encPath,
		"outPath":  *outPath,
		"bytes":    len(plaintext),
	})
}

// ==========================================================================
// 原 cmd_circle.go
// ==========================================================================

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
