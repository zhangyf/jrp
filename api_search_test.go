package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhangyf/objstore"
)

// memDocStore 在内存对象存储上加一个可调的 DocSearcher，用来测 /api/search
// 的知识点两路（MetaInsight 命中 / 报错兜底）不联网、不碰真 COS。
type memDocStore struct {
	*memObjStore
	hits []objstore.DocHit
	err  error
	// 记录最后一次收到的查询，验证 DatasetName/阈值/Limit/SearchText 透传。
	lastQ objstore.DocSearchQuery
}

func (m *memDocStore) DocSearch(ctx context.Context, q objstore.DocSearchQuery) ([]objstore.DocHit, error) {
	m.lastQ = q
	if m.err != nil {
		return nil, m.err
	}
	return m.hits, nil
}

// 编译期钉住：*memDocStore 必须同时满足 Store 与 DocSearcher。
var _ objstore.Store = (*memDocStore)(nil)
var _ objstore.DocSearcher = (*memDocStore)(nil)

func newSearchServer(store objstore.Store) (*server, *Storage) {
	st := &Storage{lang: "ja", store: store, dryRun: true}
	return &server{storage: st, lang: "ja", dryRun: true}, st
}

func decodeSearch(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v / body=%s", err, w.Body.String())
	}
	return m
}

func TestHandleSearchMethodAndEmptyQuery(t *testing.T) {
	srv, st := newSearchServer(newMemObjStore())

	// 只收 GET
	w := httptest.NewRecorder()
	srv.handleSearch(w, httptest.NewRequest(http.MethodPost, "/api/search?q=x", nil), st)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应 405, got %d", w.Code)
	}

	// 空 q：回空结果，不报错、不调任何一路
	w = httptest.NewRecorder()
	srv.handleSearch(w, httptest.NewRequest(http.MethodGet, "/api/search?q=", nil), st)
	if w.Code != http.StatusOK {
		t.Fatalf("空 q 应 200, got %d", w.Code)
	}
	m := decodeSearch(t, w)
	if m["success"] != true || m["source"] != "none" {
		t.Errorf("空 q 响应不对: %v", m)
	}
}

func TestHandleSearchMetaInsightPath(t *testing.T) {
	ds := &memDocStore{memObjStore: newMemObjStore(), hits: []objstore.DocHit{
		{
			URI:   "cos://b/" + "language-review/ja/knowledge/标准日本语初级上册_第16课知识点.md",
			Text:  "# 标准日本语初级上册 第16课 知识点\n## 〜ています 的两种用法\n| 用法 | 说明 |\n|---|---|\n| 结果状态 | 动作结束后的状态 |",
			Score: 94,
		},
		{ // 汇编类：解不出课号，只展示不跳页
			URI:   "cos://b/language-review/ja/knowledge/造句易错语法速查（第12-13课）.md",
			Text:  "ている 和 ています 的区别",
			Score: 69,
		},
	}}
	srv, st := newSearchServer(ds)
	t.Setenv("JRP_SEARCH_DATASET", "test-dataset")

	w := httptest.NewRecorder()
	srv.handleSearch(w, httptest.NewRequest(http.MethodGet, "/api/search?q=%E3%81%A6%E3%81%84%E3%82%8B", nil), st) // ている
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d: %s", w.Code, w.Body.String())
	}
	m := decodeSearch(t, w)
	if m["source"] != "metainsight" {
		t.Errorf("应走 MetaInsight, got source=%v", m["source"])
	}
	if m["q"] != "ている" {
		t.Errorf("q 应是 ている, got %v", m["q"])
	}
	docs, _ := m["docs"].([]interface{})
	if len(docs) != 2 {
		t.Fatalf("应有 2 条知识点结果, got %d: %v", len(docs), m["docs"])
	}
	d0 := docs[0].(map[string]interface{})
	// 高分第16课排最前，能跳页、标题正确、片段去掉了重复标题和表格分隔行。
	if d0["lesson"] != float64(16) || d0["jump"] != true || d0["title"] != "第16课" {
		t.Errorf("第16课结果不对: %v", d0)
	}
	txt, _ := d0["text"].(string)
	for _, banned := range []string{"标准日本语初级上册", "|---"} {
		if containsStr(txt, banned) {
			t.Errorf("片段仍含 %q: %q", banned, txt)
		}
	}
	if !containsStr(txt, "〜ています") {
		t.Errorf("片段该保留正文: %q", txt)
	}
	d1 := docs[1].(map[string]interface{})
	if d1["jump"] != false || d1["lesson"] != float64(-1) {
		t.Errorf("汇编类应只展示不跳页: %v", d1)
	}

	// 参数透传：数据集名/阈值 50/Limit/查询文本。
	if ds.lastQ.DatasetName != "test-dataset" {
		t.Errorf("DatasetName 没透传: %q", ds.lastQ.DatasetName)
	}
	if ds.lastQ.SearchText != "ている" {
		t.Errorf("SearchText 没透传: %q", ds.lastQ.SearchText)
	}
	if ds.lastQ.MatchThreshold != defaultSearchThreshold {
		t.Errorf("阈值应默认 %d, got %d", defaultSearchThreshold, ds.lastQ.MatchThreshold)
	}
}

