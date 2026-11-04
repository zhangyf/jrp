// Package stats 存放学习进度统计用例（规则+IO）。
//
// 原先 ComputeStats / buildStatsDetail / buildJlptProgress / maxLesson /
// lastIndexOf 散在根包 cmd_stats.go，DTO（StatsResult / StatsSnapshot /
// StatsDetail / JlptProgress 等）散在根包 types.go。2026-11-04 按分层设计
// P3 收拢到本包：DTO 随迁、根包留别名；编排只依赖最小接口 StatsArchiveReader
// 和注入的时钟 now，不碰 *Storage / COS / HTTP / 全局配置，可独立单测。
//
// ⚠️ 行为不变：日期筛选、按天取最新版本、快照字段、changes 文案、
// JLPT 门槛口径都逐字保留。改任何一处前先看 stats_test.go。
package stats

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

// ComputeStats 统计最近 days 天。CLI 与 HTTP handler 共用，两边都只读。
func ComputeStats(ctx context.Context, r StatsArchiveReader, lang string, days int, now time.Time) (*StatsResult, error) {
	refs, err := r.ListArchiveRefs(ctx)
	if err != nil {
		return nil, err
	}

	cutoff := now.AddDate(0, 0, -days)

	// Group archives by date, keep the latest version per date.
	type dateEntry struct {
		date     time.Time
		filename string
		key      string
	}
	dateMap := make(map[string]dateEntry)

	for _, ref := range refs {
		if ref.Date.Before(cutoff) || ref.Date.After(now) {
			continue
		}

		dateKey := ref.Date.Format("060102")
		existing, exists := dateMap[dateKey]
		if !exists || ref.Date.After(existing.date) {
			dateMap[dateKey] = dateEntry{
				date:     ref.Date,
				filename: ref.Filename,
				key:      ref.Key,
			}
		}
	}

	// Sort by date
	var entries []dateEntry
	for _, e := range dateMap {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].date.Before(entries[j].date)
	})

	// Download and parse each archive, extract stats.
	// Also capture the last successfully parsed archive for detail breakdown.
	var snapshots []StatsSnapshot
	var lastArchive *archive.Archive
	for _, e := range entries {
		data, err := r.ReadArchiveBlob(ctx, e.key)
		if err != nil {
			continue
		}

		arc, err := archive.ParseArchive(string(data), lang)
		if err != nil {
			continue
		}
		lastArchive = arc

		allWords := review.AllWords(arc.Groups)
		mastered, basic, needsConsol, untested := review.CountByStatus(allWords)

		// Get version from filename
		_, major, minor, _ := archive.ParseFilename(e.filename)

		snapshots = append(snapshots, StatsSnapshot{
			Date:        e.date.Format("01/02"),
			Version:     fmt.Sprintf("v%d.%d", major, minor),
			Total:       len(allWords),
			Mastered:    mastered,
			Basic:       basic,
			NeedsConsol: needsConsol,
			Untested:    untested,
			Errors:      review.TotalErrors(arc.Groups),
		})
	}

	// Build detail from the last successfully parsed archive (reuse, no second download)
	var detail *StatsDetail
	var jlpt *JlptProgress
	if lastArchive != nil {
		detail = buildStatsDetail(lastArchive.Groups)
		// JLPT 门槛是日语专属口径，其他语言不给，避免前端画出无意义的进度条
		if lang == "ja" {
			jlpt = buildJlptProgress(lastArchive.Groups)
		}
	}

	// Calculate changes
	changes := make(map[string]string)
	if len(snapshots) >= 2 {
		first := snapshots[0]
		last := snapshots[len(snapshots)-1]

		changes["total_change"] = fmt.Sprintf("%d → %d (%+d)", first.Total, last.Total, last.Total-first.Total)
		changes["mastered_change"] = fmt.Sprintf("%d → %d (%+d)", first.Mastered, last.Mastered, last.Mastered-first.Mastered)
		changes["basic_change"] = fmt.Sprintf("%d → %d (%+d)", first.Basic, last.Basic, last.Basic-first.Basic)
		changes["needs_consol_change"] = fmt.Sprintf("%d → %d (%+d)", first.NeedsConsol, last.NeedsConsol, last.NeedsConsol-first.NeedsConsol)
		changes["errors_change"] = fmt.Sprintf("%d → %d (%+d)", first.Errors, last.Errors, last.Errors-first.Errors)
		changes["period"] = fmt.Sprintf("%s ~ %s", first.Date, last.Date)
	}

	return &StatsResult{Snapshots: snapshots, Changes: changes, Detail: detail, Jlpt: jlpt}, nil
}
