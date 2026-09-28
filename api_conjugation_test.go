package main

import "testing"

// 活用规则的回归测试。每条用例都对应一类变形陷阱，
// 改活用逻辑前先想清楚会不会把这些打回原形。

func TestConjVerb(t *testing.T) {
	cases := []struct {
		name       string
		kana, kanj string
		sub        string
		wantBase   string
		wantTe     string
	}{
		// 动1：五段，い段→う段 + て形音便
		{"五段か行", "かきます", "書きます", "1", "かく(書く)", "かいて(書いて)"},
		{"五段つ行", "もちます", "持ちます", "1", "もつ(持つ)", "もって(持って)"},
		{"五段す行", "はなします", "話します", "1", "はなす(話す)", "はなして(話して)"},
		{"五段く行", "あるきます", "歩きます", "1", "あるく(歩く)", "あるいて(歩いて)"},
		{"五段ぐ行", "いそぎます", "急ぎます", "1", "いそぐ(急ぐ)", "いそいで(急いで)"},
		{"五段ぶ行", "あそびます", "遊びます", "1", "あそぶ(遊ぶ)", "あそんで(遊んで)"},
		{"五段む行", "のみます", "飲みます", "1", "のむ(飲む)", "のんで(飲んで)"},
		{"五段う行", "かいます", "買います", "1", "かう(買う)", "かって(買って)"},
		{"五段る行", "しります", "知ります", "1", "しる(知る)", "しって(知って)"},
		{"行く例外", "いきます", "行きます", "1", "いく(行く)", "いって(行って)"},
		// 动2：一段
		{"一段", "かたづけます", "片づけます", "2", "かたづける(片づける)", "かたづけて(片づけて)"},
		{"一段い干", "みます", "見ます", "2", "みる(見る)", "みて(見て)"},
		// 动3
		{"サ变复合", "れんしゅうします", "練習します", "3", "れんしゅうする(練習する)", "れんしゅうして(練習して)"},
		{"裸します", "します", "", "3", "する", "して"},
		{"カ变来る", "きます", "来ます", "3", "くる(来る)", "きて(来て)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, te, ok := conjVerb(c.kana, c.kanj, c.sub)
			if !ok {
				t.Fatalf("conjVerb(%s) 返回 !ok", c.kana)
			}
			if base != c.wantBase {
				t.Errorf("base = %q, want %q", base, c.wantBase)
			}
			if te != c.wantTe {
				t.Errorf("te = %q, want %q", te, c.wantTe)
			}
		})
	}

	// 算不出的要稳稳返回 !ok，不能给错答案
	if _, _, ok := conjVerb("すし", "", "1"); ok {
		t.Error("非动词词形应当 !ok")
	}
	// 反向保护：い段结尾的普通五段（かいます→かう）不能被特殊五段规则误伤
	if base, te, ok := conjVerb("かいます", "買います", "1"); !ok || base != "かう(買う)" || te != "かって(買って)" {
		t.Errorf("かいます 被误判：base=%q te=%q ok=%v", base, te, ok)
	}

	// 特殊五段（尊敬语）：くださる/なさる/いらっしゃる/おっしゃる/ござる 的ます形
	// 词干是 い段→う段算不出来的（くださ+います→くださう✗），必须 !ok 跳过
	for _, w := range []string{"くださいます", "なさいます", "いらっしゃいます", "おっしゃいます", "ございます"} {
		if _, _, ok := conjVerb(w, w, "1"); ok {
			t.Errorf("特殊五段 %s 应当 !ok（普通规则会算出错形）", w)
		}
	}
}

