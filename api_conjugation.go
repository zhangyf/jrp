package main

// 活用专项练习（2026-09-28 老师新增板块）。
//
//	GET /api/conjugation?type=verb&count=10      → 动词卡：原词（ます形）→ 原形 + て形 + た形
//	GET /api/conjugation?type=verb_nai&count=10  → 动词ない形卡：原词 → 原形 + ない形
//	GET /api/conjugation?type=adj&count=10       → 形容词卡：原词 → 过去式 / 否定形 / 过去否定形（带です）
//
// 设计要点：
//   - 词源 = 档案全词表 × word_meta 词性，只收 pos 能对上号的词；
//     算不出活用的词（词条形态怪）直接跳过，绝不硬算。
//   - 服务端算好标准答案（可多写法），判分放在前端用 grade.js 的 either 档：
//     写假名或汉字都算对，多写法用「/」分隔交给 parseAnswerForms 拆。
//   - 纯自测：不回写档案、不动 ReviewCount —— 老师明确要求「只练不计档」。
//   - 纯规则位于 internal/conjugation/rules.go；新增题型时在本文件组装 tasks，前端自动跟着渲染。

import (
	"math/rand"
	"net/http"
	"strconv"

	"github.com/zhangyf/jrp/internal/conjugation"
	"github.com/zhangyf/jrp/internal/review"
)

// ConjTask 是一张卡上的一道填空：label（原形/て形/过去式…）+ 可接受写法列表。
type ConjTask struct {
	Label   string   `json:"label"`
	Answers []string `json:"answers"` // 多写法；前端 join("/") 喂给 gradeAnswer
}

// ConjItem 是一张活用卡。
type ConjItem struct {
	Word       string     `json:"word"` // 档案原词形「かな(漢字)」
	Definition string     `json:"definition"`
	Pos        string     `json:"pos"`
	Sub        string     `json:"sub"`
	Tasks      []ConjTask `json:"tasks"`
}

// handleConjugation —— 活用专项出题。只读，不写档案。
func (s *server) handleConjugation(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "GET only",
		})
		return
	}
	typ := r.URL.Query().Get("type")
	if typ != "verb" && typ != "verb_nai" && typ != "adj" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "type 必须是 verb / verb_nai / adj",
		})
		return
	}
	count := 10
	if n, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil {
		if n < 1 {
			n = 1
		}
		if n > 30 {
			n = 30
		}
		count = n
	}

	// 体验版：固定 2 题，不读档案（真路径会下载最新档案再洗牌）。
	if st.uid == trialUID {
		writeTrialConjugation(w, typ)
		return
	}

	// LoadArchiveForRead：新账号没档案 → 空池子，下面正常回「共 0 题」。
	// 之前这里 500「no archives found」，审核直接判白屏（2026-11-02）。
	arc, err := st.LoadArchiveForRead(bgctx(), s.lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	meta, err := st.DownloadWordMeta(bgctx())
	if err != nil || meta.Items == nil {
		meta = &WordMeta{Items: map[string]WordPOS{}}
	}

	pool := make([]ConjItem, 0, 128)
	seenKana := map[string]bool{}
	for _, w := range review.AllWords(arc.Groups) {
		pos, has := meta.Items[w.Word]
		if !has {
			continue
		}
		kana, kanji, _ := splitLexiconWord(w.Word)
		// 档案里有「あおい」和「あおい(青い)」这类同词异形（历史导入留下的），
		// 出题按假名去重，免得同一批里出现两张一样的卡。
		if seenKana[kana] {
			continue
		}

		switch typ {
		case "verb", "verb_nai":
			cls := conjugation.VerbClass(pos.Pos, pos.Sub)
			if cls == "" || conjugation.SkipWord(w.Word, kana) {
				continue
			}
			base, te, ta, ok := conjugation.Verb(kana, kanji, cls)
			if !ok {
				continue
			}
			if typ == "verb" {
				pool = append(pool, ConjItem{
					Word: w.Word, Definition: w.Definition, Pos: pos.Pos, Sub: pos.Sub,
					Tasks: []ConjTask{
						{Label: "原形", Answers: []string{base}},
						{Label: "て形", Answers: []string{te}},
						// た形（2026-11-03 老师要求）：跟て形同一张卡，
						// 音便同源，放一起对比着练正好。
						{Label: "た形", Answers: []string{ta}},
					},
				})
			} else {
				// ない形卡：原形 + ない形（conjugation.VerbNai 内部复用 conjugation.Verb 算原形）
				_, nai, okNai := conjugation.VerbNai(kana, kanji, cls)
				if !okNai {
					continue
				}
				pool = append(pool, ConjItem{
					Word: w.Word, Definition: w.Definition, Pos: pos.Pos, Sub: pos.Sub,
					Tasks: []ConjTask{
						{Label: "原形", Answers: []string{base}},
						{Label: "ない形", Answers: []string{nai}},
					},
				})
			}
			seenKana[kana] = true

		case "adj":
			cls := conjugation.AdjClass(pos.Pos, pos.Sub)
			if cls == "" {
				continue
			}
			var past, neg, pastneg string
			var ok bool
			if cls == "i" {
				past, neg, pastneg, ok = conjugation.AdjI(kana, kanji)
			} else {
				past, neg, pastneg, ok = conjugation.AdjNa(kana, kanji)
			}
			if !ok {
				continue
			}
			pool = append(pool, ConjItem{
				Word: w.Word, Definition: w.Definition, Pos: pos.Pos, Sub: pos.Sub,
				Tasks: []ConjTask{
					{Label: "过去式", Answers: []string{past}},
					{Label: "否定形", Answers: []string{neg}},
					{Label: "过去否定形", Answers: []string{pastneg}},
				},
			})
			seenKana[kana] = true
		}
	}

	if len(pool) == 0 {
		// 空池子是**正常态**（新账号没词 / 没标词性），不是错误：
		// 回 success=true + 空 items，页面自己有「共 0 题」的空态。
		// 之前回 success=false，审核看到的就是「页面报错」。
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "type": typ, "count": 0, "pool": 0, "items": []ConjItem{},
			"note":    "档案里没有可用于活用练习的词条（需要 word_meta 里标注了词性的动词/形容词）",
			"dry_run": s.dryRun,
		})
		return
	}

	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if count > len(pool) {
		count = len(pool)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "type": typ, "count": count,
		"pool": len(pool), "items": pool[:count], "dry_run": s.dryRun,
	})
}
