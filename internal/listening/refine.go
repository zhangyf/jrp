package listening

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// 字幕精校（方案 A：纯文本、保守策略）。
//
// 老师在 COS 桶里搭了工作流：上传视频后自动转封装 + ASR 生成 SRT。ASR 的日语
// 识别错误不少（聴解→懲戒、問題用紙→問題要旨、番号乱认……）。这里让大模型
// **只改确定的错误**，时间轴一个字不碰 —— 宁可漏改也不改坏。
//
// 为什么不给模型视频（2026-11-06 与老师对齐）：
//   - 语言层错误（同音词、搭配、固定指令句）靠上下文就能确定，纯文本够用；
//   - 数字/专名/选择题选项这类「必须听音频才知道」的，模型不知道答案，
//     **保持原样**，不猜。这是保守策略的核心：没把握就别动。
//
// 数据流：SRT 文本 → 分句 → 分批喂模型 → 模型回「编号: 改正文本」→ 只替换被
// 明确点名的句子 → 拼回 SRT（时间轴原样保留）。

// refineBatchSize 一批喂多少句给模型。字幕动辄几百句，一次全塞会超 token，
// 也让模型注意力涣散；40 句一批，模型能盯得住每一句。
const refineBatchSize = 40

// RefineBatchSize 暴露批次大小给根包的编排层用（同包常量不跨包可见）。
func RefineBatchSize() int { return refineBatchSize }

// RefinePrompt 是精校任务的系统指令。外置到 COS 可调（跟 judge-prompt 同套路），
// 这里是内置兜底版本。
const RefinePrompt = `你是日语字幕精校助手。下面是一段日语听力字幕（通常是 JLPT 听力真题），
它由语音识别(ASR)自动生成，存在识别错误。你的任务是**只修正确定的错误**。

规则（务必遵守）：
1. 只修「语言层面能确定」的错误：同音/近音词、明显的搭配或语法错误、
   JLPT 听力的固定指令句、番号递增（一番→二番→三番…）。
2. **没把握就保持原样**：数字、人名、地名、选择题选项内容——这些必须听音频才能确认，
   你听不到，一律不要改。宁可漏改，也不能把对的改成错的。
3. **只改文字，绝不改时间轴**。你也看不到时间轴，只处理句子文本。
4. 保持原有的敬体/简体、标点风格，不要润色、不要翻译、不要增删句子。
5. 每一句单独判断，不要因为上下文顺口就擅自改写整句。

输出格式（严格照做，方便程序解析）：
- 只输出**需要修改**的句子，格式为「编号: 改正后的完整句子」。
- 编号就是输入里每句前面方括号里的数字。
- 不需要修改的句子**不要输出**。
- 一行一句，不要输出解释、不要输出未修改的句子、不要加代码块标记。
- 如果整批都没有需要修改的，输出「无」。

示例：
输入：
[1] 懲戒試験を始めます。
[2] 問題要旨を挙げてください。
[3] 私の暇な日は、日曜日だけですが。
输出：
1: 聴解試験を始めます。
2: 問題用紙を挙げてください。
`

// RefineCue 是精校过程中「一句」的中间表示：编号（1-based）+ 原文本。
type RefineCue struct {
	Num  int
	Text string
}

// refineLineRe 匹配模型回的「编号: 文本」行。编号允许全角数字，冒号允许全角。
var refineLineRe = regexp.MustCompile(`^\s*([0-9０-９]+)\s*[:：]\s*(.+?)\s*$`)

// BuildRefineInput 把 cues 拼成给模型看的带编号文本。
// 只取文本，不带时间轴——模型不需要也不该看到时间。
func BuildRefineInput(cues []Cue, start, end int) string {
	var b strings.Builder
	for i := start; i < end && i < len(cues); i++ {
		b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, cues[i].Text))
	}
	return b.String()
}

// ParseRefineReply 解析模型回的「编号: 改正文本」，返回编号→新文本。
// 解析不了的行跳过（不整体报错）——模型偶尔会加一句解释，忽略即可。
// 返回的 map 只含模型明确点名的编号，没点名的句子保持原样。
func ParseRefineReply(reply string) map[int]string {
	out := map[int]string{}
	for _, ln := range strings.Split(reply, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln == "无" || strings.HasPrefix(ln, "```") {
			continue
		}
		m := refineLineRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(normalizeDigits(m[1]))
		if err != nil || n < 1 {
			continue
		}
		out[n] = m[2]
	}
	return out
}

// ApplyRefinements 把模型给的修改应用到 cues，返回改了几处。
// 只替换模型明确点名的编号；编号越界（模型幻觉出的编号）忽略。
// ⚠️ 时间轴完全不动——这里只碰 Text。
func ApplyRefinements(cues []Cue, changes map[int]string) int {
	applied := 0
	for n, newText := range changes {
		if n < 1 || n > len(cues) {
			continue
		}
		newText = strings.TrimSpace(newText)
		if newText == "" || newText == cues[n-1].Text {
			continue
		}
		cues[n-1].Text = newText
		applied++
	}
	return applied
}

// RenderSRT 把 cues 拼回标准 SRT 文本。时间轴用 cue 里已有的秒数反格式化。
func RenderSRT(cues []Cue) string {
	var b strings.Builder
	for i, c := range cues {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("\n")
		b.WriteString(formatClock(c.Start))
		b.WriteString(" --> ")
		b.WriteString(formatClock(c.End))
		b.WriteString("\n")
		b.WriteString(c.Text)
		b.WriteString("\n\n")
	}
	return b.String()
}

// formatClock 秒 → "HH:MM:SS,mmm"。
//
// ⚠️ 毫秒必须四舍五入不能截断：4.02 的 (4.02-4)*1000 是 19.999…，
// int() 直接截成 19，字幕时间会整体偏 1ms（round-trip 就对不回去了）。
// 四舍五入后 ms 可能进位到 1000，要进到秒。
func formatClock(s float64) string {
	if s < 0 {
		s = 0
	}
	ms := int(math.Round(s * 1000))
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	sec := (ms % 60000) / 1000
	ms %= 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, sec, ms)
}

// normalizeDigits 全角数字转半角，方便 Atoi。
func normalizeDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '０' && r <= '９' {
			b.WriteRune(r - '０' + '0')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
