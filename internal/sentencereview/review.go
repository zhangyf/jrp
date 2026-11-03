package sentencereview

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/sentence"
)

// 回炉变形句：把「正确率不错、但很久没见面」的词塞回课本句型里再练一遍。
//
// 背景（2026-10-09 老师提的）：档案里有一大批正确率≥80%、但 30 天以上没复习的词。
// 按艾宾浩斯间隔它们还没到期，可实际已经忘得差不多了。直接加进单词复习等于
// 「又背一遍词表」——老师明确不要。所以走造句：拿句库里的原型句，只换掉一个词，
// 助词序列一个字不动，写整句时自然要把那个词写出来。
//
// 为什么不从课本原文里找含该词的句子：实测 290 个回炉词只有 58% 能在课本句子里
// 找到，没命中的全是カナダ / ピザ / アニメ / まんが 这类生词表名词——标日初级
// 前 17 课课文里压根没出现过。变形句不看运气，覆盖率 100%。
//
// 句库由 AI 生成（词性匹配、语义合理性由人/AI 把关），Go 只负责挑，不做任何
// 换词拼接——Go 里没有分词器，判断不出句子里哪个位置能换什么词性。

// DefaultReviewIdleDays 一个词闲置多少天以上才算「回炉」。
const DefaultReviewIdleDays = 30

// CircleRank 圆级分档，数字小的先出：三圆 0、二圆 1、一圆 2、没标 3。
//
// ⚠️ 只认 word_meta 里存的这三个中文值 —— 手册就是这么标的手写分级，
// 写成别的（"3圆"/"三星"）一律落「没标」档，不参与优先。
func CircleRank(circle map[string]string, word string) int {
	switch circle[word] {
	case "三圆":
		return 0
	case "二圆":
		return 1
	case "一圆":
		return 2
	}
	return 3
}

// WordIdleDays 算某个词距上次复习过了多少天。
//
// 档案里 LastReview 只有 MM/DD，没有年份。规则：不晚于今天就算今年，
// 晚于今天（比如今天是 10/09、值是 12/25）就算去年 —— 跨年只可能往回推一年。
// 解析不出来返回 -1，调用方按「不限制」处理，宁可多练不要漏。
func WordIdleDays(w review.Word, today time.Time) int {
	parts := strings.Split(strings.TrimSpace(w.LastReview), "/")
	if len(parts) != 2 {
		return -1
	}
	mm, err1 := strconv.Atoi(parts[0])
	dd, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || mm < 1 || mm > 12 || dd < 1 || dd > 31 {
		return -1
	}
	year := today.Year()
	curMM, curDD := int(today.Month()), today.Day()
	if mm > curMM || (mm == curMM && dd > curDD) {
		year--
	}
	d := time.Date(year, time.Month(mm), dd, 0, 0, 0, 0, today.Location())
	if d.After(today) {
		return -1
	}
	return int(today.Sub(d).Hours() / 24)
}

// wordIdleMap 把档案摊成「完整词形 -> 闲置天数」，供挑句时过滤。
func wordIdleMap(arc *archive.Archive, today time.Time) map[string]int {
	out := map[string]int{}
	if arc == nil {
		return out
	}
	for _, g := range arc.Groups {
		for _, w := range g.Words {
			if w.Word == "" {
				continue
			}
			if d := WordIdleDays(w, today); d >= 0 {
				out[w.Word] = d
			}
		}
	}
	return out
}

