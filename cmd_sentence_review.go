package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
)

// runSentenceReviewTodo 只读体检：列出「够格回炉、但还没做变形句」的词，
// 并给每个词推荐几条能套的原型句骨架。
//
// 为什么需要它：回炉句库是**静态**的 —— Go 只负责挑句，不造词（没有分词器，
// 判断不出句子里哪个位置能换什么词），句库由 AI 生成后上传。所以档案里新进
// 「正确率≥80% 且闲置≥30天」状态的词不会自动变成变形句，得定期补。
// 这个命令把「谁还没做」和「能套哪句」一次列全，补一批只要几分钟。
//
//	jrp --lang ja sentence-review-todo
//	jrp --lang ja sentence-review-todo --limit 0           # 列出全部
//	jrp --lang ja sentence-review-todo --pending           # 连"在路上"的一起看
func runSentenceReviewTodo(fs *flag.FlagSet, lang string) {
	limit := fs.Int("limit", 50, "最多列出多少个待补词（0 = 全部）")
	nproto := fs.Int("protos", 3, "每个词推荐几条原型句骨架")
	withPending := fs.Bool("pending", false, "连「在路上」（正确率够但还没闲置满）的词一起列")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	rb, err := storage.DownloadReviewBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
		os.Exit(1)
	}
	covered := map[string]bool{}
	for _, s := range rb.Sentences {
		if s.Word != "" {
			covered[s.Word] = true
		}
	}

	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取句库失败：%v\n", err)
		os.Exit(1)
	}
	byLesson := groupProtosByLesson(bank.Sentences)

	data, _, err := storage.DownloadLatestArchive(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取档案失败：%v\n", err)
		os.Exit(1)
	}
	arc, err := archive.ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 解析档案失败：%v\n", err)
		os.Exit(1)
	}
	meta, _ := storage.DownloadWordMeta(ctx) // 读不到就留空，不致命

	today := time.Now()
	type item struct {
		Word     string         `json:"word"`
		Pos      string         `json:"pos"`
		Def      string         `json:"def"`
		Lesson   string         `json:"lesson"`
		Reviews  int            `json:"reviews"`
		Accuracy float64        `json:"accuracy"`
		IdleDays int            `json:"idle_days"`
		DueIn    int            `json:"days_to_qualify,omitempty"` // 仅"在路上"的词有
		Protos   []protoSuggest `json:"prototypes,omitempty"`
	}
	var qualified, pending []item
	var young, weak, qualifiedTotal, coveredCount int

	seen := map[string]bool{}
	for _, g := range arc.Groups {
		for _, w := range g.Words {
			if w.Word == "" || seen[w.Word] {
				continue
			}
			seen[w.Word] = true
			acc := 0.0
			if w.ReviewCount > 0 {
				acc = float64(w.ReviewCount-w.ErrorCount) / float64(w.ReviewCount)
			}
			idle := wordIdleDays(w, today)
			pos, _, _, _ := lookupWordMeta(meta, w.Word)
			ln := groupLesson(w.Group)
			it := item{
				Word: w.Word, Pos: pos, Def: w.Definition,
				Lesson: lessonLabel(ln), Reviews: w.ReviewCount,
				Accuracy: roundAcc(acc), IdleDays: idle,
			}
			switch {
			case w.ReviewCount < 2:
				young++
			case acc < 0.8:
				weak++
			case idle >= DefaultReviewIdleDays:
				qualifiedTotal++
				if covered[w.Word] {
					coveredCount++
					break
				}
				it.Protos = protoList(pickPrototypes(byLesson, ln, *nproto, pos))
				qualified = append(qualified, it)
			default:
				it.DueIn = DefaultReviewIdleDays - idle
				pending = append(pending, it)
			}
		}
	}

	// 闲置越久越该先补
	sort.SliceStable(qualified, func(i, j int) bool { return qualified[i].IdleDays > qualified[j].IdleDays })
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].DueIn < pending[j].DueIn })

	missing := len(qualified)
	if *limit > 0 && len(qualified) > *limit {
		qualified = qualified[:*limit]
	}

	out := map[string]interface{}{
		"success":  true,
		"command":  "sentence-review-todo",
		"language": lang,
		"note":     "只读体检，未写入任何数据",
		"criteria": map[string]interface{}{
			"min_reviews":   2,
			"min_accuracy":  0.8,
			"min_idle_days": DefaultReviewIdleDays,
		},
		"summary": map[string]interface{}{
			"archive_words": len(seen),
			"qualified":     qualifiedTotal,
			"covered":       coveredCount,
			"missing":       missing,
			"listed":        len(qualified),
			"pending":       len(pending),
			"young_lt2":     young,
			"weak_lt80":     weak,
			"review_bank":   len(rb.Sentences),
		},
		"items": qualified,
	}
	if *withPending {
		out["pending_items"] = pending
		buckets := map[string]int{}
		for _, p := range pending {
			switch {
			case p.DueIn <= 7:
				buckets["7天内"]++
			case p.DueIn <= 14:
				buckets["8-14天"]++
			case p.DueIn <= 21:
				buckets["15-21天"]++
			default:
				buckets["22-29天"]++
			}
		}
		out["pending_due_in"] = buckets
	}
	if missing == 0 {
		out["hint"] = "全部合格词都已经有变形句了，不用补。"
	} else {
		out["hint"] = "按 prototypes 里的原型句换一个词（助词序列别动），造好后用 sentence-bank 之外的回炉库上传入口写回。"
	}
	outputResult(out)
}

