package lexicon

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zhangyf/jrp/internal/archive"
	"github.com/zhangyf/jrp/internal/review"
	"github.com/zhangyf/jrp/internal/wordmeta"
)

func TestSplitLexiconWord(t *testing.T) {
	cases := []struct {
		in                string
		kana, kanji, note string
	}{
		{"みず(水)", "みず", "水", ""},
		{"  はし(橋)  ", "はし", "橋", ""},
		{"さん（敬称）", "さん", "", "敬称"},
		{"たべます", "たべます", "", ""},
		{"  たべる  ", "たべる", "", ""},
	}
	for _, c := range cases {
		kana, kanji, note := SplitLexiconWord(c.in)
		if kana != c.kana || kanji != c.kanji || note != c.note {
			t.Errorf("SplitLexiconWord(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.in, kana, kanji, note, c.kana, c.kanji, c.note)
		}
	}
}

// DTO 随迁后 /api/lexicon 的响应字节不能变。这里把字段顺序和 json tag
// 钉死成一份期望 JSON —— 改 struct 会立刻在这里红。
func TestLexiconItemJSONBytes(t *testing.T) {
	item := LexiconItem{
		Number: 1, Word: "みず(水)", Kana: "みず", Kanji: "水", Note: "",
		WordNote: "跟お湯区分", Def: "水", Pos: "名词", Sub: "",
		Status: "🟢已掌握", Group: "第1课", Circle: "三圆", Hold: false,
		Reviews: 5, Errors: 1, Last: "11/02",
	}
	got, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"number":1,"word":"みず(水)","kana":"みず","kanji":"水","note":"","word_note":"跟お湯区分","def":"水","pos":"名词","sub":"","status":"🟢已掌握","group":"第1课","circle":"三圆","hold":false,"reviews":5,"errors":1,"last":"11/02"}`
	if string(got) != want {
		t.Fatalf("LexiconItem JSON 字节变了:\ngot  %s\nwant %s", got, want)
	}
}

func TestBuildLexicon(t *testing.T) {
	arc := &archive.Archive{
		Language: "ja",
		Groups: []review.WordGroup{
			{Title: "第2课", Words: []review.Word{
				{Word: "はし(橋)", Definition: "桥", Group: "第2课", Status: "🟡基本掌握", ReviewCount: 6, ErrorCount: 1, LastReview: "11/01"},
			}},
			{Title: "第1课", Words: []review.Word{
				{Word: "みず(水)", Definition: "水", Group: "第1课"},
			}},
		},
	}
	meta := &wordmeta.WordMeta{Items: map[string]wordmeta.WordPOS{
		"はし(橋)": {Pos: "名词", Note: "跟箸区分", Circle: "二圆"},
	}}

	items, summary, groups := BuildLexicon(arc, meta)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Number != 1 || items[0].Word != "はし(橋)" ||
		items[0].Kana != "はし" || items[0].Kanji != "橋" ||
		items[0].WordNote != "跟箸区分" || items[0].Pos != "名词" ||
		items[0].Circle != "二圆" || items[0].Reviews != 6 ||
		items[0].Errors != 1 || items[0].Last != "11/01" {
		t.Errorf("第 1 行拼装不符: %+v", items[0])
	}
	if items[1].Pos != "" || items[1].Word != "みず(水)" || items[1].Kanji != "水" {
		t.Errorf("第 2 行拼装不符: %+v", items[1])
	}
	if !reflect.DeepEqual(summary, map[string]int{"名词": 1, "未标注": 1}) {
		t.Errorf("posSummary = %+v", summary)
	}
	if !reflect.DeepEqual(groups, []string{"第1课", "第2课"}) {
		t.Errorf("groups 应按字典序 = %+v", groups)
	}
}
