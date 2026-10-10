package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"strconv"
	"time"
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
	arc, err := ParseArchive(string(data), lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: 解析档案失败：%v\n", err)
		os.Exit(1)
	}
	meta, _ := storage.DownloadWordMeta(ctx) // 读不到就留空，不致命

	today := time.Now()
	type item struct {
		Word     string        `json:"word"`
		Pos      string        `json:"pos"`
		Def      string        `json:"def"`
		Lesson   string        `json:"lesson"`
		Reviews  int           `json:"reviews"`
		Accuracy float64       `json:"accuracy"`
		IdleDays int           `json:"idle_days"`
		DueIn    int           `json:"days_to_qualify,omitempty"` // 仅"在路上"的词有
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
			pos, _, _ := lookupWordMeta(meta, w.Word)
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
			"min_reviews":    2,
			"min_accuracy":   0.8,
			"min_idle_days":  DefaultReviewIdleDays,
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

// groupProtosByLesson 把原型句按课号分组，组内按「基本课文优先 + 短句优先」排序
// —— 短句好改造，基本课文是课本里最典型的句型。
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
			return len([]rune(list[i].Answer)) < len([]rune(list[j].Answer))
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
	if n < 7 {
		score -= 30 // 没槽位可换
	}
	if n > 22 {
		score -= 8 // 太长难改造
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
			ls = []int{lesson - d, lesson + d}
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
