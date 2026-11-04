// api_data.go —— 词条 / 词形变形 / 知识库 / 录入类接口
//
// 腿一 R2：把原本平铺在根目录的 4 个 api_*.go / storage*.go 物理合并到本文件。
// 函数体逐字保留，仅对 import 取并集去重；HTTP 响应、存档、Excel 字节均不变。
// 每个分区以「原文件名」标注，便于定位；查找 handler 先看下面的来源清单。
//
// 来源与顶层声明：
// api_conjugation.go: ConjTask, ConjItem, handleConjugation
// api_knowledge.go: KSpan, KItem, KBlock, handleKnowledge, knowledgeFullAccess
// api_words.go: LexiconItem, handleLexicon
// api_words.go: handleAddWords
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"math/rand"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zhangyf/jrp/internal/conjugation"
	"github.com/zhangyf/jrp/internal/knowledge"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/words"
)

// ==========================================================================
// 原 api_conjugation.go
// ==========================================================================

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
		kana, kanji, _ := words.SplitLexiconWord(w.Word)
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

// ==========================================================================
// 原 api_knowledge.go
// ==========================================================================

// ---------- 知识点板块（小程序「新标日III知识点」，2026-11-03 老师要求） ----------
//
// 需求原话：小程序里新增一个板块，把新标日各课的知识点总结按课时展示出来；
// **非登录状态下仅展示一部分，其余被模糊滤镜挡住**。
//
// 三条设计决定，动这个文件之前先读：
//
//  1. **markdown 在服务端解析，不在小程序里解析**。
//     小程序没有 markdown 渲染能力（rich-text 只吃 HTML 节点，且样式不可控），
//     而且「哪几块模糊」必须按块切 —— 所以这里把 md 拆成结构化块数组发给前端，
//     前端只负责按 type 摆样式。
//
//  2. **这个接口不走 withUser**。
//     匿名和体验版也要能读到（只是读到的内容被截断），而 withUser 在鉴权打开后
//     对匿名直接 401。所以这里自己认会话：认出来就给全量，认不出来就给受限版。
//     ⛔ 只读 sharedBase（教材内容，不属于任何人的个人数据），一行都不碰 uid 目录。
//
//  3. **模糊是展示层软限制，不是加密**。
//     后端照样把全文发下去，只多给一个 visible 数字让前端把后面的块盖住。
//     想拿全文的人抓个包就有 —— 这是有意的取舍：知识点是教材整理而非隐私数据，
//     挡一下是为了引导登录，不是防攻击。真要防就在服务端按块截断（那前端就没有
//     「模糊」的视觉了，跟老师要的效果不符）。

type KSpan = knowledge.Span
type KItem = knowledge.Item
type KBlock = knowledge.Block

// lessonDocRe 从文件名里认课号：「标准日本语初级上册_第21课知识点.md」→ 21。
//
// 只认「第N课知识点」这一种命名 —— knowledge 目录里还混着
// 「课本原文汇编_第N单元（基本课文）.md」和「造句易错语法速查（第12-13课）.md」，
// 它们不是按课的知识点总结，不能进这个板块。
var lessonDocRe = regexp.MustCompile(`第(\d+)课知识点`)

// handleKnowledge 知识点板块的唯一入口。
//
//	GET /api/knowledge            → 课号列表（按课号数字排序，不是字符串排序）
//	GET /api/knowledge?lesson=21  → 第 21 课全文（分块）
func (s *server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	objs, err := s.storage.ListKnowledge(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "知识点目录读不了：" + err.Error(),
		})
		return
	}

	type lesson struct {
		num  int
		name string
		size int64
	}
	var ls []lesson
	for _, o := range objs {
		m := lessonDocRe.FindStringSubmatch(o.Key)
		if len(m) != 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ls = append(ls, lesson{num: n, name: o.Key[strings.LastIndex(o.Key, "/")+1:], size: o.Size})
	}
	// 课号必须按数字排：字符串排序会把第 10 课排在第 1 课前面。
	sort.Slice(ls, func(i, j int) bool { return ls[i].num < ls[j].num })

	if len(ls) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "lessons": []interface{}{},
		})
		return
	}

	full := s.knowledgeFullAccess(r, ctx)
	want := strings.TrimSpace(r.URL.Query().Get("lesson"))

	// 没传 lesson：只回列表（体验版也要能看到有哪些课，才有「登录看全部」的动力）。
	if want == "" {
		out := make([]map[string]interface{}, 0, len(ls))
		for _, l := range ls {
			out = append(out, map[string]interface{}{
				"lesson": l.num,
				"title":  "第" + strconv.Itoa(l.num) + "课",
				"size":   l.size,
			})
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"locked":  !full,
			"lessons": out,
		})
		return
	}

	num, err := strconv.Atoi(want)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "lesson 应该是课号数字",
		})
		return
	}
	var hit *lesson
	for i := range ls {
		if ls[i].num == num {
			hit = &ls[i]
			break
		}
	}
	if hit == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false, "error": "没有第 " + want + " 课的知识点",
		})
		return
	}

	data, err := s.storage.DownloadKnowledge(ctx, hit.name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "知识点读不了：" + err.Error(),
		})
		return
	}
	blocks := knowledge.ParseMarkdown(string(data))

	visible := len(blocks)
	if !full {
		visible = knowledge.VisibleBlockCount(blocks)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"lesson":  num,
		"title":   knowledge.Title(blocks, num),
		"locked":  !full,
		"visible": visible,
		"blocks":  blocks,
	})
}

