package stats

import (
	"context"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

// fakeReader 是 StatsArchiveReader 的内存替身，不碰 COS。
type fakeReader struct {
	refs []ArchiveRef
	blob map[string][]byte
}

func (f *fakeReader) ListArchiveRefs(ctx context.Context) ([]ArchiveRef, error) {
	return f.refs, nil
}

func (f *fakeReader) ReadArchiveBlob(ctx context.Context, key string) ([]byte, error) {
	return f.blob[key], nil
}

// seedArchiveAt 造一份合法档案 markdown + 对应文件名，塞进 fake reader。
// Filename 用 archive.Filename 生成，保证能被 ComputeStats 的 ParseFilename 解析出版本号。
func seedArchiveAt(t *testing.T, day string, major, minor int, words []review.Word) (key, filename string, md []byte) {
	t.Helper()
	date, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	filename = archive.Filename("日语学习进度档案", date, major, minor)
	key = "language-review/ja/archives/" + filename
	arc := &archive.Archive{
		Language: "ja",
		Groups:   []review.WordGroup{{Title: "第1课", Words: words}},
	}
	md = []byte(archive.WriteArchive(arc, "日语", "日语单词"))
	return key, filename, md
}

// 编排：两天各一份档案 → 快照按日期升序、changes 计算、detail/jlpt 来自最后一份。
func TestComputeStatsSnapshotsAndChanges(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)

	key1, fn1, md1 := seedArchiveAt(t, "2026-11-02", 1, 0, []review.Word{
		{Word: "みず(水)", Definition: "水", Group: "第1课"},
	})
	key2, fn2, md2 := seedArchiveAt(t, "2026-11-03", 1, 0, []review.Word{
		{Word: "みず(水)", Definition: "水", Group: "第1课", ReviewCount: 10, ErrorCount: 1},
		{Word: "はし(橋)", Definition: "桥", Group: "第1课"},
	})

	r := &fakeReader{
		refs: []ArchiveRef{
			{Key: key1, Filename: fn1, Date: time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)},
			{Key: key2, Filename: fn2, Date: time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local)},
		},
		blob: map[string][]byte{key1: md1, key2: md2},
	}

	res, err := ComputeStats(context.Background(), r, "ja", 7, now)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Snapshots) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(res.Snapshots))
	}
	if res.Snapshots[0].Total != 1 || res.Snapshots[1].Total != 2 {
		t.Errorf("snapshots Total 不符: %+v", res.Snapshots)
	}
	if res.Snapshots[0].Date != "11/02" || res.Snapshots[1].Date != "11/03" {
		t.Errorf("snapshots Date 不符: %+v", res.Snapshots)
	}
	if res.Snapshots[1].Version != "v1.0" {
		t.Errorf("snapshots Version 不符: %+v", res.Snapshots[1])
	}
	if res.Changes["total_change"] != "1 → 2 (+1)" {
		t.Errorf("total_change = %q", res.Changes["total_change"])
	}
	if res.Changes["period"] != "11/02 ~ 11/03" {
		t.Errorf("period = %q", res.Changes["period"])
	}

	if res.Detail == nil || len(res.Detail.ByLesson) != 1 {
		t.Fatalf("detail 应来自最后一份档案: %+v", res.Detail)
	}
	if res.Jlpt == nil || res.Jlpt.VocabTotal != 2 {
		t.Fatalf("jlpt 应为日语且 2 词: %+v", res.Jlpt)
	}
	if res.Jlpt.VocabFirm != 1 { // 只有 みず 复习过且 90%
		t.Errorf("VocabFirm = %d, want 1", res.Jlpt.VocabFirm)
	}
}

// 同一天多版本只保留遍历序第一份（ListAllArchives 已按 LastModified 降序）；
// 超出 cutoff 的旧档案被过滤。
func TestComputeStatsKeepsLatestPerDayAndDropsOld(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)

	keyNew, fnNew, mdNew := seedArchiveAt(t, "2026-11-03", 1, 1, []review.Word{
		{Word: "みず(水)", Definition: "水", Group: "第1课"},
	})
	keyOld, fnOld, mdOld := seedArchiveAt(t, "2026-11-03", 1, 0, []review.Word{
		{Word: "みず(水)", Definition: "水", Group: "第1课"},
	})
	keyStale, fnStale, mdStale := seedArchiveAt(t, "2026-10-25", 1, 0, []review.Word{
		{Word: "ふるい(古い)", Definition: "旧", Group: "第1课"},
	})

	r := &fakeReader{
		// 顺序模拟 ListAllArchives 的「新在前」：同一天 v1.1 先于 v1.0，10-25 在最末。
		refs: []ArchiveRef{
			{Key: keyNew, Filename: fnNew, Date: time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local)},
			{Key: keyOld, Filename: fnOld, Date: time.Date(2026, 11, 3, 0, 0, 0, 0, time.Local)},
			{Key: keyStale, Filename: fnStale, Date: time.Date(2026, 10, 25, 0, 0, 0, 0, time.Local)},
		},
		blob: map[string][]byte{keyNew: mdNew, keyOld: mdOld, keyStale: mdStale},
	}

	res, err := ComputeStats(context.Background(), r, "ja", 7, now)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1（同天去重 + 旧档过滤）", len(res.Snapshots))
	}
	if res.Snapshots[0].Version != "v1.1" {
		t.Errorf("应保留遍历序第一份 v1.1，实际 %q", res.Snapshots[0].Version)
	}
	if res.Snapshots[0].Date != "11/03" {
		t.Errorf("Date = %q, want 11/03", res.Snapshots[0].Date)
	}
}