func TestConjAdjI(t *testing.T) {
	cases := []struct {
		name          string
		kana, kanj    string
		wantPast      string
		wantNeg       string
		wantPastNeg   string
	}{
		{"普通い形", "あかるい", "明るい", "あかるかったです(明るかったです)", "あかるくないです(明るくないです)", "あかるくなかったです(明るくなかったです)"},
		{"いい特例", "いい", "いい", "よかったです", "よくないです", "よくなかったです"},
		{"かっこいい", "かっこいい", "格好いい", "かっこよかったです(格好よかったです)", "かっこよくないです(格好よくないです)", "かっこよくなかったです(格好よくなかったです)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			past, neg, pastneg, ok := conjAdjI(c.kana, c.kanj)
			if !ok {
				t.Fatalf("conjAdjI(%s) 返回 !ok", c.kana)
			}
			if past != c.wantPast {
				t.Errorf("past = %q, want %q", past, c.wantPast)
			}
			if neg != c.wantNeg {
				t.Errorf("neg = %q, want %q", neg, c.wantNeg)
			}
			if pastneg != c.wantPastNeg {
				t.Errorf("pastneg = %q, want %q", pastneg, c.wantPastNeg)
			}
		})
	}
	// 注：きれい 这类「以 い 结尾的ナ形」由 handler 按 word_meta 的 pos.Sub 路由，
	// conjAdjI 本身无法从词形判断词性，这里不做断言（曾经误解成 !ok，是错的）。
}

func TestConjAdjNa(t *testing.T) {
	past, neg, pastneg, ok := conjAdjNa("あんぜん", "安全")
	if !ok {
		t.Fatal("conjAdjNa 返回 !ok")
	}
	if past != "あんぜんでした(安全でした)" {
		t.Errorf("past = %q", past)
	}
	// 否定 4 写法都得在，且都带汉字侧
	for _, want := range []string{
		"あんぜんじゃありません(安全じゃありません)",
		"あんぜんではありません(安全ではありません)",
		"あんぜんじゃないです(安全じゃないです)",
		"あんぜんではないです(安全ではないです)",
	} {
		if !containsVariant(neg, want) {
			t.Errorf("neg 缺少写法 %q，实际 %q", want, neg)
		}
	}
	if !containsVariant(pastneg, "あんぜんじゃありませんでした(安全じゃありませんでした)") {
		t.Errorf("pastneg 缺少标准写法，实际 %q", pastneg)
	}
}

// containsVariant 按多写法串的「/」分隔逐段比对。
func containsVariant(multi, want string) bool {
	for _, p := range splitVariants(multi) {
		if p == want {
			return true
		}
	}
	return false
}

func splitVariants(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// 词性表的两种写法都要认：中文「一类/二类/三类」「い形/な形」与数字 1/2/3。
// 只认数字会让词池从 111 个动词缩到 6 个 —— 这条测试就是防这个回归。
func TestConjClassMapping(t *testing.T) {
	verbs := []struct {
		pos, sub, want string
	}{
		{"动词", "一类", "1"}, {"动词", "二类", "2"}, {"动词", "三类", "3"},
		{"动", "1", "1"}, {"动", "2", "2"}, {"动", "3", "3"},
		{"名词", "一类", ""}, {"动词", "", ""}, {"动词", "五类", ""},
	}
	for _, c := range verbs {
		if got := conjVerbClass(c.pos, c.sub); got != c.want {
			t.Errorf("conjVerbClass(%q,%q) = %q, want %q", c.pos, c.sub, got, c.want)
		}
	}
	adjs := []struct {
		pos, sub, want string
	}{
		{"形容词", "い形", "i"}, {"形容词", "な形", "na"},
		{"形", "1", "i"}, {"形", "2", "na"},
		{"名词", "い形", ""}, {"形容词", "", ""}, {"形容词", "3", ""},
	}
	for _, c := range adjs {
		if got := conjAdjClass(c.pos, c.sub); got != c.want {
			t.Errorf("conjAdjClass(%q,%q) = %q, want %q", c.pos, c.sub, got, c.want)
		}
	}
}

func TestConjSkipWord(t *testing.T) {
	if !conjSkipWord("にすんでいます", "にすんでいます") {
		t.Error("〜ています 的活用形应当跳过（不是辞书形）")
	}
	if !conjSkipWord("いってきます", "いってきます") {
		t.Error("寒暄固定说法应当跳过")
	}
	if conjSkipWord("すみます(住みます)", "すみます") {
		t.Error("普通动词不该被跳过")
	}
	if conjSkipWord("あります", "あります") {
		t.Error("あります 不该被跳过")
	}
}