// BuildReviewSentences 挑 n 条回炉句。纯函数，不碰 COS。
//
// 三条筛选，顺序不能反：
//  1. exclude —— 去掉当天已经出过的（原句轮转或错句重练里已包含）
//  2. 闲置过滤 —— 锚点词最近刚复习过（< minIdleDays）就跳过。句库是静态的，
//     不刷这一层的话，练过的词会一直占着名额，回炉就退化成随机抽句
//  3. 「最久没出过」排序 —— 池子比每天的名额大得多（290 词 vs 12 句），
//     不排序永远轮不到后半截
//  4. 同一原型句当天只用一次 —— 原型句当天被选中过就跳过它的变形句，
//     且多个回炉词共用一个骨架时也只留一条。否则会出现第 20 句写
//     「森さんは 先週 休みました」、第 22 句写「森さんは 今日 休みました」，
//     骨架刚练过一遍，变形句等于白出。
//  5. 圆级分层（2026-11-03）：三圆词（手册最高频）拿 circleRatio% 的名额，
//     剩下的名额给二圆 / 一圆 / 没标。层内仍按第 3 步的「最久没出过」排。
//     三圆池子不够就由其他档补满 —— 是「占大头」不是「独占」，题量永不缩水。
//     circle 为 nil（读不到 word_meta）或 circleRatio <= 0 时整步跳过，
//     行为跟没加这个功能时一模一样。
//
// arc 传 nil 时跳过第 2 步（拿不到档案就当全都回炉，宁可多练）。
func BuildReviewSentences(bank *ReviewBank, hist sentence.SentenceHistory, arc *archive.Archive,
	today time.Time, n int, exclude map[string]bool, minIdleDays int,
	circle map[string]string, circleRatio int) []sentence.BankSentence {

	if bank == nil || n <= 0 {
		return nil
	}

	idle := wordIdleMap(arc, today)

	// 每句上次出过的时间（复用 sentence_history，跟原句共用同一套去重账本）。
	lastSeen := make(map[string]time.Time)
	for dateStr, answers := range hist {
		d, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		for _, a := range answers {
			k := sentence.Norm(a)
			if k == "" {
				continue
			}
			if cur, ok := lastSeen[k]; !ok || d.After(cur) {
				lastSeen[k] = d
			}
		}
	}

	type cand struct {
		s    ReviewSentence
		last time.Time // 零值 = 从没出过
	}
	var cs []cand
	for _, s := range bank.Sentences {
		if s.Answer == "" {
			continue
		}
		k := sentence.Norm(s.Answer)
		if k == "" || exclude[k] {
			continue
		}
		// 原型句当天已经出过（作为原句或别的变形句）→ 跳过
		if s.Prototype != "" && exclude[sentence.Norm(s.Prototype)] {
			continue
		}
		if minIdleDays > 0 && s.Word != "" {
			if d, ok := idle[s.Word]; ok && d >= 0 && d < minIdleDays {
				continue
			}
		}
		cs = append(cs, cand{s: s, last: lastSeen[k]})
	}

	// 从没出过优先，其次最久没出过，最后按 id 稳定（跟 sortCands 同一套语义）。
	sort.SliceStable(cs, func(i, j int) bool {
		zi, zj := cs[i].last.IsZero(), cs[j].last.IsZero()
		if zi != zj {
			return zi
		}
		if !zi && !cs[i].last.Equal(cs[j].last) {
			return cs[i].last.Before(cs[j].last)
		}
		return cs[i].s.ID < cs[j].s.ID
	})

	out := make([]sentence.BankSentence, 0, n)
	usedProto := make(map[string]bool)
	// take 把 list 里的候选加进 out，直到 out 有 limit 条或 list 走完。
	// 同一骨架当天只留一条（usedProto 跨层共享，分层也不能破这条）。
	take := func(list []cand, limit int) {
		for _, c := range list {
			if len(out) >= limit {
				return
			}
			if c.s.Prototype != "" {
				pk := sentence.Norm(c.s.Prototype)
				if usedProto[pk] {
					continue
				}
				usedProto[pk] = true
			}
			out = append(out, sentence.BankSentence{
				ID:        c.s.ID,
				Lesson:    c.s.Lesson,
				Type:      "transformed",
				Prototype: c.s.Prototype,
				Answer:    c.s.Answer,
				Chinese:   c.s.Chinese,
			})
		}
	}

	// 圆级分层：三圆一组，其余一组，组内保持上面排好的顺序。
	// quota 是三圆的名额上限，不是保底 —— 三圆不够时剩下的名额由其他档吃掉。
	if circleRatio > 0 && circle != nil {
		var three, rest []cand
		for _, c := range cs {
			if CircleRank(circle, c.s.Word) == 0 {
				three = append(three, c)
			} else {
				rest = append(rest, c)
			}
		}
		take(three, n*circleRatio/100) // 三圆占大头
		take(rest, n)                  // 其余补满
		take(three, n)                 // 都不够再把三圆剩的补上（题量优先）
		return out
	}

	take(cs, n)
	return out
}