// knowledgeFullAccess 这次请求能不能看全文。
//
// 三种情况给全量：正常登录（含 7 天体验期内的 pending）、被冻结/过期 pending 不算、
// 以及 CLI 的共享 token。体验版（trial）和匿名一律受限。
func (s *server) knowledgeFullAccess(r *http.Request, ctx context.Context) bool {
	if s.token != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1 {
		return true
	}
	sess, logged := s.sessionUser(r)
	if !logged {
		return false
	}
	// ⚠️ uid == "" ⇔ owner（零迁移约定，见 auth.go / api_auth.go 的 is_owner）。
	// 2026-11-03 踩过：这里原来写成 `sess.UID == "" || sess.UID == trialUID`，
	// 把 owner 当成匿名挡在门外 —— 老师在小程序里「每日复习已登录、知识点还是未登录」
	// 就是这个。匿名压根没有会话（sessionUser 已经先返回 false 了），
	// 能走到这里的 uid=="" 只可能是 owner。
	if sess.UID == trialUID {
		return false // 体验版凭证是公开的，等于匿名
	}
	if sess.UID == "" {
		return true // owner：全量
	}
	u, ok := findUserByUID(cachedUsers(ctx, s.storage), sess.UID)
	if !ok {
		return true // 用户表里查不到（老会话）就按会话角色给
	}
	if u.Role == roleDisabled {
		return false
	}
	if u.Role == rolePending && pendingExpired(u) {
		return false
	}
	return true
}

// ==========================================================================
// 原 api_words.go
// ==========================================================================

// LexiconItem 是词汇总表的一行，定义已迁 internal/words（P2）。
// 根包只留别名 —— /api/lexicon、CLI dump-lexicon、体验版假数据、
// 活用练习、查释义都继续用这个名字，JSON 序列化字节不变。
type LexiconItem = words.LexiconItem

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

	items, posSummary, groups := words.BuildLexicon(arc, meta)

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

// ==========================================================================
// 原 api_words.go
// ==========================================================================

// 录入新词。
//
//	POST /api/words  { group, items:[{word, def}] }
//	→ { success, added, duplicates, version, total_words, dup_definitions… }
//
// 跟 CLI 的 add-words 共用 words_add.go 里那一份实现：重复词跳过、
// 中文释义撞车点名、版本号、changelog 全都在那边，不会两边各写一套漂掉。
//
// 权限：跟其他 withUser 接口一样（已登录且不是 pending/disabled）。
// 儿子也能录 —— 录进的是各自的档案，不会串到老师那份。
func (s *server) handleAddWords(w http.ResponseWriter, r *http.Request, st *Storage) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"success": false, "error": "POST only",
		})
		return
	}

	var body struct {
		Group string `json:"group"`
		Items []struct {
			Word string `json:"word"`
			Def  string `json:"def"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "请求格式不对：" + err.Error(),
		})
		return
	}
	if len(body.Items) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "没有要录的词",
		})
		return
	}
	if len(body.Items) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "一次最多录 200 个词",
		})
		return
	}

	items := make([]WordToAdd, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, WordToAdd{Word: it.Word, Definition: it.Def})
	}

	res, err := addWordsToArchive(s.ctx(), st, body.Group, items, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":              true,
		"added":                res.Added,
		"duplicates":           res.Duplicates,
		"version":              res.Version,
		"new_filename":         res.NewFilename,
		"total_words":          res.TotalWords,
		"dup_definitions":      res.DupDefs,
		"dup_core_definitions": res.DupCores,
		"dry_run":              s.dryRun,
	})
}
