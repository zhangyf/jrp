package listening

import (
	"math"
	"testing"
)

func TestParseSRTBasic(t *testing.T) {
	src := "1\n00:00:04,020 --> 00:00:07,140\n日本語能力試験。\n\n2\n00:00:07,140 --> 00:00:10,340\n懲戒ベルゴ。\n"
	cues := ParseSRT(src)
	if len(cues) != 2 {
		t.Fatalf("want 2 cues, got %d: %+v", len(cues), cues)
	}
	if cues[0].Start != 4.02 || cues[0].End != 7.14 {
		t.Errorf("cue0 time wrong: %+v", cues[0])
	}
	if cues[0].Text != "日本語能力試験。" {
		t.Errorf("cue0 text wrong: %q", cues[0].Text)
	}
	if cues[1].Text != "懲戒ベルゴ。" {
		t.Errorf("cue1 text wrong: %q", cues[1].Text)
	}
}

func TestParseSRTMultiLineText(t *testing.T) {
	src := "1\n00:00:01,000 --> 00:00:03,000\n第一行\n第二行\n"
	cues := ParseSRT(src)
	if len(cues) != 1 {
		t.Fatalf("want 1 cue, got %d", len(cues))
	}
	if cues[0].Text != "第一行\n第二行" {
		t.Errorf("multiline text wrong: %q", cues[0].Text)
	}
}

func TestParseSRTSkipsBadBlocks(t *testing.T) {
	// 块1正常；块2缺时间轴；块3时间轴解析不了；块4空文本。只应留下 1 条。
	src := "1\n00:00:01,000 --> 00:00:02,000\n正しい。\n\n" +
		"2\n没有时间轴的块\n\n" +
		"3\n00:00:xx --> 00:00:yy\n坏时间\n\n" +
		"4\n00:00:05,000 --> 00:00:06,000\n\n"
	cues := ParseSRT(src)
	if len(cues) != 1 || cues[0].Text != "正しい。" {
		t.Fatalf("should keep only the valid cue, got %+v", cues)
	}
}

func TestParseSRTCRLFAndBOM(t *testing.T) {
	src := "\ufeff1\r\n00:00:01,500 --> 00:00:02,500\r\nテスト\r\n"
	cues := ParseSRT(src)
	if len(cues) != 1 {
		t.Fatalf("want 1 cue, got %d", len(cues))
	}
	if math.Abs(cues[0].Start-1.5) > 1e-9 {
		t.Errorf("start wrong: %v", cues[0].Start)
	}
}

func TestParseSRTCoordinatesSuffix(t *testing.T) {
	// 结尾时间轴后面带坐标后缀，应被忽略。
	src := "1\n00:00:01,000 --> 00:00:02,000 X1:100 X2:200\n座標付き\n"
	cues := ParseSRT(src)
	if len(cues) != 1 || cues[0].End != 2.0 {
		t.Fatalf("should parse with coord suffix: %+v", cues)
	}
}

func TestParseSRTEmpty(t *testing.T) {
	cues := ParseSRT("")
	if len(cues) != 0 {
		t.Fatalf("want 0 cues, got %d", len(cues))
	}
}
