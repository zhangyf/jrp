package listening

// 精校旋钮 + 切分策略（2026-11-06 老师提的两点）。
//
// ① **配置外置**：批次上限、超时、模型这些不该硬编码 —— 改它要重新编译部署太重。
//    照 judge-prompt / plan-config 同一套：COS 上放 configs/refine_config.json，
//    读不到 / 配坏 / 越界一律回退内置默认，精校绝不因为一份坏配置卡住。
//
// ② **按题目切，不按句数切**：原来 40（后来 12）句一批是错的。
//    一道 JLPT 听力题从「一番」开始，题干 → 会话 → 質問 → 選択肢 是**完整语境**，
//    按固定句数硬切会把同一题劈成两段，模型看不到题目全貌：
//    - 碎片句（ASR 把「聴解」切成「懲戒ベルゴ」）离开题目上下文就没法推断；
//    - 番号、选项这些前后关联的信息也被切断。
//    所以默认按「大题号（問題一）/小题号（一番）」切段，一段 = 一道题。
//    万一识别不出题号（非 JLPT 素材），再退回「每 max_cues_per_part 句一段」兜底。

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxCuesPerPart 一段最多多少句。题目切分正常工作时用不到它；
	// 只有识别不出题号、或某道题特别长时才兜底硬切，防止一段大到一个请求跑不完。
	DefaultMaxCuesPerPart = 60
	// DefaultTimeoutSeconds 单次模型调用超时（秒）。实测 15 句约 42s，
	// 一道题通常 10-40 句，150s 留足余量。
	DefaultTimeoutSeconds = 150
)

// RefineConfig 精校旋钮。零值/缺省字段由 NormalizeRefineConfig 补默认。
type RefineConfig struct {
	// ByQuestion 按题目切分（默认 true）。关掉就退回按 MaxCuesPerPart 硬切。
	ByQuestion *bool `json:"by_question,omitempty"`
	// MaxCuesPerPart 单段句数上限（默认 60）。
	MaxCuesPerPart int `json:"max_cues_per_part,omitempty"`
	// TimeoutSeconds 单次模型调用超时（默认 150）。
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Model / Fallback 覆盖现网文本链（留空就用 JRP_LLM_MODEL + 备胎）。
	Model    string `json:"model,omitempty"`
	Fallback string `json:"fallback,omitempty"`
}

// BuiltinRefineConfig 内置默认旋钮。
func BuiltinRefineConfig() RefineConfig {
	byQ := true
	return RefineConfig{
		ByQuestion:     &byQ,
		MaxCuesPerPart: DefaultMaxCuesPerPart,
		TimeoutSeconds: DefaultTimeoutSeconds,
	}
}

// ParseRefineConfig 解析配置。空内容返回零值（= 没配），其余交给 Choose 判好坏。
func ParseRefineConfig(data []byte) (RefineConfig, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return RefineConfig{}, nil
	}
	var c RefineConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return RefineConfig{}, err
	}
	return c, nil
}

// NormalizeRefineConfig 把缺省/越界字段补成默认值。永远不会返回坏配置。
//
// 越界时不是报错而是夹到合法区间 —— 精校是后台任务，为了一份配置写错就整批失败不值得。
func NormalizeRefineConfig(c RefineConfig) RefineConfig {
	out := BuiltinRefineConfig()
	if c.ByQuestion != nil {
		v := *c.ByQuestion
		out.ByQuestion = &v
	}
	if c.MaxCuesPerPart > 0 {
		out.MaxCuesPerPart = clampInt(c.MaxCuesPerPart, 1, 200)
	}
	if c.TimeoutSeconds > 0 {
		out.TimeoutSeconds = clampInt(c.TimeoutSeconds, 30, 600)
	}
	out.Model = strings.TrimSpace(c.Model)
	out.Fallback = strings.TrimSpace(c.Fallback)
	return out
}

// ValidateRefineConfig 写入前校验。CLI 上传/--set 用，宁可拒绝也不写一份坏配置。
func ValidateRefineConfig(c RefineConfig) error {
	if c.MaxCuesPerPart != 0 && (c.MaxCuesPerPart < 1 || c.MaxCuesPerPart > 200) {
		return fmt.Errorf("max_cues_per_part 应在 1-200 之间（当前 %d）", c.MaxCuesPerPart)
	}
	if c.TimeoutSeconds != 0 && (c.TimeoutSeconds < 30 || c.TimeoutSeconds > 600) {
		return fmt.Errorf("timeout_seconds 应在 30-600 之间（当前 %d）", c.TimeoutSeconds)
	}
	return nil
}