// roundAcc 保留两位小数，避免 JSON 里出现 0.9166666666666666 这种。
func roundAcc(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

type protoSuggest struct {
	ID      string `json:"id"`
	Lesson  string `json:"lesson"`
	Source  string `json:"source"`
	Answer  string `json:"answer"`
	Chinese string `json:"chinese"`
}

var groupLessonRe = regexp.MustCompile(`第(\d+)课`)

// groupLesson 从档案分组标题里抽课号（「第1课01 基础词（5/25）」→ 1）。
// 抽不到返回 0，调用方按"课号未知"处理。
func groupLesson(group string) int {
	m := groupLessonRe.FindStringSubmatch(group)
	if len(m) != 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

func lessonLabel(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("第%d课", n)
}

// groupProtosByLesson 把原型句按课号分组，组内按「基本课文优先 + 长句优先」排序
// —— 基本课文是课本里最典型的句型。
//
// ⚠️ 2026-10-20 老师反馈：变形句偏简单（平均 13.8 字，主句库 15.9；1-7 课占 64%）。
// 原来这里按「短句优先」排（短句好改造），结果造出来的全是短句。
// 改成「长句优先」：骨架本身有内容，换进去的那个词才练得到东西。
// 真正决定顺序的是 protoScore（见下），这里只作为同分时的稳定次序。
func groupProtosByLesson(sentences []BankSentence) map[int][]BankSentence {
	out := map[int][]BankSentence{}
	for _, s := range sentences {
		if s.Answer == "" {
			continue
		}
		n := groupLesson(s.Lesson)
		out[n] = append(out[n], s)
	}
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			bi := list[i].Source == "基本课文"
			bj := list[j].Source == "基本课文"
			if bi != bj {
				return bi
			}
			return len([]rune(list[i].Answer)) > len([]rune(list[j].Answer))
		})
	}
	return out
}

