// Package listening 解析 SRT 字幕为带时间轴的分句数组，供小程序听力模块
// 做「播放到哪句就高亮哪句、点哪句就跳到哪句」。
//
// 纯函数、不碰网络与对象存储 —— 输入 SRT 文本，输出结构化字幕，方便单测。
package listening

import (
	"strconv"
	"strings"
)

// Cue 是一条字幕：起止秒数 + 文本。
//
// 时间用 float64 秒（不是 "00:00:04,020" 这种字符串），前端 onTimeUpdate
// 拿到的 currentTime 就是秒，直接比大小即可，不用再解析一遍。
type Cue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// ParseSRT 把 SRT 文本解析成字幕数组。
//
// SRT 块结构（块之间用空行分隔）：
//
//	1
//	00:00:04,020 --> 00:00:07,140
//	日本語能力試験。
//
// 容错取向：跳过解析不了时间轴的块（不整体报错），空文本块丢弃 —— 真实字幕
// 常有编号错乱、时间轴前后多一个空格、块内多行文本等情况，别因为一条脏数据
// 让整个文件解析失败。
func ParseSRT(src string) []Cue {
	// 统一换行，去掉 BOM。
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	blocks := strings.Split(src, "\n\n")

	cues := make([]Cue, 0, 64)
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		// 找到含 "-->" 的那一行当时间轴；它前面的行（编号）忽略，后面的行拼成文本。
		ti := -1
		for i, ln := range lines {
			if strings.Contains(ln, "-->") {
				ti = i
				break
			}
		}
		if ti < 0 || ti+1 >= len(lines) {
			continue
		}
		start, end, ok := parseTimecode(lines[ti])
		if !ok {
			continue
		}
		text := strings.TrimSpace(strings.Join(lines[ti+1:], "\n"))
		if text == "" {
			continue
		}
		cues = append(cues, Cue{Start: start, End: end, Text: text})
	}
	return cues
}

// parseTimecode 解析 "00:00:04,020 --> 00:00:07,140"（可能带坐标后缀，忽略）。
func parseTimecode(line string) (float64, float64, bool) {
	parts := strings.SplitN(line, "-->", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, ok := parseClock(strings.TrimSpace(parts[0]))
	if !ok {
		return 0, 0, false
	}
	// 结尾部分可能带 "  X1:100" 之类的坐标，取第一个空白前的字段。
	endPart := strings.TrimSpace(parts[1])
	if i := strings.IndexAny(endPart, " \t"); i >= 0 {
		endPart = endPart[:i]
	}
	end, ok := parseClock(endPart)
	if !ok {
		return 0, 0, false
	}
	return start, end, true
}

// parseClock 解析 "HH:MM:SS,mmm" 或 "HH:MM:SS.mmm" 为秒。
func parseClock(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	// 统一把毫秒分隔符 , 换成 .，方便切分。
	s = strings.Replace(s, ",", ".", 1)
	dot := strings.LastIndex(s, ".")
	var main, frac string
	if dot >= 0 {
		main, frac = s[:dot], s[dot+1:]
	} else {
		main, frac = s, ""
	}
	seg := strings.Split(main, ":")
	if len(seg) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(seg[0])
	m, err2 := strconv.Atoi(seg[1])
	sec, err3 := strconv.Atoi(seg[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	total := float64(h)*3600 + float64(m)*60 + float64(sec)
	if frac != "" {
		if f, err := strconv.ParseFloat("0."+frac, 64); err == nil {
			total += f
		}
	}
	return total, true
}
