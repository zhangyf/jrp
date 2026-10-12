package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

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
		fmt.Print(builtinJudgePrompt)
		if !strings.HasSuffix(builtinJudgePrompt, "\n") {
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
		if err := validateJudgePrompt(text); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 模板不能用（%v）—— 没写进 COS\n", err)
			os.Exit(1)
		}
		if err := storage.UploadJudgePrompt(ctx, text); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传失败：%v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已上传模板（%d 字符，版本 %s），即时生效，不用重启服务\n",
			len(text), promptVersion(text))
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
		fmt.Printf("已删除 COS 模板，回退内置（版本 %s）\n", promptVersion(builtinJudgePrompt))
		return
	}

	// 查看
	cosText, err := storage.DownloadJudgePrompt(ctx)
	tpl, source := chooseJudgePrompt(cosText, err)
	fmt.Printf("来源：%s\n版本：%s\n字符数：%d\n\n", source, promptVersion(tpl), len(tpl))
	fmt.Println(tpl)
	if source == "builtin" && strings.TrimSpace(cosText) != "" {
		fmt.Println("\n（COS 上有一份模板，但校验没过，当前实际用的是内置模板）")
	}
}
