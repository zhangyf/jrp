package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

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
			"success":  true,
			"command":  "plan-config",
			"reset":    true,
			"dry_run":  *dryRun,
			"effective": builtinPlanConfig(),
			"source":   "builtin",
			"hint":     "COS 上的配置已删除，线上最多 60 秒后回退到内置默认",
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
			cur = builtinPlanConfig() // 没配过：以内置默认当起点，只改指定的键
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
			if !validPlanCount(v) {
				fmt.Fprintf(os.Stderr, "Error: %s=%d 越界，允许 0-%d\n", key, v, maxPlanCount)
				os.Exit(1)
			}
			switch key {
			case "sentence_base_count":
				next.SentenceBaseCount = v
			case "sentence_review_count":
				next.SentenceReviewCount = v
			default:
				fmt.Fprintf(os.Stderr, "Error: 不能改 %q，只能改 sentence_base_count / sentence_review_count\n", key)
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
	eff, source := choosePlanConfig(cos, err)
	outputResult(map[string]interface{}{
		"success":   true,
		"command":   "plan-config",
		"cos_raw":   cos,
		"effective": eff,
		"source":    source,
		"limits":    map[string]int{"min": 0, "max": maxPlanCount},
		"cos_key":   storage.planConfigKey(),
		"hint":      "source=builtin 表示 COS 上没配或配坏了，用的是内置默认 20+20",
	})
}
