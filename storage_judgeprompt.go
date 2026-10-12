package main

import (
	"context"
	"strings"
)

// 造句判定用的 prompt 模板，存在 COS 上 —— 改 prompt 不用改代码、不用重新部署。
//
// 这是老师 2026-10-12 定的：整段模板外置，随时改随时生效。代价是没了编译期
// 保护，所以配了四道护栏（全在 api_judge.go 里）：
//   1. 模板必须含 {answer} 和 {input}，缺一个就判坏，回退内置模板。
//   2. 填完占位符后如果通篇没提 JSON 输出，自动在末尾补上输出格式段
//      —— 否则模型开始自由发挥，parseJudgeReply 抠不出结论，判分全线失灵。
//   3. COS 上没有 / 读失败 / 内容为空 → 用内置模板，练习绝不卡住。
//   4. 结果按 60 秒缓存，别每次判定都打 COS。
//
// 模板改动会让旧结论失效：版本取内容 sha256 前 12 位，草稿里的结论记着它，
// 版本对不上就作废重判（见 api_judge.go 的 promptVersion）。

const judgePromptCosKey = "configs/judge_prompt.txt"

func (s *Storage) judgePromptKey() string {
	return s.cosPrefix() + "/" + judgePromptCosKey
}

// DownloadJudgePrompt 读模板。不存在或读失败返回空串 —— 调用方据此回退内置模板。
func (s *Storage) DownloadJudgePrompt(ctx context.Context) (string, error) {
	data, err := s.store.GetAll(ctx, s.judgePromptKey())
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), nil
}

// UploadJudgePrompt 写模板。演练模式下不落盘，跟其余 Upload* 保持一致。
func (s *Storage) UploadJudgePrompt(ctx context.Context, text string) error {
	if s.dryRun {
		return nil
	}
	return s.store.PutObject(ctx, s.judgePromptKey(), []byte(text))
}

// DeleteJudgePrompt 删模板（--reset），删掉即回退到内置模板。
// 对象本来就不存在时 COS 返回 404，这里也当成功。
func (s *Storage) DeleteJudgePrompt(ctx context.Context) error {
	if s.dryRun {
		return nil
	}
	if err := s.store.DeleteObject(ctx, s.judgePromptKey()); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "404") ||
			strings.Contains(err.Error(), "NoSuchKey") {
			return nil
		}
		return err
	}
	return nil
}
