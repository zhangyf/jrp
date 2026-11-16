package words

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CircleGroupOf 决定一个新词进哪个分组：按手册课号「第N课 手册补录」。
// 课号为 0 又没显式给组名时落「未分组」，跟 add-words 保持一致。
func CircleGroupOf(it CircleItem) string {
	if g := strings.TrimSpace(it.Group); g != "" {
		return g
	}
	if it.Lesson > 0 {
		return fmt.Sprintf("第%d课 手册补录", it.Lesson)
	}
	return "未分组"
}

// LessonOfGroup 从分组名里抠课号（「第21课 手册补录」→ 21）。抠不出来返回 0。
// unhold 按课放开时用：老师说「学到第 21 课了」，就把这一课的词放出来。
func LessonOfGroup(title string) int {
	i := strings.Index(title, "第")
	if i < 0 {
		return 0
	}
	rest := title[i+len("第"):]
	j := strings.Index(rest, "课")
	if j <= 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
	if err != nil {
		return 0
	}
	return n
}

// CircleByPos 手动录入词的圆级规则（老师 2026-11-13 定）：
// 动词→三圆、形容词→二圆、副词→二圆；其他词性（名词/连体词/助词…）→空串。
// 空串 = 手册里没收录 / 不套这个简单规则，词表里那格就空着。
// 老写法（动/形/副）也认，跟 miniprogram/utils/pos.js 的 POS_ALIAS 同一套归一口径。
func CircleByPos(pos string) string {
	switch strings.TrimSpace(pos) {
	case "动词", "动":
		return "三圆"
	case "形容词", "形", "副词", "副":
		return "二圆"
	default:
		return ""
	}
}

// CircleMap 摊出「词形 -> 圆级」（三圆 / 二圆 / 一圆），供造句子挑句时加权。
//
// 拿不到词性表（读失败 / 一条都没标）返回 nil —— 调用方按「不分层」处理，
// 出题行为跟加这个功能之前一模一样。宁可少加权，也不能因为读不到词表就少出题。
func CircleMap(meta *WordMeta) map[string]string {
	if meta == nil || len(meta.Items) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta.Items))
	for w, p := range meta.Items {
		if c := strings.TrimSpace(p.Circle); c != "" {
			out[w] = c
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LookupWordMeta 按完整词形「假名(汉字)」查词性 / 备注 / 圆级。
// 拿不到就四个空串 —— 词形对不上绝不猜，猜错会把另一个词的词性贴上来。
func LookupWordMeta(meta *WordMeta, word string) (pos, sub, note, circle string) {
	if meta == nil {
		return "", "", "", ""
	}
	p, ok := meta.Items[word]
	if !ok {
		return "", "", "", ""
	}
	return p.Pos, p.Sub, p.Note, p.Circle
}

// HoldSet 把词性表里的 hold 标记抽成一张「要跳过」的集合。
// 读不到词表 / 一个都没挂起都返回 nil —— fail-open，不能因为读不到
// 配置就把整份复习计划清空。
func HoldSet(meta *WordMeta) map[string]bool {
	if meta == nil || len(meta.Items) == 0 {
		return nil
	}
	out := map[string]bool{}
	for w, p := range meta.Items {
		if p.Hold {
			out[w] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NearKeys 找出包含给定子串的词形，最多 5 个（子串从 2 个字符起才有效）。
// 给 wordnote 提示「你是不是想改这几个」用：老师经常只打假名，
// 所以先去掉括号里的汉字写法再匹配，结果排序后截断，避免刷屏。
func NearKeys(items map[string]WordPOS, w string) []string {
	key := strings.TrimSpace(w)
	// 去掉括号里的汉字写法再试，老师经常只打假名
	if i := strings.IndexAny(key, "（("); i > 0 {
		key = strings.TrimSpace(key[:i])
	}
	if len([]rune(key)) < 2 {
		return nil
	}
	var out []string
	for k := range items {
		if strings.Contains(k, key) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}