func TestHandleSearchFallbackOnError(t *testing.T) {
	ds := &memDocStore{memObjStore: newMemObjStore(), err: errors.New("meta insight down")}
	// 兜底读 knowledge md：放一份能被字面命中的文档。
	key := ds.sharedMemKey("/knowledge/标准日本语初级上册_第11课知识点.md")
	ds.PutObject(context.Background(), key,
		[]byte("# 标准日本语初级上册 第11课 知识点\n## 句型\n[名1]は[名2]が 好きです\n"))

	srv, st := newSearchServer(ds)
	w := httptest.NewRecorder()
	srv.handleSearch(w, httptest.NewRequest(http.MethodGet, "/api/search?q=%E5%A5%BD%E3%81%8D", nil), st) // 好き（汉字写法，与文档字面一致）
	m := decodeSearch(t, w)
	if m["source"] != "literal" {
		t.Errorf("MetaInsight 报错应落字面兜底, got source=%v", m["source"])
	}
	docs, _ := m["docs"].([]interface{})
	if len(docs) != 1 {
		t.Fatalf("兜底应命中 1 条, got %d: %v", len(docs), m["docs"])
	}
	d0 := docs[0].(map[string]interface{})
	if d0["lesson"] != float64(11) || d0["jump"] != true {
		t.Errorf("兜底结果应解出第11课: %v", d0)
	}
}

func TestHandleSearchNoDocSearcher(t *testing.T) {
	// store 不实现 DocSearcher（S3 / 旧配置）→ 直接走字面兜底，不 panic。
	mem := newMemObjStore()
	st := &Storage{lang: "ja", store: mem, dryRun: true}
	srv := &server{storage: st, lang: "ja", dryRun: true}
	key := st.sharedBase() + "/knowledge/标准日本语初级上册_第2课知识点.md"
	mem.PutObject(context.Background(), key, []byte("## これ\nこれは ほん です\n"))

	w := httptest.NewRecorder()
	srv.handleSearch(w, httptest.NewRequest(http.MethodGet, "/api/search?q=%E3%81%BB%E3%82%93", nil), st) // ほん
	m := decodeSearch(t, w)
	if m["source"] != "literal" {
		t.Errorf("无 DocSearcher 应落兜底, got %v", m["source"])
	}
	docs, _ := m["docs"].([]interface{})
	if len(docs) != 1 || docs[0].(map[string]interface{})["lesson"] != float64(2) {
		t.Errorf("兜底没命中第2课: %v", m["docs"])
	}
}

func TestHandleSearchNilStoreNoPanic(t *testing.T) {
	// 没配 COS 时 store 为 nil：两路都该安静返回空，绝不 panic / 500。
	st := &Storage{lang: "ja", dryRun: true}
	srv := &server{storage: st, lang: "ja", dryRun: true}
	w := httptest.NewRecorder()
	srv.handleSearch(w, httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil), st)
	if w.Code != http.StatusOK {
		t.Fatalf("nil store 应 200, got %d", w.Code)
	}
	m := decodeSearch(t, w)
	if m["success"] != true {
		t.Errorf("nil store 应 success: %v", m)
	}
}

// 权限口径：/api/search 绝不能进 trial 白名单（体验版凭证公开，搜索会烧钱）。
func TestSearchNotInTrialWhitelist(t *testing.T) {
	if trialAllowed("/api/search") {
		t.Fatal("/api/search 不该对体验版放行")
	}
}

func (m *memDocStore) sharedMemKey(suffix string) string {
	return LangConfigs["ja"].COSPrefix + suffix
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
