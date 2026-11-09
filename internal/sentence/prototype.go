package sentence

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 回炉变形句的「挑原型句骨架」规则（原 cmd_sentence_review.go 里的纯函数）。
// Go 里没有分词器，造句子由 AI 完成，这里只负责给某个待补词从主句库里
// 挑几条能套的骨架：先同课、再按课号距离往外扩，同课内按词性槽位打分。

var groupLessonRe = regexp.MustCompile(`第(\d+)课`)

// GroupLesson 从档案分组标题里抽课号（「第1课01 基础词（5/25）」→ 1）。
// 抽不到返回 0，调用方按"课号未知"处理。
func GroupLesson(group string) int {
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

func LessonLabel(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("第%d课", n)
}

// RoundAcc 保留两位小数，避免 JSON 里出现 0.9166666666666666 这种。
func RoundAcc(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// JpLen 日语句子的长度：去掉半角/全角空格再数字数，作为难度代理。
func JpLen(s string) int {
	s = strings.NewReplacer(" ", "", "\u3000", "", "\t", "").Replace(s)
	return len([]rune(s))
}

// GroupProtosByLesson 把原型句按课号分组，组内按「基本课文优先 + 长句优先」排序
// —— 基本课文是课本里最典型的句型。
//
// ⚠️ 2026-10-20 老师反馈：变形句偏简单（平均 13.8 字，主句库 15.9；1-7 课占 64%）。
// 原来这里按「短句优先」排（短句好改造），结果造出来的全是短句。
// 改成「长句优先」：骨架本身有内容，换进去的那个词才练得到东西。
// 真正决定顺序的是 ProtoScore（见下），这里只作为同分时的稳定次序。
func GroupProtosByLesson(sentences []BankSentence) map[int][]BankSentence {
	out := map[int][]BankSentence{}
	for _, s := range sentences {
		if s.Answer == "" {
			continue
		}
		n := GroupLesson(s.Lesson)
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

// ProtoScore 给原型句打分：这个词能不能套进去。
//
// 只按课号匹配是不够的——「なごや（名古屋）」在第4课，同课最短的几条是
// 「だれも いません。」这种没名词槽的句子，套不进去。所以按词性看句子里
// 有没有对应的槽位；太短的句子（「はい、そうです。」）一律降权。
func ProtoScore(s BankSentence, pos string) int {
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

// PickPrototypes 给某个词挑 n 条原型句骨架。
//
// 2026-11-09 解绑「单词」和「句式」：原来「先同课、再按课号距离往外扩」——
// 第 1 课的词被强制配第 1 课的「～は～です」简单句，回炉句越练越简单
// （实测前 7 课占 254/397 = 64%，第 18-22 课 0 句）。
// 老师明确：单词是前面的单词没关系，句式要用后面课的复杂句式，且要多样。
//
// 新逻辑：全库摊平，按 ProtoScore（词性槽位能不能套进去 + 长句偏好）全局排序
// 取前 n，不再按课号外扩。词性匹配仍是第一优先（套不进去的句子排后面），
// 长句偏好天然倾向高课的复杂长句；lesson 退化为「同分时同课略优先」的
// tie-break（同课句子含这个词的概率更高），不再决定起始位置。
func PickPrototypes(byLesson map[int][]BankSentence, lesson int, n int, pos string) []BankSentence {
	if n <= 0 {
		return nil
	}
	var all []BankSentence
	for _, list := range byLesson {
		all = append(all, list...)
	}
	if len(all) == 0 {
		return nil
	}
	sort.SliceStable(all, func(i, j int) bool {
		si, sj := ProtoScore(all[i], pos), ProtoScore(all[j], pos)
		if si != sj {
			return si > sj
		}
		// 同分 tie-break：同课的略优先（更可能含这个词），再按 ID 稳定排序。
		li, lj := GroupLesson(all[i].Lesson), GroupLesson(all[j].Lesson)
		if (li == lesson) != (lj == lesson) {
			return li == lesson
		}
		return all[i].ID < all[j].ID
	})
	if len(all) > n {
		all = all[:n]
	}
	return all
}

func ProtoList(ss []BankSentence) []ProtoSuggest {
	out := make([]ProtoSuggest, 0, len(ss))
	for _, s := range ss {
		out = append(out, ProtoSuggest{
			ID: s.ID, Lesson: s.Lesson, Source: s.Source,
			Answer: s.Answer, Chinese: s.Chinese,
		})
	}
	return out
}

// LoadReviewFile 读本地回炉句文件。两种格式都收：
// {"sentences": [...]}（整库，--file 用）和裸数组 [...](补批，--merge 用)。
func LoadReviewFile(path string) (*ReviewBank, error) {
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

// NextReviewIDNum 现有 id 里最大的序号（R308 → 308），补批时从它往后编。
// 取 max(现有最大序号, 条数) —— id 有空洞（比如删过中间几条）时也不会撞号。
func NextReviewIDNum(ss []ReviewSentence) int {
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
