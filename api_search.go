// api_search.go —— 全局搜索：四页练习右上角的浮动搜索框后端。
//
// 方案 C（2026-11-05 老师拍板）：一次请求内并行两路，口径刻意分开 ——
//   - 词汇：结构化词条，要精确命中 → 服务端读档案 + 归一化字面匹配。
//     ⚠️ 不用语义检索：实测「くろい」走 MetaInsight 全是不相关低分噪声，
//     且档案是 per-user（personalBase），绑共享数据集会跨用户捞到别人档案。
//   - 知识点：自然语言描述（「は 和 が 的区别」）→ COS 智能检索 MetaInsight
//     托管语义检索（sharedBase/knowledge/，官方 DocSearch 模板）。
//
// 安全：挂 withUser ⇒ 匿名 401、体验版 403（trialAllowed 不含本路由，
// 不需要改白名单）。绝不把知识点全文下发到浏览器建索引，只回命中的 N 条片段。
// 三条硬指标（既有接口 JSON / COS 存档 / Excel 渲染字节）不变，本路由是纯新增。
//
// 配置：内置默认 + 可选环境变量，**不铺 COS 三层外置**（避免重蹈 prompt
// 外置那次动作过快）。以后要可调再加 plan_config 式外置。
package main

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhangyf/jrp/internal/search"
	"github.com/zhangyf/jrp/internal/words"
	"github.com/zhangyf/objstore"
)

const (
	// 老师 COS 账号里已建好、只绑 knowledge/ 前缀的数据集（2026-11-05 实测）。
	defaultSearchDataset = "openclaw-backup-tx-doc-u85qww"
	// 官方推荐 80，但实测中文描述类查询 80 会漏（「は 和 が 的区别」80→0 条）；
	// 50 保住召回又挡光 ≤22 的噪声。见 2026-11-05 工作日志的阈值对比表。
	defaultSearchThreshold = 50
	defaultDocLimit        = 8
	defaultWordLimit       = 8
	// 搜索是输入即搜，知识点路是网络调用，必须设超时，超时就落兜底，
	// 不能让搜索框卡死。词汇路是 COS 读，通常更快，不单独限时。
	docSearchTimeout = 5 * time.Second
	// 结果片段展示字数（按 rune），别把整个 100~600 字 chunk 塞进浮层。
	docSnippetRunes = 160
)

