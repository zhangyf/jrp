package main

import (
	"context"
	"fmt"
	"time"
)

// Draft 是一次「写了一半还没回写」的练习快照。
//
// 存在 COS 而不是浏览器 localStorage —— 老师会在手机、家里的 Mac、办公室的 Windows
// 上打开同一个站，写到一半换设备要能接着写。
//
// 只存老师亲手动过的东西：输入的答案、点的「不会」、勾的「算对」。
// **不存判分结果（correct）** —— 它由 (input, word, grade_mode) 完全决定，恢复时重算即可。
// 这样切判分档位后恢复草稿，不会因为存档时的档位不同而带上过期的对错。
type Draft struct {
	Date      string      `json:"date"`
	Mode      string      `json:"mode"`       // words | hard | sentences | offline
	GradeMode string      `json:"grade_mode"` // 存档时的判分档位，仅用于展示
	SavedAt   string      `json:"saved_at"`
	Items     []DraftItem `json:"items"`
}

type DraftItem struct {
	Number  int    `json:"number"`
	Answer  string `json:"answer"`
	Unknown bool   `json:"unknown"`
	Manual  bool   `json:"manual"`
	// Prompt 题目原文：造句存原句，单词存词形。恢复时用来核对。
	//
	// 造句的 number 只是当天的 1..N 位置号，不是句子的身份。万一同一天
	// 换过一批题（比如手工重跑了 gen-plan），位置对得上、句子却不是同一句，
	// 没有它就会把上一批写的句子糊到下一批上 —— 老师看到的「你写的」
	// 和「正确答案」会驴唇不对马嘴，还容易被当成判分 bug。
	Prompt string `json:"prompt,omitempty"`
}

// HasContent 判断这份草稿有没有实际内容。全空的草稿没必要往 COS 里写，
// 否则老师刚打开页面什么都没填也会留一个垃圾对象。
func (d *Draft) HasContent() bool {
	if d == nil {
		return false
	}
	for _, it := range d.Items {
		if it.Unknown || it.Manual || len(it.Answer) > 0 {
			return true
		}
	}
	return false
}

// draftKey 草稿的 COS 键：drafts/draft_<date>_<mode>.json
//
// 按「日期 + 模式」分键，所以同一天的今日练习和钉子户互不覆盖，
// 隔天的旧草稿也永远不会串到今天。
func (s *Storage) draftKey(date, mode string) string {
	return fmt.Sprintf("%s/drafts/draft_%s_%s.json", s.cosPrefix(), date, mode)
}

// UploadDraft 写草稿。演练模式下不落盘 —— 跟其余 Upload* 保持一致，
// 这样 --dry-run 验收时能确认「一个字节都没写进 COS」。
func (s *Storage) UploadDraft(ctx context.Context, d *Draft) error {
	if s.dryRun {
		return nil
	}
	if d.SavedAt == "" {
		d.SavedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	return s.store.PutObject(ctx, s.draftKey(d.Date, d.Mode), []byte(toJSON(d)))
}

// DownloadDraft 读草稿。
//
// **不存在、解析失败、日期对不上，一律返回 (nil, nil)** —— 草稿是锦上添花的功能，
// 读不到就当没有，绝不能因此让整个练习页打不开。
func (s *Storage) DownloadDraft(ctx context.Context, date, mode string) (*Draft, error) {
	data, err := s.store.GetAll(ctx, s.draftKey(date, mode))
	if err != nil {
		return nil, nil
	}
	var d Draft
	if err := jsonUnmarshal(data, &d); err != nil {
		return nil, nil
	}
	if d.Date != "" && d.Date != date {
		return nil, nil // 陈年草稿，日期对不上就不恢复
	}
	return &d, nil
}

// DeleteDraft 清草稿（回写成功后调）。
//
// 对象本来就不存在时 COS 会返回 404，这里也当成功 —— 清不掉不影响任何功能，
// 下次 load 也只是恢复不到东西。
func (s *Storage) DeleteDraft(ctx context.Context, date, mode string) error {
	if s.dryRun {
		return nil
	}
	_ = s.store.DeleteObject(ctx, s.draftKey(date, mode))
	return nil
}
