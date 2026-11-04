package record

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/offline"
	"github.com/zhangyf/jrp/internal/review"
)

// fakeArchiveStore 是 ArchiveStore 的内存替身，不碰 COS。
type fakeArchiveStore struct {
	latest     []byte
	oldName    string
	uploadErr  error
	uploaded   string
	uploadBlob []byte
}

func (f *fakeArchiveStore) DownloadLatestArchive(ctx context.Context) ([]byte, string, error) {
	return f.latest, f.oldName, nil
}

func (f *fakeArchiveStore) UploadArchive(ctx context.Context, filename string, data []byte) error {
	if f.uploadErr != nil {
		return f.uploadErr
	}
	f.uploaded = filename
	f.uploadBlob = data
	return nil
}

// fakePlanStore 是 PlanStore 的内存替身。
type fakePlanStore struct {
	daily   *offline.ReviewPlan
	hard    *offline.ReviewPlan
	planErr error
}

func (f *fakePlanStore) DownloadPlan(ctx context.Context, date string) (*offline.ReviewPlan, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}
	return f.daily, nil
}

func (f *fakePlanStore) DownloadHardPlan(ctx context.Context, date string) (*offline.ReviewPlan, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}
	return f.hard, nil
}

var jpFormat = Format{Name: "日语", WordColumn: "日语单词", FilePrefix: "日语学习进度档案"}

// seedJaArchive 造一份含两词的日语档案 markdown，配一个过去日期的旧文件名。
func seedJaArchive() []byte {
	arc := &archive.Archive{
		Language: "ja",
		Groups: []review.WordGroup{{Title: "第1课", Words: []review.Word{
			{Word: "みず(水)", Definition: "水", Group: "第1课"},
			{Word: "はし(橋)", Definition: "桥", Group: "第1课"},
		}}},
	}
	return []byte(archive.WriteArchive(arc, "日语", "日语单词"))
}

// 编号→词形映射、PlanDate 回填日期、计数口径、版本/文件名一致性。
func TestApplyBackfillsReviewDateAndCounts(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local) // 执行日
	planDate := "2026-11-02"                               // 真实复习日（要回填）

	arcStore := &fakeArchiveStore{latest: seedJaArchive(), oldName: "日语学习进度档案_200101_v3.0.md"}
	planStore := &fakePlanStore{daily: &offline.ReviewPlan{
		Date: planDate, Language: "ja",
		Words: []offline.PlanWord{
			{Number: 1, Word: "みず(水)"},
			{Number: 2, Word: "はし(橋)"},
		},
	}}

	res, err := Apply(context.Background(), arcStore, planStore, "ja", jpFormat, Input{
		PlanDate: planDate,
		WordResults: []RecordResult{
			{Number: 1, Correct: true},
			{Number: 2, Correct: false},
			{Number: 99, Correct: true}, // 不在 plan → notFound
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	if res.Correct != 1 || res.Wrong != 1 || res.NotFound != 1 || res.TotalWords != 2 {
		t.Fatalf("计数不符：%+v", res)
	}
	if res.OldFilename != arcStore.oldName || res.NewFilename == "" || res.NewFilename == arcStore.oldName {
		t.Fatalf("文件名不符：old=%q new=%q", res.OldFilename, res.NewFilename)
	}
	if res.PlanDate != planDate || res.Hard {
		t.Fatalf("PlanDate/Hard 不符：%+v", res)
	}
	// 旧档案 2001-01-01 → 新的一天 → v1.0；文件名日期取执行日 now（261104）。
	if res.Version != "v1.0" || res.NewFilename != "日语学习进度档案_261104_v1.0.md" {
		t.Fatalf("版本/文件名不符：version=%q new=%q", res.Version, res.NewFilename)
	}

	// 回填日期：LastReview 应是真实复习日 11/02，不是执行日 11/04。
	got, err := archive.ParseArchive(string(arcStore.uploadBlob), "ja")
	if err != nil {
		t.Fatal(err)
	}
	mizu, _ := review.FindWord(got.Groups, "みず(水)")
	hashi, _ := review.FindWord(got.Groups, "はし(橋)")
	if mizu == nil || hashi == nil {
		t.Fatalf("回写后词丢失：mizu=%v hashi=%v", mizu, hashi)
	}
	if mizu.LastReview != "11/02" || hashi.LastReview != "11/02" {
		t.Fatalf("LastReview 应回填为 11/02：みず=%q はし=%q", mizu.LastReview, hashi.LastReview)
	}
	if len(got.Changelog) != 1 {
		t.Fatalf("changelog 应只有新一条：%+v", got.Changelog)
	}
	if got.Changelog[0].Description != "复习结果：1词写对，1词写错（1词未找到）" {
		t.Fatalf("changelog 描述不符：%q", got.Changelog[0].Description)
	}
}

// Hard 走 DownloadHardPlan，changelog 带「钉子户专项」前缀。
func TestApplyHardPlan(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)

	arcStore := &fakeArchiveStore{latest: seedJaArchive(), oldName: "日语学习进度档案_200101_v3.0.md"}
	planStore := &fakePlanStore{
		daily: &offline.ReviewPlan{}, // 不应被读到
		hard: &offline.ReviewPlan{
			Date: "2026-11-02", Language: "ja",
			Words: []offline.PlanWord{{Number: 5, Word: "みず(水)"}},
		},
	}

	res, err := Apply(context.Background(), arcStore, planStore, "ja", jpFormat, Input{
		PlanDate: "2026-11-02", Hard: true,
		WordResults: []RecordResult{{Number: 5, Correct: true}},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Hard || res.Correct != 1 || res.Wrong != 0 {
		t.Fatalf("hard 结果不符：%+v", res)
	}
	got, err := archive.ParseArchive(string(arcStore.uploadBlob), "ja")
	if err != nil {
		t.Fatal(err)
	}
	if got.Changelog[0].Description != "钉子户专项复习结果：1词写对，0词写错" {
		t.Fatalf("hard changelog 描述不符：%q", got.Changelog[0].Description)
	}
}

// 错误透传：plan 下载失败 / 档案上传失败都不吞。
func TestApplyErrorPassthrough(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)

	t.Run("plan download error", func(t *testing.T) {
		arcStore := &fakeArchiveStore{latest: seedJaArchive(), oldName: "日语学习进度档案_200101_v3.0.md"}
		planStore := &fakePlanStore{planErr: errors.New("plan 拉取失败")}
		_, err := Apply(context.Background(), arcStore, planStore, "ja", jpFormat, Input{PlanDate: "2026-11-02"}, now)
		if err == nil || !strings.Contains(err.Error(), "plan 拉取失败") {
			t.Fatalf("plan 错误应透传：%v", err)
		}
	})

	t.Run("upload error", func(t *testing.T) {
		arcStore := &fakeArchiveStore{latest: seedJaArchive(), oldName: "日语学习进度档案_200101_v3.0.md", uploadErr: errors.New("上传失败")}
		planStore := &fakePlanStore{daily: &offline.ReviewPlan{Date: "2026-11-02", Words: []offline.PlanWord{{Number: 1, Word: "みず(水)"}}}}
		_, err := Apply(context.Background(), arcStore, planStore, "ja", jpFormat, Input{
			PlanDate:    "2026-11-02",
			WordResults: []RecordResult{{Number: 1, Correct: true}},
		}, now)
		if err == nil || !strings.Contains(err.Error(), "上传失败") {
			t.Fatalf("upload 错误应透传：%v", err)
		}
	})
}