// protoScore 给原型句打分：这个词能不能套进去。
//
// 只按课号匹配是不够的——「なごや（名古屋）」在第4课，同课最短的几条是
// 「だれも いません。」这种没名词槽的句子，套不进去。所以按词性看句子里
// 有没有对应的槽位；太短的句子（「はい、そうです。」）一律降权。
func protoScore(s BankSentence, pos string) int {
	a := s.Answer
	n := len([]rune(a))
	score := 0
	// 长度偏好：**中长句优先**。
	// 2026-10-20 之前是「短句优先」（n>22 扣分），造出来的变形句平均只有 13.8 字、
	// 比主句库 15.9 字短一截，老师练着觉得太简单。现在反过来：
	// 太短的句子没内容可练（换进去也没练到什么），14-26 字是最合适的骨架，
	// 超过 26 字改造时容易把助词序列改坏，轻扣。
	switch {
	case n < 10:
		score -= 30 // 没槽位可换，也没内容
	case n >= 14 && n <= 26:
		score += 15 // 够料的句子
	case n > 26:
		score -= 6 // 太长难改造
	}
	switch {
	case strings.Contains(pos, "名词"):
		// 名词槽：看句子里有没有能接名词的助词
		for _, p := range []string{"は ", "が ", "を ", "に ", "へ ", "から ", "まで ", "と ", "や ", "の ", "で "} {
			if strings.Contains(a, p) {
				score += 10
			}
		}
	case strings.Contains(pos, "动词"):
		if strings.HasSuffix(a, "ます。") || strings.HasSuffix(a, "ました。") || strings.HasSuffix(a, "ません。") {
			score += 25
		}
	case strings.Contains(pos, "形容词"):
		if strings.Contains(a, "いです") || strings.Contains(a, "い ") || strings.Contains(a, "な ") || strings.Contains(a, "かった") {
			score += 25
		}
	}
	if s.Source == "基本课文" {
		score += 5
	}
	return score
}

