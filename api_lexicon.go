package main

import (
	"net/http"

	"github.com/zhangyf/jrp/internal/lexicon"
)

// LexiconItem 是词汇总表的一行，定义已迁 internal/lexicon（P2）。
// 根包只留别名 —— /api/lexicon、CLI dump-lexicon、体验版假数据、
// 活用练习、查释义都继续用这个名字，JSON 序列化字节不变。
type LexiconItem = lexicon.LexiconItem

// handleLexicon —— 词汇总表。
//
//	GET /api/lexicon → { success, count, items:[...], pos_summary:{...}, groups:[...] }
//
// 词性来自独立的 word_meta.json，不在档案里。两份数据按词形 join，
// 词性表里没有的词 pos 为空字符串（页面显示「—」，不影响其他列）。
func (s *server) handleLexicon(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET only",
		})
		return
	}
	// 体验版：内置 10 行，不读档案、不读 word_meta，一次不碰 COS。
	if st.uid == trialUID {
		writeTrialLexicon(w)
		return
	}
	ctx := s.ctx()

	// LoadArchiveForRead：新账号没档案 → 回 0 行，别 500（2026-11-02 审核被拒）。
	arc, err := st.LoadArchiveForRead(ctx, s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	meta, err := st.DownloadWordMeta(ctx)
	if err != nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}
	if meta.Items == nil {
		meta.Items = map[string]WordPOS{}
	}

	items, posSummary, groups := lexicon.BuildLexicon(arc, meta)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"count":        len(items),
		"items":        items,
		"pos_summary":  posSummary,
		"groups":       groups,
		"meta_note":    meta.Note,
		"meta_updated": meta.Updated,
		"dry_run":      s.dryRun,
	})
}
