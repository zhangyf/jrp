// Package record 存放复习结果回写用例（规则+IO）。
//
// 原 ApplyRecord（cmd_record.go）是 CLI 与 HTTP 服务跨端共用的规则+IO 黏合：
// 下载 plan + 最新档案 → 套用单词结果 → bump 版本 → 上传新档案。
// 2026-11-04 按分层设计 P3 收拢到本包：只依赖最小接口 ArchiveStore / PlanStore
// 和注入的 Format / now，不碰 *Storage / COS / HTTP / 全局配置，可独立单测。
//
// ⚠️ 行为不变：编号→词形映射、RecordCorrect/RecordWrong 打点日期、
// 版本/changelog/文件名、计数口径都逐字保留。改任何一处前先看 record_test.go
// 和根包 api_record_test.go 的 TestApplyRecordReviewBoundary。
package record

import (
	"context"
	"fmt"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/review"
)

// Apply 下载 plan + 最新档案，套用单词结果、bump 版本、上传新档案。
// 原 ApplyRecord 逐字搬入；storage.* 换成注入的最小接口，
// LangConfigs[lang] 换成 format、time.Now() 换成 now。
func Apply(ctx context.Context, arcStore ArchiveStore, planStore PlanStore, lang string, format Format, in Input, now time.Time) (*Result, error) {
	var plan *offline.ReviewPlan
	var err error
	if in.Hard {
		plan, err = planStore.DownloadHardPlan(ctx, in.PlanDate)
	} else {
		plan, err = planStore.DownloadPlan(ctx, in.PlanDate)
	}
	if err != nil {
		return nil, fmt.Errorf("downloading plan for date %s (hard=%v): %w", in.PlanDate, in.Hard, err)
	}

	data, oldFilename, err := arcStore.DownloadLatestArchive(ctx)
	if err != nil {
		return nil, fmt.Errorf("downloading latest archive: %w", err)
	}

	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		return nil, fmt.Errorf("parsing archive: %w", err)
	}

	// Build number → word mapping from plan
	planMap := make(map[int]string) // number → word text
	for _, pw := range plan.Words {
		planMap[pw.Number] = pw.Word
	}

	// reviewDate is the date the review actually belongs to (from the plan),
	// NOT the execution/backfill day. The old code passed `now` straight into
	// RecordCorrect/RecordWrong, which stamped every word's LastReview with the
	// backfill day (e.g. 08/22) instead of the real review day. Versioning,
	// changelog and filename still use `now` so a same-day backfill keeps
	// bumping the minor version on the execution day.
	reviewDate := now
	if in.PlanDate != "" {
		if d, err := time.Parse("2006-01-02", in.PlanDate); err == nil {
			reviewDate = d
		}
	}
	correctCount := 0
	wrongCount := 0
	notFound := 0

	for _, r := range in.WordResults {
		wordText, ok := planMap[r.Number]
		if !ok {
			notFound++
			continue
		}

		w, _ := review.FindWord(arc.Groups, wordText)
		if w == nil {
			notFound++
			continue
		}

		if r.Correct {
			review.RecordCorrect(w, reviewDate)
			correctCount++
		} else {
			review.RecordWrong(w, reviewDate)
			wrongCount++
		}
	}

	// Calculate version
	oldDate, oldMajor, oldMinor, _ := archive.ParseFilename(oldFilename)
	newMajor, newMinor := archive.NextVersion(oldDate, oldMajor, oldMinor, now, false)

	// Add changelog entry
	description := fmt.Sprintf("复习结果：%d词写对，%d词写错", correctCount, wrongCount)
	if in.Hard {
		description = "钉子户专项" + description
	}
	if notFound > 0 {
		description += fmt.Sprintf("（%d词未找到）", notFound)
	}
	archive.AddChangelogEntry(arc, now, newMajor, newMinor, description)

	// Write and upload archive
	newContent := archive.WriteArchive(arc, format.Name, format.WordColumn)
	newFilename := archive.Filename(format.FilePrefix, now, newMajor, newMinor)

	if err := arcStore.UploadArchive(ctx, newFilename, []byte(newContent)); err != nil {
		return nil, fmt.Errorf("uploading archive: %w", err)
	}

	return &Result{
		Success:     true,
		Command:     "record",
		PlanDate:    in.PlanDate,
		Correct:     correctCount,
		Wrong:       wrongCount,
		NotFound:    notFound,
		OldFilename: oldFilename,
		NewFilename: newFilename,
		Version:     fmt.Sprintf("v%d.%d", newMajor, newMinor),
		TotalWords:  review.CountAllWords(arc.Groups),
		Hard:        in.Hard,
	}, nil
}
