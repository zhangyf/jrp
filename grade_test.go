package main

import "testing"

// 词形取自老师档案里的真实形态（jrp --lang ja stats 的 top_reviewed）。
func TestGradeAnswer(t *testing.T) {
	cases := []struct {
		name  string
		word  string
		input string
		mode  GradeMode
		want  bool
	}{
		// ---- 假名档：只认假名；照抄全形也算对；只写汉字不算对 ----
		{"kana 写假名", "おんがく(音楽)", "おんがく", GradeKana, true},
		{"kana 写全形", "おんがく(音楽)", "おんがく(音楽)", GradeKana, true},
		{"kana 写全形无括号", "おんがく(音楽)", "おんがく音楽", GradeKana, true},
		{"kana 只写汉字不算对", "おんがく(音楽)", "音楽", GradeKana, false},
		{"kana 假名写错", "おんがく(音楽)", "おんかく", GradeKana, false},

		// ---- 汉字档：只认汉字（或全形）----
		{"kanji 写汉字", "おんがく(音楽)", "音楽", GradeKanji, true},
		{"kanji 写全形", "おんがく(音楽)", "おんがく(音楽)", GradeKanji, true},
		{"kanji 只写假名不算对", "おんがく(音楽)", "おんがく", GradeKanji, false},

		// ---- 完整档：假名 + 汉字都要 ----
		{"full 写全形", "おんがく(音楽)", "おんがく(音楽)", GradeFull, true},
		{"full 只写假名", "おんがく(音楽)", "おんがく", GradeFull, false},
		{"full 只写汉字", "おんがく(音楽)", "音楽", GradeFull, false},

		// ---- 任一档：假名或汉字，写哪个都对 ----
		{"either 写假名", "おんがく(音楽)", "おんがく", GradeEither, true},
		{"either 写汉字", "おんがく(音楽)", "音楽", GradeEither, true},
		{"either 写全形", "おんがく(音楽)", "おんがく(音楽)", GradeEither, true},
		{"either 写错", "おんがく(音楽)", "おんかく", GradeEither, false},
		{"either 汉字写错", "おんがく(音楽)", "音楽会", GradeEither, false},
		{"either 混写词写汉字", "ひるごはん(昼ご飯)", "昼ご飯", GradeEither, true},
		{"either 无汉字词退回假名", "はじめまして", "はじめまして", GradeEither, true},
		{"either 片假名折叠仍生效", "カレー", "かれー", GradeEither, true},

		// ---- 混写汉字（汉字部分本身含假名）----
		{"kana 混写词写假名", "ひるごはん(昼ご飯)", "ひるごはん", GradeKana, true},
		{"kanji 混写词写汉字", "ひるごはん(昼ご飯)", "昼ご飯", GradeKanji, true},
		{"kanji 混写词汉字写错", "ひるごはん(昼ご飯)", "昼ごはん", GradeKanji, false},
		{"kana 混写词只写汉字", "ひるごはん(昼ご飯)", "昼ご飯", GradeKana, false},

		// ---- 动词，汉字部分含送假名 ----
		{"kana 动词写假名", "かいます(買います)", "かいます", GradeKana, true},
		{"kanji 动词写汉字", "かいます(買います)", "買います", GradeKanji, true},
		{"full 动词写全形", "ちがいます(違います)", "ちがいます(違います)", GradeFull, true},

		// ---- 纯假名词：汉字档 / 完整档自动退回假名档 ----
		{"无汉字词 kanji 退回", "はじめまして", "はじめまして", GradeKanji, true},
		{"无汉字词 full 退回", "はじめまして", "はじめまして", GradeFull, true},
		{"无汉字词写错", "はじめまして", "はじめました", GradeKana, false},

		// ---- 片假名：假名档允许打成平假名 ----
		{"片假名词写片假名", "カレー", "カレー", GradeKana, true},
		{"片假名词写平假名", "カレー", "かれー", GradeKana, true},
		{"片假名词写错", "カレー", "カレイ", GradeKana, false},

		// ---- 归一化：空白 / 中黑点不敏感 ----
		{"带空格", "てぶくろ(手袋)", " てぶくろ ", GradeKana, true},
		{"带全角空格", "てぶくろ(手袋)", "て　ぶくろ", GradeKana, true},
		{"带中黑点", "てぶくろ(手袋)", "てぶくろ・", GradeKana, true},

		// ---- 多形态 ----
		{"多形态命中第二形态", "いい(良い)/よい(良い)", "よい", GradeKana, true},
		{"多形态命中第一形态", "いい(良い)/よい(良い)", "いい", GradeKana, true},
		{"多形态都不命中", "いい(良い)/よい(良い)", "よろしい", GradeKana, false},

		// ---- 边界 ----
		{"空输入", "おんがく(音楽)", "", GradeKana, false},
		{"纯空格输入", "おんがく(音楽)", "   ", GradeKana, false},
		{"空答案", "", "おんがく", GradeKana, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := GradeAnswer(c.input, c.word, c.mode)
			if got != c.want {
				t.Errorf("GradeAnswer(%q, %q, %s) = %v, want %v",
					c.input, c.word, c.mode, got, c.want)
			}
		})
	}
}

func TestParseGradeMode(t *testing.T) {
	cases := []struct {
		in   string
		want GradeMode
	}{
		{"", GradeKana},
		{"kana", GradeKana},
		{"KANA", GradeKana},
		{"乱七八糟", GradeKana},
		{"kanji", GradeKanji},
		{" Kanji ", GradeKanji},
		{"full", GradeFull},
		{"either", GradeEither},
		{" EITHER ", GradeEither},
	}
	for _, c := range cases {
		if got := ParseGradeMode(c.in); got != c.want {
			t.Errorf("ParseGradeMode(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestParseAnswerForms(t *testing.T) {
	forms := parseAnswerForms("おんがく(音楽)")
	if len(forms) != 1 {
		t.Fatalf("got %d forms, want 1", len(forms))
	}
	if forms[0].Kana != "おんがく" || forms[0].Kanji != "音楽" || forms[0].Full != "おんがく音楽" {
		t.Errorf("unexpected form: %+v", forms[0])
	}

	// 全角括号也要能解析
	forms = parseAnswerForms("おんがく（音楽）")
	if len(forms) != 1 || forms[0].Kanji != "音楽" {
		t.Errorf("full-width paren not parsed: %+v", forms)
	}

	// 无括号
	forms = parseAnswerForms("カレー")
	if len(forms) != 1 || forms[0].Kana != "カレー" || forms[0].Kanji != "" {
		t.Errorf("unexpected form: %+v", forms)
	}

	// 多形态去重
	forms = parseAnswerForms("いい(良い)/いい(良い)")
	if len(forms) != 1 {
		t.Errorf("duplicate forms not deduped: %+v", forms)
	}
}
