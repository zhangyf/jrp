package words

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
)

// fakeArchiveStore 是 ArchiveStore 的内存实现：EnsureArchive 直接返回给定档案，
// UploadArchive 把写回内容记下来供断言。用例层不碰 COS 就能完整测编排。
type fakeArchiveStore struct {
	arc       *archive.Archive
	filename  string
	ensureErr error
	uploadErr error
	uploads   []fakeUpload
}

type fakeUpload struct {
	filename string
	data     []byte
}

func (f *fakeArchiveStore) EnsureArchive(_ context.Context, _ string) (*archive.Archive, string, error) {
	if f.ensureErr != nil {
		return nil, "", f.ensureErr
	}
	return f.arc, f.filename, nil
}

func (f *fakeArchiveStore) UploadArchive(_ context.Context, filename string, data []byte) error {
	if f.uploadErr != nil {
		return f.uploadErr
	}
	f.uploads = append(f.uploads, fakeUpload{filename: filename, data: append([]byte(nil), data...)})
	return nil
}

var testNow = time.Date(2026, 11, 4, 10, 30, 0, 0, time.Local)

const (
	testOldFilename = "日语学习进度档案_261104_v1.0.md"
	testNewFilename = "日语学习进度档案_261104_v1.1.md"
)

func testFormat() Format {
	return Format{Name: "日语", WordColumn: "日语单词", FilePrefix: "日语学习进度档案"}
}

func newTestStore(groups ...review.WordGroup) *fakeArchiveStore {
	return &fakeArchiveStore{
		arc:      &archive.Archive{Language: "ja", Groups: groups},
		filename: testOldFilename,
	}
}

func testParams(items ...WordToAdd) Params {
	return Params{
		Group:  "第1课",
		Items:  items,
		Lang:   "ja",
		Format: testFormat(),
		Now:    func() time.Time { return testNow },
	}
}

// 正常加词：一个新词 + 一个重复词，版本 minor+1，写回字节含新词与 changelog。
func TestAddNormal(t *testing.T) {
	st := newTestStore(review.WordGroup{Title: "第1课", Words: []review.Word{
		{Word: "みず(水)", Definition: "水", Status: "🟢已掌握"},
	}})
	out, err := Add(context.Background(), st, testParams(
		WordToAdd{Word: " ほし(星) ", Definition: " 星星 "},
		WordToAdd{Word: "みず(水)", Definition: "水"},
	))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if out.Added != 1 || out.Duplicates != 1 {
		t.Errorf("Added/Duplicates = %d/%d, want 1/1", out.Added, out.Duplicates)
	}
	if out.Version != "v1.1" || out.NewFilename != testNewFilename {
		t.Errorf("Version/NewFilename = %q/%q, want v1.1/%s", out.Version, out.NewFilename, testNewFilename)
	}
	if out.OldFilename != testOldFilename || out.TotalWords != 2 || out.MajorBump {
		t.Errorf("OldFilename/TotalWords/MajorBump = %q/%d/%v", out.OldFilename, out.TotalWords, out.MajorBump)
	}
	if len(st.uploads) != 1 {
		t.Fatalf("uploads = %d, want 1", len(st.uploads))
	}
	got := string(st.uploads[0].data)
	for _, want := range []string{"ほし(星)", "星星", "添加1词（跳过1重复词）", "### 第1课"} {
		if !strings.Contains(got, want) {
			t.Errorf("写回档案缺 %q", want)
		}
	}
	// 写回内容能被解析回等价档案
	parsed, err := archive.ParseArchive(got, "ja")
	if err != nil {
		t.Fatalf("ParseArchive(写回内容): %v", err)
	}
	if review.CountAllWords(parsed.Groups) != 2 {
		t.Errorf("解析回词数 = %d, want 2", review.CountAllWords(parsed.Groups))
	}
}

