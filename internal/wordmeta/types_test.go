package wordmeta

import (
	"encoding/json"
	"strings"
	"testing"
)

// JSON 序列化形态是「行为不变」的硬锚点：word_meta.json / circle 输入输出的
// tag、omitempty 全都不能因为搬包而变。以下用手写的期望串固定住。
func TestWordMetaJSONShape(t *testing.T) {
	m := WordMeta{Version: 2, Updated: "2026-11-04", Note: "n", Items: map[string]WordPOS{
		"あ": {Pos: "名词"}, // Sub/Note/Circle/Hold 全零值
	}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// 顶层 4 个字段、词条 pos/sub 恒出现（这两个 tag 没有 omitempty）。
	for _, want := range []string{
		`"version":2`, `"updated":"2026-11-04"`, `"note":"n"`, `"items"`,
		`"あ"`, `"pos":"名词"`, `"sub":""`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("WordMeta JSON 缺 %s：%s", want, s)
		}
	}
	// note/circle/hold 带 omitempty，零值不该出现。
	for _, bad := range []string{`"note":""`, `"circle"`, `"hold"`} {
		if strings.Contains(s, bad) {
			t.Errorf("WordPOS 零值字段 %s 不该序列化：%s", bad, s)
		}
	}
}

func TestWordPOSFieldsSerializeWhenSet(t *testing.T) {
	b, _ := json.Marshal(WordPOS{Pos: "动词", Sub: "一类", Note: "备注", Circle: "三圆", Hold: true})
	s := string(b)
	for _, want := range []string{`"pos":"动词"`, `"sub":"一类"`, `"note":"备注"`, `"circle":"三圆"`, `"hold":true`} {
		if !strings.Contains(s, want) {
			t.Errorf("WordPOS JSON 缺 %s：%s", want, s)
		}
	}
}

func TestCircleTypesJSONShape(t *testing.T) {
	in := CircleInput{Language: "ja", Words: []CircleItem{{Word: "あ", Circle: "三圆", Lesson: 21}}}
	b, _ := json.Marshal(in)
	s := string(b)
	for _, want := range []string{`"language":"ja"`, `"words"`, `"word":"あ"`, `"circle":"三圆"`, `"lesson":21`} {
		if !strings.Contains(s, want) {
			t.Errorf("CircleInput JSON 缺 %s：%s", want, s)
		}
	}
	// CircleInput.DefFix 和 CircleItem 的 def/pos/sub/group 都是 omitempty，零值不出现。
	for _, bad := range []string{`"def_fix"`, `"def"`, `"pos"`, `"sub"`, `"group"`} {
		if strings.Contains(s, bad) {
			t.Errorf("CircleInput/CircleItem 零值字段 %s 不该序列化：%s", bad, s)
		}
	}

	out := CircleOutcome{Added: 1, Duplicates: 2, Tagged: 3, TotalWords: 100}
	b, _ = json.Marshal(out)
	s = string(b)
	for _, want := range []string{`"added":1`, `"duplicates":2`, `"tagged":3`, `"total_words":100`, `"old_filename":""`, `"version":""`} {
		if !strings.Contains(s, want) {
			t.Errorf("CircleOutcome JSON 缺 %s：%s", want, s)
		}
	}
	for _, bad := range []string{`"groups"`, `"warnings"`, `"dup_definitions"`, `"dup_core_definitions"`} {
		if strings.Contains(s, bad) {
			t.Errorf("CircleOutcome 空可选字段不该序列化：%s", s)
		}
	}
}

func TestWordMetaJSONRoundTrip(t *testing.T) {
	orig := WordMeta{Version: 3, Updated: "2026-11-04", Note: "备注", Items: map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "穿过", Circle: "三圆", Hold: true},
		"なかなか":        {Pos: "副词"},
	}}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var back WordMeta
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Version != orig.Version || back.Updated != orig.Updated || back.Note != orig.Note {
		t.Errorf("顶层字段 round-trip 丢了：%+v", back)
	}
	p, ok := back.Items["とおります(通ります)"]
	if !ok {
		t.Fatal("词条丢了")
	}
	if p.Pos != "动词" || p.Sub != "一类" || p.Note != "穿过" || p.Circle != "三圆" || !p.Hold {
		t.Errorf("WordPOS round-trip 丢了：%+v", p)
	}
}