// pickPrototypes 给某个词挑 n 条原型句骨架：先同课，再按课号距离往外扩；
// 同课内按「这个词能不能套进去」（protoScore）排序。
// 同课的句子最可能就是这个词在课文里出现的句型。
func pickPrototypes(byLesson map[int][]BankSentence, lesson int, n int, pos string) []BankSentence {
	if n <= 0 {
		return nil
	}
	// 课号未知（「追加词汇（8/7）」这类补充分组，62 个分组里有 6 个）：
	// 没法靠课号定位，就在全库里按「这个词能不能套进去」打分挑。
	if lesson <= 0 {
		var all []BankSentence
		for _, list := range byLesson {
			all = append(all, list...)
		}
		sort.SliceStable(all, func(i, j int) bool {
			return protoScore(all[i], pos) > protoScore(all[j], pos)
		})
		if len(all) > n {
			all = all[:n]
		}
		return all
	}
	var out []BankSentence
	seen := map[string]bool{}
	for d := 0; d <= 30 && len(out) < n; d++ {
		var ls []int
		if d == 0 {
			ls = []int{lesson}
		} else {
			// ⚠️ 先往高课扩、再往低课扩（2026-10-20 改）。
			// 原来先 -d 再 +d，结果跨课补的句子都来自更简单的老课。
			// 高课的句型更复杂，先拿高课的骨架才不会把变形句越造越简单。
			ls = []int{lesson + d, lesson - d}
		}
		for _, l := range ls {
			if l <= 0 {
				continue
			}
			cands := append([]BankSentence(nil), byLesson[l]...)
			sort.SliceStable(cands, func(i, j int) bool {
				return protoScore(cands[i], pos) > protoScore(cands[j], pos)
			})
			for _, s := range cands {
				if seen[s.ID] {
					continue
				}
				seen[s.ID] = true
				out = append(out, s)
				if len(out) >= n {
					break
				}
			}
			if len(out) >= n {
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func protoList(ss []BankSentence) []protoSuggest {
	out := make([]protoSuggest, 0, len(ss))
	for _, s := range ss {
		out = append(out, protoSuggest{
			ID: s.ID, Lesson: s.Lesson, Source: s.Source,
			Answer: s.Answer, Chinese: s.Chinese,
		})
	}
	return out
}

// runSentenceReviewStats —— 只读体检：回炉变形句库「覆盖到哪儿了、难不难」。
//
// 三个问题一次答完：
//
//  1. 覆盖多少词：回炉库里有多少条、锚点词去重后多少个（跟 sentence-review-todo
//     的 qualified / covered 对得上）。
//
//  2. 覆盖多少课：变形句的 lesson 分布，旁边给主句库同课的句数做对照 ——
//     某课主句库有 30 句、变形句只有 2 句，说明那课的旧词基本还没开始回炉。
//
//  3. 难不难：变形句是「原型句换一个词」来的，长度跟原型句差不多，
//     所以句长（去空格字符数）就是现成的难度代理。跟主句库同课的平均句长
//     放一起比，就能看出变形句是不是集中在短句/低课次（= 偏简单）。
//
//     jrp --lang ja sentence-review-stats
func runSentenceReviewStats(fs *flag.FlagSet, lang string) {
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	rb, err := storage.DownloadReviewBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
		os.Exit(1)
	}
	bank, err := storage.DownloadSentenceBank(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 读取句库失败：%v\n", err)
		os.Exit(1)
	}

	// 主句库按课：句数 + 平均句长，用来跟变形句对照
	type lessonStat struct {
		Count int
		Len   int
	}
	bankBy := map[int]*lessonStat{}
	bankTotal, bankLen := 0, 0
	for _, s := range bank.Sentences {
		if s.Answer == "" {
			continue
		}
		n := groupLesson(s.Lesson)
		if bankBy[n] == nil {
			bankBy[n] = &lessonStat{}
		}
		l := jpLen(s.Answer)
		bankBy[n].Count++
		bankBy[n].Len += l
		bankTotal++
		bankLen += l
	}

	// 回炉库按课：句数 + 平均句长 + 覆盖词数
	revBy := map[int]*lessonStat{}
	words := map[int]map[string]bool{}
	revTotal, revLen := 0, 0
	seenWord := map[string]bool{}
	bySource := map[string]int{}
	for _, s := range rb.Sentences {
		if s.Answer == "" {
			continue
		}
		n := groupLesson(s.Lesson)
		if revBy[n] == nil {
			revBy[n] = &lessonStat{}
			words[n] = map[string]bool{}
		}
		l := jpLen(s.Answer)
		revBy[n].Count++
		revBy[n].Len += l
		if s.Word != "" {
			words[n][s.Word] = true
			seenWord[s.Word] = true
		}
		if s.Source != "" {
			bySource[s.Source]++
		}
		revTotal++
		revLen += l
	}

	// 课号并集，升序；课号 0（解析不出来）放最后标「未标课」
	lessons := []int{}
	seen := map[int]bool{}
	for n := range bankBy {
		if !seen[n] {
			seen[n] = true
			lessons = append(lessons, n)
		}
	}
	for n := range revBy {
		if !seen[n] {
			seen[n] = true
			lessons = append(lessons, n)
		}
	}
	sort.Slice(lessons, func(i, j int) bool {
		if lessons[i] == 0 {
			return false
		}
		if lessons[j] == 0 {
			return true
		}
		return lessons[i] < lessons[j]
	})

	rows := make([]map[string]interface{}, 0, len(lessons))
	for _, n := range lessons {
		name := lessonLabel(n)
		if n == 0 {
			name = "未标课"
		}
		b, r := bankBy[n], revBy[n]
		row := map[string]interface{}{
			"lesson": name, "review": 0, "review_words": 0,
			"review_avg_len": 0, "bank": 0, "bank_avg_len": 0,
		}
		if r != nil && r.Count > 0 {
			row["review"] = r.Count
			row["review_words"] = len(words[n])
			row["review_avg_len"] = roundAcc(float64(r.Len) / float64(r.Count))
		}
		if b != nil && b.Count > 0 {
			row["bank"] = b.Count
			row["bank_avg_len"] = roundAcc(float64(b.Len) / float64(b.Count))
		}
		rows = append(rows, row)
	}

	avg := func(total, n int) float64 {
		if n == 0 {
			return 0
		}
		return roundAcc(float64(total) / float64(n))
	}

	outputResult(map[string]interface{}{
		"success":  true,
		"command":  "sentence-review-stats",
		"language": lang,
		"note":     "只读体检，未写入任何数据。句长=去空格后的字符数，作为难度代理（越长一般越难）。",
		"summary": map[string]interface{}{
			"review_sentences": revTotal,
			"review_words":     len(seenWord),
			"review_avg_len":   avg(revLen, revTotal),
			"bank_sentences":   bankTotal,
			"bank_avg_len":     avg(bankLen, bankTotal),
			"by_source":        bySource,
		},
		"by_lesson": rows,
	})
}

// jpLen 日语句子的长度：去掉半角/全角空格再数字数，作为难度代理。
func jpLen(s string) int {
	s = strings.NewReplacer(" ", "", "\u3000", "", "\t", "").Replace(s)
	return len([]rune(s))
}

// ---------- 回炉句库读写（2026-11-03 补） ----------

// runSentenceReviewBank 回炉变形句库的上传/下载入口。
//
// 为什么以前没有它：句库由 AI 造（Go 里没分词器，判断不出句子里哪个位置
// 能换什么词），造完只能靠 COS 直传写回 —— 没校验、没去重、补一批得整库覆盖。
// 现在补批走 --merge：按锚点词去重只追加新的，已有的句子一条不动。
//
//	jrp --lang ja sentence-review-bank                    # 概览
//	jrp --lang ja sentence-review-bank --dump             # 下载整库打印到 stdout
//	jrp --lang ja sentence-review-bank --merge new.json   # 补批（推荐，只追加）
//	jrp --lang ja sentence-review-bank --file all.json    # 全量覆盖（先 --dump 下来改）
func runSentenceReviewBank(fs *flag.FlagSet, lang string) {
	file := fs.String("file", "", "本地 JSON 路径，上传**覆盖**整库")
	merge := fs.String("merge", "", "本地 JSON 路径，只**追加**（按锚点词去重，已存在则跳过）")
	dump := fs.Bool("dump", false, "下载 COS 回炉库并打印到 stdout")
	_ = fs.Parse(cmdArgs)

	storage, err := NewStorage(lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()

	switch {
	case *dump:
		rb, err := storage.DownloadReviewBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(rb)
		return

	case *file != "":
		nb, err := loadReviewFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取本地文件失败：%v\n", err)
			os.Exit(1)
		}
		fatal, warn := checkReviewSentences(ctx, storage, nb.Sentences, lang)
		reportReviewCheck(fatal, warn)
		if len(fatal) > 0 {
			fmt.Fprintln(os.Stderr, "Error: 校验没过，没有上传（改完再来）")
			os.Exit(1)
		}
		if nb.Updated == "" {
			nb.Updated = time.Now().Format("2006-01-02")
		}
		if nb.Version == 0 {
			nb.Version = 1
		}
		if err := storage.UploadReviewBank(ctx, nb); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-review-bank",
			"mode":      "file",
			"uploaded":  len(nb.Sentences),
			"bank_key":  storage.reviewBankKey(),
			"next_step": "跑 sentence-review-todo 看 missing 是否归零，再 sentence-preview 看今天会出哪些",
		})
		return

	case *merge != "":
		cur, err := storage.DownloadReviewBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		add, err := loadReviewFile(*merge)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取本地文件失败：%v\n", err)
			os.Exit(1)
		}
		haveWord := map[string]bool{}
		haveAns := map[string]bool{}
		haveID := map[string]bool{}
		for _, s := range cur.Sentences {
			if s.Word != "" {
				haveWord[s.Word] = true
			}
			haveAns[normSentence(s.Answer)] = true
			haveID[s.ID] = true
		}
		fatal, warn := checkReviewSentences(ctx, storage, add.Sentences, lang)
		reportReviewCheck(fatal, warn)
		if len(fatal) > 0 {
			fmt.Fprintln(os.Stderr, "Error: 校验没过，没有上传（改完再来）")
			os.Exit(1)
		}
		nextN := nextReviewIDNum(cur.Sentences)
		var added []ReviewSentence
		var skipped []string
		for _, s := range add.Sentences {
			switch {
			case s.Word != "" && haveWord[s.Word]:
				skipped = append(skipped, s.Word+"（已有变形句）")
				continue
			case haveAns[normSentence(s.Answer)]:
				skipped = append(skipped, s.Answer+"（句子已存在）")
				continue
			}
			if s.ID == "" || haveID[s.ID] {
				for {
					nextN++
					id := fmt.Sprintf("R%03d", nextN)
					if !haveID[id] {
						s.ID = id
						break
					}
				}
			}
			if s.Type == "" {
				s.Type = "transformed"
			}
			haveID[s.ID] = true
			haveAns[normSentence(s.Answer)] = true
			if s.Word != "" {
				haveWord[s.Word] = true
			}
			added = append(added, s)
		}
		if len(added) == 0 {
			outputResult(map[string]interface{}{
				"success": true, "command": "sentence-review-bank", "mode": "merge",
				"added": 0, "skipped": skipped,
				"hint": "没有新句子要加（可能这批词都已经有变形句了）",
			})
			return
		}
		cur.Sentences = append(cur.Sentences, added...)
		cur.Updated = time.Now().Format("2006-01-02")
		if cur.Version == 0 {
			cur.Version = 1
		}
		if err := storage.UploadReviewBank(ctx, cur); err != nil {
			fmt.Fprintf(os.Stderr, "Error: 上传回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-review-bank",
			"mode":      "merge",
			"added":     len(added),
			"total":     len(cur.Sentences),
			"skipped":   skipped,
			"bank_key":  storage.reviewBankKey(),
			"next_step": "跑 sentence-review-todo 看 missing 是否下降，再 sentence-preview",
		})
		return

	default:
		rb, err := storage.DownloadReviewBank(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: 读取回炉句库失败：%v\n", err)
			os.Exit(1)
		}
		byLesson := map[string]int{}
		words := map[string]bool{}
		bySource := map[string]int{}
		for _, s := range rb.Sentences {
			byLesson[s.Lesson]++
			bySource[s.Source]++
			if s.Word != "" {
				words[s.Word] = true
			}
		}
		lessons := make([]string, 0, len(byLesson))
		for k := range byLesson {
			lessons = append(lessons, k)
		}
		sortLessons(lessons)
		outputResult(map[string]interface{}{
			"success":   true,
			"command":   "sentence-review-bank",
			"version":   rb.Version,
			"updated":   rb.Updated,
			"total":     len(rb.Sentences),
			"words":     len(words),
			"by_lesson": byLesson,
			"by_source": bySource,
			"bank_key":  storage.reviewBankKey(),
		})
	}
}