// group 为空 → 落「未分组」，不新建空标题组。
func TestAddEmptyGroupDefaultsTo未分组(t *testing.T) {
	st := newTestStore()
	p := testParams(WordToAdd{Word: "あめ(雨)", Definition: "雨"})
	p.Group = ""
	if _, err := Add(context.Background(), st, p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(st.uploads) != 1 || !strings.Contains(string(st.uploads[0].data), "### 未分组") {
		t.Errorf("空 group 没落「未分组」：uploads=%d", len(st.uploads))
	}
}

// 输入内部去重 + 空词跳过。
func TestAddDedupesInput(t *testing.T) {
	st := newTestStore()
	out, err := Add(context.Background(), st, testParams(
		WordToAdd{Word: "あめ(雨)", Definition: "雨"},
		WordToAdd{Word: "あめ(雨)", Definition: "雨（重复）"},
		WordToAdd{Word: "   ", Definition: "空词"},
	))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if out.Added != 1 || out.Duplicates != 0 {
		t.Errorf("Added/Duplicates = %d/%d, want 1/0", out.Added, out.Duplicates)
	}
}

// 全部是重复词 → added==0，不写档，NewFilename 保持旧文件名（跟旧实现一致）。
func TestAddNoNewWordsSkipsUpload(t *testing.T) {
	st := newTestStore(review.WordGroup{Title: "第1课", Words: []review.Word{
		{Word: "みず(水)", Definition: "水"},
	}})
	out, err := Add(context.Background(), st, testParams(WordToAdd{Word: "みず(水)", Definition: "水"}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if out.Added != 0 || out.Duplicates != 1 {
		t.Errorf("Added/Duplicates = %d/%d, want 0/1", out.Added, out.Duplicates)
	}
	if len(st.uploads) != 0 {
		t.Errorf("added==0 不该写档，uploads=%d", len(st.uploads))
	}
	if out.NewFilename != testOldFilename || out.OldFilename != testOldFilename || out.Version != "" {
		t.Errorf("不写档分支 NewFilename/OldFilename/Version = %q/%q/%q", out.NewFilename, out.OldFilename, out.Version)
	}
}

// 中文释义一字不差 → 点名 dup_definitions；共享义项 → 点名 dup_core_definitions。
func TestAddReportsDefinitionCollisions(t *testing.T) {
	stExact := newTestStore(review.WordGroup{Title: "g", Words: []review.Word{
		{Word: "エアコン", Definition: "空调"},
	}})
	out, err := Add(context.Background(), stExact, testParams(WordToAdd{Word: "クーラー", Definition: "空调"}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(out.DupDefs) != 1 || !strings.Contains(out.DupDefs[0], "エアコン") {
		t.Errorf("DupDefs = %v, want 点名 エアコン", out.DupDefs)
	}
	if len(out.DupCores) != 0 {
		t.Errorf("一字不差时不该再报义项撞车：%v", out.DupCores)
	}

	stCore := newTestStore(review.WordGroup{Title: "g", Words: []review.Word{
		{Word: "とても", Definition: "非常"},
	}})
	out, err = Add(context.Background(), stCore, testParams(WordToAdd{Word: "なかなか", Definition: "相当，很，非常"}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(out.DupCores) != 1 || !strings.Contains(out.DupCores[0], "とても") {
		t.Errorf("DupCores = %v, want 点名 とても", out.DupCores)
	}
}

// forceMajor 与「一次 ≥20 词」都触发大版本（major+1, minor=0）。
func TestAddMajorBump(t *testing.T) {
	t.Run("force", func(t *testing.T) {
		st := newTestStore()
		p := testParams(WordToAdd{Word: "あめ(雨)", Definition: "雨"})
		p.ForceMajor = true
		out, err := Add(context.Background(), st, p)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if !out.MajorBump || out.Version != "v2.0" {
			t.Errorf("forceMajor: MajorBump/Version = %v/%q, want true/v2.0", out.MajorBump, out.Version)
		}
		if got := string(st.uploads[0].data); strings.Contains(got, "批量导入触发大版本") {
			t.Errorf("forceMajor 不该带「批量导入触发大版本」说明")
		}
	})
	t.Run("20 words", func(t *testing.T) {
		st := newTestStore()
		items := make([]WordToAdd, 20)
		for i := range items {
			items[i] = WordToAdd{Word: string(rune('あ'+i)) + "語", Definition: "释义"}
		}
		out, err := Add(context.Background(), st, testParams(items...))
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if !out.MajorBump || out.Version != "v2.0" {
			t.Errorf("20词: MajorBump/Version = %v/%q, want true/v2.0", out.MajorBump, out.Version)
		}
		if got := string(st.uploads[0].data); !strings.Contains(got, "批量导入触发大版本") {
			t.Errorf("20词应带「批量导入触发大版本」说明")
		}
	})
}

// EnsureArchive / UploadArchive 的错误要原样透传（后者带「上传新档案失败」包装）。
func TestAddPropagatesStoreErrors(t *testing.T) {
	st := newTestStore()
	st.ensureErr = errors.New("ensure boom")
	if _, err := Add(context.Background(), st, testParams(WordToAdd{Word: "x", Definition: "y"})); err == nil || !strings.Contains(err.Error(), "ensure boom") {
		t.Errorf("EnsureArchive 错误没透传：%v", err)
	}

	st2 := newTestStore()
	st2.uploadErr = errors.New("upload boom")
	_, err := Add(context.Background(), st2, testParams(WordToAdd{Word: "x", Definition: "y"}))
	if err == nil || !strings.Contains(err.Error(), "上传新档案失败") || !strings.Contains(err.Error(), "upload boom") {
		t.Errorf("UploadArchive 错误没按旧口径包装：%v", err)
	}
}

// fakeMetaStore 是 MetaStore 的内存实现，用于断言词性表写回。
type fakeMetaStore struct {
	meta     *WordMeta
	dlErr    error
	upErr    error
	uploads  int
	lastMeta *WordMeta
}

func (f *fakeMetaStore) DownloadWordMeta(_ context.Context) (*WordMeta, error) {
	if f.dlErr != nil {
		return nil, f.dlErr
	}
	if f.meta == nil {
		return &WordMeta{Items: map[string]WordPOS{}}, nil
	}
	return f.meta, nil
}

func (f *fakeMetaStore) UploadWordMeta(_ context.Context, m *WordMeta) error {
	if f.upErr != nil {
		return f.upErr
	}
	f.uploads++
	f.lastMeta = m
	return nil
}

func testParamsMeta(meta MetaStore, items ...WordToAdd) Params {
	p := testParams(items...)
	p.Meta = meta
	return p
}

// 带词性的新词：Add 后词性表要有对应条目，圆级按老师 2026-11-13 的规则自动定。
func TestAddWritesWordMeta(t *testing.T) {
	arc := newTestStore()
	meta := &fakeMetaStore{}
	p := testParamsMeta(meta,
		WordToAdd{Word: "かきます(書きます)", Definition: "写", Pos: "动词", Sub: "一类"},
		WordToAdd{Word: "たかい(高い)", Definition: "高", Pos: "形容词", Sub: "い形"},
		WordToAdd{Word: "ゆっくり", Definition: "慢慢地", Pos: "副词"},
		WordToAdd{Word: "げんかん(玄関)", Definition: "玄关", Pos: "名词"},
	)

	out, err := Add(context.Background(), arc, p)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if out.Added != 4 || len(out.AddedWords) != 4 {
		t.Fatalf("Added/AddedWords = %d/%d, want 4/4", out.Added, len(out.AddedWords))
	}
	if meta.uploads != 1 {
		t.Fatalf("词性表应写一次，实际 %d 次", meta.uploads)
	}
	m := meta.lastMeta
	if m == nil {
		t.Fatal("lastMeta 为 nil")
	}
	cases := map[string]struct{ pos, sub, circle string }{
		"かきます(書きます)": {"动词", "一类", "三圆"},
		"たかい(高い)":    {"形容词", "い形", "二圆"},
		"ゆっくり":       {"副词", "", "二圆"},
		"げんかん(玄関)":   {"名词", "", ""},
	}
	for w, want := range cases {
		got, ok := m.Items[w]
		if !ok {
			t.Errorf("词性表缺 %q", w)
			continue
		}
		if got.Pos != want.pos || got.Sub != want.sub || got.Circle != want.circle {
			t.Errorf("%q = %+v, want pos=%q sub=%q circle=%q", w, got, want.pos, want.sub, want.circle)
		}
	}
	if m.Updated != "2026-11-04" {
		t.Errorf("Updated = %q, want 2026-11-04", m.Updated)
	}
}

// 没带词性的词：不下载不写词性表。
func TestAddNoPosSkipsWordMeta(t *testing.T) {
	arc := newTestStore()
	meta := &fakeMetaStore{}
	if _, err := Add(context.Background(), arc, testParamsMeta(meta, WordToAdd{Word: "あめ(雨)", Definition: "雨"})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if meta.uploads != 0 {
		t.Errorf("没带词性不该写词性表，写了 %d 次", meta.uploads)
	}
}

// Meta 为 nil（CLI 批量导入）：即便带了 pos 也不写，行为跟以前一样。
func TestAddMetaNilSkipsWordMeta(t *testing.T) {
	arc := newTestStore()
	p := testParams(WordToAdd{Word: "あめ(雨)", Definition: "雨", Pos: "名词"}) // Meta 留 nil
	if _, err := Add(context.Background(), arc, p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 没有 MetaStore 可断言 —— 只要不 panic、不出错，就说明 nil 分支走对了。
}

// 词性表已有别的词，写新词不能冲掉旧条目（读-改-写整份覆盖）。
func TestAddWordMetaPreservesExisting(t *testing.T) {
	arc := newTestStore()
	meta := &fakeMetaStore{meta: &WordMeta{Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Circle: "三圆", Note: "空间上从某处穿过"},
	}}}
	p := testParamsMeta(meta, WordToAdd{Word: "かきます(書きます)", Definition: "写", Pos: "动词", Sub: "一类"})

	if _, err := Add(context.Background(), arc, p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	old, ok := meta.lastMeta.Items["とおります(通ります)"]
	if !ok || old.Note != "空间上从某处穿过" || old.Circle != "三圆" {
		t.Errorf("已有词条被冲掉了：%+v", old)
	}
}
