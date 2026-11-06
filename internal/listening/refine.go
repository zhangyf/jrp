package listening

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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

// 切分策略见 refine_config.go：默认**按题目**（题号边界）分段，一段 = 一道题，
// 不再按固定句数硬切 —— 按句数切会把同一道题劈成两段，模型看不到题目全貌，
// 碎片句也就救不回来（2026-11-06 老师提的）。

// RefinePrompt 是精校任务的系统指令。外置到 COS 可调（configs/refine_prompt.txt，
// 跟 judge-prompt 同套路），这份是内置兜底版本。
//
// 2026-11-06 按 ①② 调过（实测暴露「碎片句救不了、模型偏保守」）：
//   - ① 加领域锚点：明说这是 JLPT 听力真题、开头/结尾有固定套话，并列出高频 ASR
//     混淆对（聴解↔懲戒、問題用紙↔問題要旨、番号被切成バ/ば/段）——给模型推断的抓手。
//   - ② 放宽碎片句尺度：上下文/固定套话能明确推断原意时（含被切碎的词）可以修；
//     但数字、人名、地名、选择题选项**仍然保持原样**（这些必须听音频，红线不动）。
const RefinePrompt = `你是日语字幕精校助手。下面是一段**日语听力字幕**，绝大多数是 JLPT 听力真题
（聴解試験），由语音识别(ASR)自动生成，识别错误不少。你的任务是**只修正确定的错误**。

【你会看到什么】
- 给你的内容通常是一整道题：从题号开始，包含题干、会话、質問、选项，到下一道题之前结束。
  同一段里的句子属于同一道题，可以互相印证。
- 开头那段是考试说明（固定套话），不是题目。

【唯一的判断标准：这句话在日语语法上是否成立】
- **成立 → 一个字都不要动**，哪怕它跟 JLPT 标准原文不一样。
- **不成立**（缺助词、活用错、词不成词、完全讲不通）→ 改成语法通顺、最接近原字幕的写法。

【最重要的一条：禁止用你记忆里的标准原文替换字幕】
这是最容易犯、也最严重的错误。ASR 听到什么就是什么，你不是在还原原版脚本。
- ❌ 字幕「メモをとってもいいです。」语法通顺 → 不许改成「メモを取らないでください。」
- ❌ 字幕「問題用紙を配ります。」通顺 → 不许改成「問題用紙を取ってください。」
- ❌ 字幕「旅行しましょう。」通顺 → 不许改成「旅行しました。」
- ❌ 字幕「話す時間が短いです」通顺 → 不许改成「始まる時間が早いです」
- ✅ 字幕「七月のお願いします。」缺助词 → 改「七月のをお願いします。」
- ✅ 字幕「午後は一回の三番の教室」里「一回」讲不通 → 改「一階」（同音词纠错）
- ✅ 字幕「市原さんの中から。」在选项语境里讲不通 → 改「一から四の中から。」

【碎片句】
- 题号被切碎（「に。」「さん。」），结合所在位置补成完整题号：「二番。」「三番。」
- 补全要写完整形式，不要只写「二」「三」。
- **补全后不要跟相邻句子重复**：如果补出来的话和上一句/下一句几乎一样，说明补多了，
  保持原样（❌ 上一句已是「聴解試験を始めます。」，就不要把「これから聴解の。」补成同一句）。
- **补不出来就保持原样**，绝不猜成别的话（❌ 不许把「さん。」改成「そうですか。」）。

【绝对不能碰的】
- 数字、人名、地名、物品、动作、选择题选项的具体内容 —— 听不到就不动。
- 时间轴（你看不到，也不必管）。
- 通顺句子的语气/敬简体（不许删「です」「ます」，也不许补）。

【输出格式，严格照做，方便程序解析】
- 只输出**需要修改**的句子，格式为「编号: 改正后的完整句子」。
- 编号就是输入里每句前面方括号里的数字。
- 不需要修改的句子**不要输出**。
- 一行一句，不要输出解释、不要输出未修改的句子、不要加代码块标记。
- 如果整段都没有需要修改的，输出「无」。
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

// ---- prompt 外置（跟 judge-prompt 同套路）----

// ValidateRefinePrompt 校验外置精校模板。护栏：必须包含输出格式关键标记
// 「编号:」的说明，否则模型不知道按什么格式回，ParseRefineReply 抠不出东西。
// 精校模板没有占位符（字幕是拼在模板后面的），所以只校验非空 + 输出格式说明。
func ValidateRefinePrompt(tpl string) error {
	if strings.TrimSpace(tpl) == "" {
		return fmt.Errorf("模板为空")
	}
	// 输出格式说明里必须有「编号」和冒号示例，否则模型回的格式程序解析不了。
	if !strings.Contains(tpl, "编号") {
		return fmt.Errorf("模板缺少输出格式说明（应包含「编号:」）")
	}
	return nil
}

// ChooseRefinePrompt 在「COS 上读到的」和「内置的」之间做选择。
// 读不到、空的、校验不过，一律回退内置——精校绝不因为一份坏文件卡住。
func ChooseRefinePrompt(cosText string, err error) (tpl, source string) {
	if err != nil || strings.TrimSpace(cosText) == "" {
		return RefinePrompt, "builtin"
	}
	if err := ValidateRefinePrompt(cosText); err != nil {
		log.Printf("refine-prompt: COS 上的模板不可用（%v），回退内置模板", err)
		return RefinePrompt, "builtin"
	}
	return cosText, "cos"
}

// RefinePromptVersion 模板版本：内容 sha256 前 12 位。改一个字版本就变，
// 便于排查「这次精校用的是哪版 prompt」。
func RefinePromptVersion(tpl string) string {
	sum := sha256.Sum256([]byte(tpl))
	return hex.EncodeToString(sum[:])[:12]
}

// RefineConfigStore 读 COS 上的精校模板。实现方是根包 *Storage 的
// DownloadRefinePrompt。用例只读不写（上传/删除走 CLI，不进接口）。
type RefineConfigStore interface {
	DownloadRefinePrompt(ctx context.Context) (string, error)
}

// refinePromptTTL 模板缓存时长。精校是视频上传才触发的低频动作，60 秒足够，
// 改模板后最多 60 秒生效。
const refinePromptTTL = 60 * time.Second

var refinePromptCache = struct {
	sync.Mutex
	at      time.Time
	tpl     string
	version string
	source  string
}{}

// ResolveRefinePrompt 拿当前生效的精校模板：COS 优先，坏了回退内置。缓存 60 秒。
func ResolveRefinePrompt(ctx context.Context, store RefineConfigStore) (tpl, version, source string) {
	refinePromptCache.Lock()
	if time.Since(refinePromptCache.at) < refinePromptTTL && refinePromptCache.tpl != "" {
		t, v, src := refinePromptCache.tpl, refinePromptCache.version, refinePromptCache.source
		refinePromptCache.Unlock()
		return t, v, src
	}
	refinePromptCache.Unlock()

	tpl, source = ChooseRefinePrompt(store.DownloadRefinePrompt(ctx))
	version = RefinePromptVersion(tpl)

	refinePromptCache.Lock()
	refinePromptCache.at, refinePromptCache.tpl = time.Now(), tpl
	refinePromptCache.version, refinePromptCache.source = version, source
	refinePromptCache.Unlock()
	return tpl, version, source
}