// ChooseRefineConfig 在 COS 上的配置和内置默认之间选。读不到/解析坏/校验不过 → 内置。
func ChooseRefineConfig(data []byte, err error) (RefineConfig, string) {
	builtin := BuiltinRefineConfig()
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return builtin, "builtin"
	}
	c, perr := ParseRefineConfig(data)
	if perr != nil {
		log.Printf("refine-config: COS 配置解析失败（%v），回退内置", perr)
		return builtin, "builtin"
	}
	if verr := ValidateRefineConfig(c); verr != nil {
		log.Printf("refine-config: COS 配置越界（%v），回退内置", verr)
		return builtin, "builtin"
	}
	return NormalizeRefineConfig(c), "cos"
}

// RefineSettingsStore 读 COS 上的精校旋钮。实现方是根包 *Storage 的 DownloadRefineConfig。
//
// 名字跟 refine.go 的 RefineConfigStore（读 prompt 模板）区分开：那个管「怎么改」，
// 这个管「切多细 / 等多久 / 用哪个模型」。
type RefineSettingsStore interface {
	DownloadRefineConfig(ctx context.Context) ([]byte, error)
}

const refineConfigTTL = 60 * time.Second

var refineConfigCache = struct {
	sync.Mutex
	at  time.Time
	cfg RefineConfig
	src string
}{}

// ResolveRefineConfig 拿当前生效的旋钮：COS 优先，坏了回退内置，缓存 60 秒。
func ResolveRefineConfig(ctx context.Context, store RefineSettingsStore) (RefineConfig, string) {
	refineConfigCache.Lock()
	if time.Since(refineConfigCache.at) < refineConfigTTL && refineConfigCache.src != "" {
		c, s := refineConfigCache.cfg, refineConfigCache.src
		refineConfigCache.Unlock()
		return c, s
	}
	refineConfigCache.Unlock()

	cfg, src := ChooseRefineConfig(store.DownloadRefineConfig(ctx))

	refineConfigCache.Lock()
	refineConfigCache.at, refineConfigCache.cfg, refineConfigCache.src = time.Now(), cfg, src
	refineConfigCache.Unlock()
	return cfg, src
}

// ---- 按题目切分 ----

// RefinePart 一段精校单元：cues[Start:End]，一段 = 一道题（识别不出题号时是固定句数）。
type RefinePart struct {
	Start int
	End   int
	Label string
}

var (
	// 大题号：「問題一」「問題１」「問題 2」。要求「問題」后面紧跟数字/汉字数字，
	// 避免把「問題用紙を配ります」「問題要旨」当成题号（那两句后面没有数字）。
	partDaiRe = regexp.MustCompile(`^問題\s*[一二三四五六七八九十0-9０-９]+`)
	// 小题号：「一番」「二番」「３番」。也覆盖 ASR 常见的「ばん」「バン」写法。
	partBanRe = regexp.MustCompile(`^[一二三四五六七八九十0-9０-９]+\s*(番|ばん|バン)`)
)

// IsQuestionStart 判断这句是不是一道新题的开头。
func IsQuestionStart(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	return partDaiRe.MatchString(t) || partBanRe.MatchString(t)
}

// SplitParts 按题目切段。
//
// by_question=true（默认）：遇到大题号/小题号就开新段，一段 = 一道题；
// 单段超过 maxCues 仍然硬切（某道题特别长，一段塞太多模型跑不完）。
// by_question=false 或整份字幕识别不出题号：退回每 maxCues 句一段。
func SplitParts(cues []Cue, cfg RefineConfig) []RefinePart {
	if len(cues) == 0 {
		return nil
	}
	cfg = NormalizeRefineConfig(cfg)
	maxCues := cfg.MaxCuesPerPart
	if maxCues <= 0 {
		maxCues = DefaultMaxCuesPerPart
	}
	byQuestion := cfg.ByQuestion != nil && *cfg.ByQuestion

	var parts []RefinePart
	start := 0
	for i := 1; i < len(cues); i++ {
		// 先判题目边界：新题目从这句开始，前一段到 i 为止。
		if byQuestion && IsQuestionStart(cues[i].Text) && i > start {
			parts = append(parts, newPart(cues, start, i))
			start = i
			continue
		}
		// 再判长度上限：题目识别不出来 / 单题过长时的兜底。
		if i-start >= maxCues {
			parts = append(parts, newPart(cues, start, i))
			start = i
		}
	}
	parts = append(parts, newPart(cues, start, len(cues)))
	return parts
}

func newPart(cues []Cue, start, end int) RefinePart {
	label := ""
	for i := start; i < end && i < len(cues); i++ {
		if t := strings.TrimSpace(cues[i].Text); t != "" {
			label = truncateRunes(t, 14)
			break
		}
	}
	return RefinePart{Start: start, End: end, Label: label}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