// searchEnvInt 读整数环境变量并钳制到 [min,max]；没设/非法就用默认值。
// 环境变量给的是运维旋钮，非法值不该打挂 API。
func searchEnvInt(key string, def, min, max int) int {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func searchDataset() string {
	if v := strings.TrimSpace(os.Getenv("JRP_SEARCH_DATASET")); v != "" {
		return v
	}
	return defaultSearchDataset
}

// searchWordHit 词汇结果的一行（精简字段，前端浮层只用这些）。
type searchWordHit struct {
	Number   int    `json:"number"`
	Word     string `json:"word"`
	Kana     string `json:"kana"`
	Kanji    string `json:"kanji"`
	Note     string `json:"note"`
	WordNote string `json:"word_note"`
	Def      string `json:"def"`
	Pos      string `json:"pos"`
	Sub      string `json:"sub"`
	Group    string `json:"group"`
	Score    int    `json:"score"`
}

// searchDocHit 知识点结果的一条。
// Lesson=-1 且 Jump=false 表示 URI 解不出课号（汇编/速查类），只展示不跳页。
type searchDocHit struct {
	Lesson int    `json:"lesson"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Score  int    `json:"score"`
	Jump   bool   `json:"jump"`
}

// handleSearch GET /api/search?q=…
//
//	→ { success, q, words:[...], docs:[...], source, dry_run }
//
// source 标注知识点路实际走的是哪条：metainsight / literal（兜底）/ none（不可用）。
func (s *server) handleSearch(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET only",
		})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	q = search.TruncateRunes(q, 60) // MetaInsight SearchText 上限 60 字符
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "q": "", "words": []searchWordHit{},
			"docs": []searchDocHit{}, "source": "none", "dry_run": s.dryRun,
		})
		return
	}

	ctx := s.ctx()
	var res struct {
		words  []searchWordHit
		docs   []searchDocHit
		source string
	}
	// 两路独立、各写各的字段，无共享写。知识点路带超时，超时/报错内部落兜底，
	// 所以任何一路出问题都不影响另一路出结果（老师要的鲁棒性）。
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		res.words = s.searchWords(ctx, st, q)
	}()
	go func() {
		defer wg.Done()
		res.docs, res.source = s.searchDocs(ctx, st, q)
	}()
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "q": q,
		"words": res.words, "docs": res.docs, "source": res.source,
		"dry_run": s.dryRun,
	})
}

// searchWords 词汇路：读档案 + 词性表 → BuildLexicon → 归一化字面匹配。
// 取数失败一律回空列表（不 500），搜索是尽力而为，不该拖垮整页。
func (s *server) searchWords(ctx context.Context, st *Storage, q string) []searchWordHit {
	out := []searchWordHit{}
	if st.store == nil {
		return out
	}
	arc, err := st.LoadArchiveForRead(ctx, s.lang)
	if err != nil {
		return out
	}
	meta, err := st.DownloadWordMeta(ctx)
	if err != nil || meta == nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}
	if meta.Items == nil {
		meta.Items = map[string]WordPOS{}
	}
	items, _, _ := words.BuildLexicon(arc, meta)

	limit := searchEnvInt("JRP_SEARCH_WORD_LIMIT", defaultWordLimit, 1, 30)
	type scored struct {
		it LexiconItem
		sc int
	}
	var hits []scored
	for _, it := range items {
		fields := []string{it.Word, it.Kana, it.Kanji, it.Def, it.Pos, it.Sub, it.Note, it.WordNote, it.Group}
		if sc := search.Score(q, fields); sc >= 0 {
			hits = append(hits, scored{it: it, sc: sc})
		}
	}
	// 前缀命中优先（Score 已编码档位），同分按词表原序（number）稳定。
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].sc != hits[j].sc {
			return hits[i].sc > hits[j].sc
		}
		return hits[i].it.Number < hits[j].it.Number
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	for _, h := range hits {
		out = append(out, searchWordHit{
			Number: h.it.Number, Word: h.it.Word, Kana: h.it.Kana, Kanji: h.it.Kanji,
			Note: h.it.Note, WordNote: h.it.WordNote, Def: h.it.Def,
			Pos: h.it.Pos, Sub: h.it.Sub, Group: h.it.Group, Score: h.sc,
		})
	}
	return out
}

// searchDocs 知识点路：先试 MetaInsight 语义检索，不可用/报错/超时就落字面兜底。
func (s *server) searchDocs(ctx context.Context, st *Storage, q string) ([]searchDocHit, string) {
	if st.store == nil {
		return []searchDocHit{}, "none"
	}
	ds, ok := st.store.(objstore.DocSearcher)
	if !ok {
		return s.searchDocsLiteral(ctx, st, q), "literal"
	}
	dctx, cancel := context.WithTimeout(ctx, docSearchTimeout)
	defer cancel()
	hits, err := ds.DocSearch(dctx, objstore.DocSearchQuery{
		DatasetName:    searchDataset(),
		SearchText:     q,
		Limit:          searchEnvInt("JRP_SEARCH_LIMIT", defaultDocLimit, 1, 100),
		MatchThreshold: searchEnvInt("JRP_SEARCH_THRESHOLD", defaultSearchThreshold, 0, 100),
	})
	if err != nil {
		// MetaInsight 挂了/超时/权限问题：降级到服务端字面匹配，不留空结果。
		return s.searchDocsLiteral(ctx, st, q), "literal"
	}
	return s.mapDocHits(hits), "metainsight"
}

// mapDocHits 把 objstore.DocHit 映射成前端结果：解课号、去重复标题+截取、
// 按 URI 去重（同一课多个 chunk 只留最高分那条）、按分数降序。
func (s *server) mapDocHits(hits []objstore.DocHit) []searchDocHit {
	out := []searchDocHit{}
	// 先按分数降序，再去重 ⇒ 同 URI 保留的是最相关 chunk。
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	seen := map[string]bool{}
	limit := searchEnvInt("JRP_SEARCH_LIMIT", defaultDocLimit, 1, 100)
	for _, h := range hits {
		if seen[h.URI] {
			continue
		}
		seen[h.URI] = true
		lesson, jump := search.LessonFromURI(h.URI)
		lv := -1
		if jump {
			lv = lesson
		}
		out = append(out, searchDocHit{
			Lesson: lv,
			Title:  search.TitleFromURI(h.URI),
			Text:   search.CleanDocText(h.Text, docSnippetRunes),
			Score:  h.Score,
			Jump:   jump,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// searchDocsLiteral 兜底：MetaInsight 不可用/报错/超时，服务端读 knowledge md
// 做字面匹配（MatchText 只回命中行 ±1 行，不下发全库）。仅遍历到命中 limit 条。
func (s *server) searchDocsLiteral(ctx context.Context, st *Storage, q string) []searchDocHit {
	out := []searchDocHit{}
	objs, err := st.ListKnowledge(ctx)
	if err != nil {
		return out
	}
	limit := searchEnvInt("JRP_SEARCH_LIMIT", defaultDocLimit, 1, 100)
	for _, o := range objs {
		// 兜底也要能被超时打断，别因为 35 个 md 拖住整个请求。
		if err := ctx.Err(); err != nil {
			break
		}
		name := o.Key
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		data, err := st.DownloadKnowledge(ctx, name)
		if err != nil {
			continue
		}
		snip, ok := search.MatchText(string(data), q)
		if !ok {
			continue
		}
		lesson, jump := search.LessonFromURI(o.Key)
		lv := -1
		if jump {
			lv = lesson
		}
		out = append(out, searchDocHit{
			Lesson: lv,
			Title:  search.TitleFromURI(o.Key),
			Text:   search.CleanDocText(snip, docSnippetRunes),
			Score:  0, // 字面匹配没有语义分
			Jump:   jump,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}