// loadReviewFile 读本地回炉句文件。两种格式都收：
// {"sentences": [...]}（整库，--file 用）和裸数组 [...](补批，--merge 用)。
func loadReviewFile(path string) (*ReviewBank, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b ReviewBank
	if err := json.Unmarshal(data, &b); err == nil && len(b.Sentences) > 0 {
		return &b, nil
	}
	var ss []ReviewSentence
	if err := json.Unmarshal(data, &ss); err != nil {
		return nil, err
	}
	return &ReviewBank{Sentences: ss}, nil
}

// checkReviewSentences 上传前的体检，返回致命问题和提醒。
//
// 致命（阻断上传）：锚点词不在档案里（词形写错，挑句时查不到就会永远轮不到）、
// 原型句不在主句库里（变形句必须是课本原句的改造）、answer 空/跟课本原文撞车、
// id 重复。
// 提醒（只打印）：变形句跟原型句的空格分段数不一致 —— 变形规则是「只换一个词、
// 助词序列不动」，段数对不上多半是助词被改了，人看一眼再决定。
//
// 档案/主句库读不了时跳过对应那项（fail-open）：补批是人工活，
// 网络抖一下不该把整批卡住，但校验项会在输出里标注"未校验"。
func checkReviewSentences(ctx context.Context, st *Storage, ss []ReviewSentence, lang string) (fatal []string, warn []string) {
	bank, errBank := st.DownloadSentenceBank(ctx)
	protoBy := map[string]BankSentence{}
	bankAns := map[string]bool{}
	if errBank == nil && bank != nil {
		for _, s := range bank.Sentences {
			if s.Answer == "" {
				continue
			}
			protoBy[normSentence(s.Answer)] = s
			bankAns[normSentence(s.Answer)] = true
		}
	}
	arcWords := map[string]bool{}
	if data, _, err := st.DownloadLatestArchive(ctx); err == nil {
		if arc, err := archive.ParseArchive(string(data), lang); err == nil {
			for _, g := range arc.Groups {
				for _, w := range g.Words {
					if w.Word != "" {
						arcWords[w.Word] = true
					}
				}
			}
		}
	}

	seenID := map[string]bool{}
	seenAns := map[string]bool{}
	for i := range ss {
		s := &ss[i]
		tag := s.ID
		if tag == "" {
			tag = s.Word
		}
		if s.Word == "" {
			fatal = append(fatal, fmt.Sprintf("第 %d 条：缺 word（锚点词）", i+1))
		}
		if s.Answer == "" {
			fatal = append(fatal, fmt.Sprintf("%s：缺 answer", tag))
			continue
		}
		if s.Word != "" && len(arcWords) > 0 && !arcWords[s.Word] {
			fatal = append(fatal, fmt.Sprintf("%s：锚点词 %q 不在档案里（词形写错了？）", tag, s.Word))
		}
		if s.Prototype == "" {
			fatal = append(fatal, fmt.Sprintf("%s：缺 prototype（原型句）", tag))
		} else if len(protoBy) > 0 {
			if p, ok := protoBy[normSentence(s.Prototype)]; !ok {
				fatal = append(fatal, fmt.Sprintf("%s：原型句不在主句库里 → %s", tag, s.Prototype))
			} else {
				if s.PrototypeID == "" {
					s.PrototypeID = p.ID
				}
				if s.Source == "" {
					s.Source = p.Source
				}
				if s.SourceDoc == "" {
					s.SourceDoc = p.SourceDoc
				}
				if len(strings.Fields(s.Prototype)) != len(strings.Fields(s.Answer)) {
					warn = append(warn, fmt.Sprintf("%s：分段数跟原型句不同（%d vs %d）—— 助词序列可能被改动了：%s",
						tag, len(strings.Fields(s.Prototype)), len(strings.Fields(s.Answer)), s.Answer))
				}
			}
		}
		if bankAns[normSentence(s.Answer)] {
			fatal = append(fatal, fmt.Sprintf("%s：变形句跟课本原文一模一样 → %s", tag, s.Answer))
		}
		if seenAns[normSentence(s.Answer)] {
			fatal = append(fatal, fmt.Sprintf("%s：这一批里句子重复 → %s", tag, s.Answer))
		}
		seenAns[normSentence(s.Answer)] = true
		if s.ID != "" {
			if seenID[s.ID] {
				fatal = append(fatal, fmt.Sprintf("id 重复：%s", s.ID))
			}
			seenID[s.ID] = true
		}
	}
	if errBank != nil {
		warn = append(warn, "主句库读不了，原型句/撞车这两项没校验")
	}
	if len(arcWords) == 0 {
		warn = append(warn, "档案读不了，锚点词这项没校验")
	}
	return fatal, warn
}

func reportReviewCheck(fatal, warn []string) {
	if len(warn) > 0 {
		fmt.Fprintln(os.Stderr, "提醒：")
		for _, w := range warn {
			fmt.Fprintf(os.Stderr, "  - %s\n", w)
		}
	}
	if len(fatal) > 0 {
		fmt.Fprintln(os.Stderr, "致命：")
		for _, f := range fatal {
			fmt.Fprintf(os.Stderr, "  - %s\n", f)
		}
	}
}

// nextReviewIDNum 现有 id 里最大的序号（R308 → 308），补批时从它往后编。
// 取 max(现有最大序号, 条数) —— id 有空洞（比如删过中间几条）时也不会撞号。
func nextReviewIDNum(ss []ReviewSentence) int {
	max := len(ss)
	re := regexp.MustCompile(`^R(\d+)$`)
	for _, s := range ss {
		m := re.FindStringSubmatch(strings.TrimSpace(s.ID))
		if len(m) != 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err == nil && n > max {
			max = n
		}
	}
	return max
}
