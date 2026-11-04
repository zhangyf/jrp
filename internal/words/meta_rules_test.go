package words

import (
	"reflect"
	"testing"
)

func TestCircleGroupOf(t *testing.T) {
	cases := []struct {
		name string
		in   CircleItem
		want string
	}{
		{"课号决定分组", CircleItem{Lesson: 21}, "第21课 手册补录"},
		{"第8课", CircleItem{Lesson: 8}, "第8课 手册补录"},
		{"课号0落未分组", CircleItem{Lesson: 0}, "未分组"},
		{"显式组优先于课号", CircleItem{Lesson: 5, Group: "自定义组"}, "自定义组"},
		{"显式组去首尾空白", CircleItem{Lesson: 5, Group: "  自定义组  "}, "自定义组"},
		{"组名全空白视为没给", CircleItem{Lesson: 7, Group: "   "}, "第7课 手册补录"},
		{"负课号落未分组", CircleItem{Lesson: -1}, "未分组"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CircleGroupOf(c.in); got != c.want {
				t.Errorf("CircleGroupOf(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLessonOfGroup(t *testing.T) {
	cases := []struct {
		title string
		want  int
	}{
		{"第21课 手册补录", 21},
		{"📖 第1课01 基础词（5/25）", 1},
		{"第19课 生词表（10/13）", 19},
		{"追加词汇（8/7）", 0},
		{"天气/季节词（7/13）", 0},
		{"第课", 0},
		{"第 12 课 中间有空格", 12},
		{"第abc课 不是数字", 0},
		{"没有第字 3课", 0},
		{"第8册 没有课字", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := LessonOfGroup(c.title); got != c.want {
			t.Errorf("LessonOfGroup(%q) = %d, want %d", c.title, got, c.want)
		}
	}
}

func metaWith(items map[string]WordPOS) *WordMeta {
	return &WordMeta{Items: items}
}

func TestCircleMap(t *testing.T) {
	t.Run("nil 表返回 nil", func(t *testing.T) {
		if got := CircleMap(nil); got != nil {
			t.Errorf("CircleMap(nil) = %v, want nil", got)
		}
	})
	t.Run("空表返回 nil", func(t *testing.T) {
		if got := CircleMap(metaWith(nil)); got != nil {
			t.Errorf("CircleMap(空) = %v, want nil", got)
		}
	})
	t.Run("有条目但全没标圆级返回 nil", func(t *testing.T) {
		m := metaWith(map[string]WordPOS{"あ": {Pos: "名词"}, "い": {Pos: "动词"}})
		if got := CircleMap(m); got != nil {
			t.Errorf("CircleMap(全空圆级) = %v, want nil", got)
		}
	})
	t.Run("圆级带空白也要 trim 后收下", func(t *testing.T) {
		m := metaWith(map[string]WordPOS{
			"あ": {Circle: " 三圆 "},
			"い": {Circle: "二圆"},
			"う": {Pos: "名词"},
		})
		got := CircleMap(m)
		want := map[string]string{"あ": "三圆", "い": "二圆"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CircleMap = %v, want %v", got, want)
		}
	})
	t.Run("圆级全空白视为没标", func(t *testing.T) {
		m := metaWith(map[string]WordPOS{"あ": {Circle: "   "}})
		if got := CircleMap(m); got != nil {
			t.Errorf("CircleMap(全空白圆级) = %v, want nil", got)
		}
	})
}

func TestLookupWordMeta(t *testing.T) {
	m := metaWith(map[string]WordPOS{
		"とおります(通ります)": {Pos: "动词", Sub: "一类", Note: "空间上从某处穿过", Circle: "三圆"},
		"なかなか":        {Pos: "副词", Note: "程度副词", Circle: "二圆"},
		"おふろ(お風呂)":    {Pos: "名词"},
	})
	cases := []struct {
		name                   string
		meta                   *WordMeta
		word                   string
		pos, sub, note, circle string
	}{
		{"四项齐全", m, "とおります(通ります)", "动词", "一类", "空间上从某处穿过", "三圆"},
		{"没细分没备注项空的", m, "なかなか", "副词", "", "程度副词", "二圆"},
		{"没备注没圆级", m, "おふろ(お風呂)", "名词", "", "", ""},
		{"未收录的词", m, "あたらしい(新しい)", "", "", "", ""},
		{"裸假名不猜", m, "とおります", "", "", "", ""},
		{"meta 为 nil", nil, "とおります(通ります)", "", "", "", ""},
		{"空表", metaWith(nil), "とおります(通ります)", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pos, sub, note, circle := LookupWordMeta(c.meta, c.word)
			if pos != c.pos || sub != c.sub || note != c.note || circle != c.circle {
				t.Errorf("LookupWordMeta = %q %q %q %q, want %q %q %q %q",
					pos, sub, note, circle, c.pos, c.sub, c.note, c.circle)
			}
		})
	}
}

func TestHoldSet(t *testing.T) {
	t.Run("nil 表 fail-open 返回 nil", func(t *testing.T) {
		if got := HoldSet(nil); got != nil {
			t.Errorf("HoldSet(nil) = %v, want nil", got)
		}
	})
	t.Run("空表返回 nil", func(t *testing.T) {
		if got := HoldSet(metaWith(nil)); got != nil {
			t.Errorf("HoldSet(空) = %v, want nil", got)
		}
	})
	t.Run("没有挂起返回 nil", func(t *testing.T) {
		m := metaWith(map[string]WordPOS{"あ": {Pos: "名词"}})
		if got := HoldSet(m); got != nil {
			t.Errorf("HoldSet(无 hold) = %v, want nil", got)
		}
	})
	t.Run("只收挂起的词", func(t *testing.T) {
		m := metaWith(map[string]WordPOS{
			"あ":   {Pos: "名词"},
			"ほるど": {Hold: true},
			"い":   {Hold: true},
		})
		got := HoldSet(m)
		want := map[string]bool{"ほるど": true, "い": true}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("HoldSet = %v, want %v", got, want)
		}
	})
}

func TestNearKeys(t *testing.T) {
	items := map[string]WordPOS{
		"とおります(通ります)": {},
		"とおる(通る)":     {},
		"とおい(遠い)":     {},
		"とけい(時計)":     {},
		"としょかん(図書館)":  {},
		"とまと":         {},
		"あさ(朝)":       {},
	}
	cases := []struct {
		name string
		w    string
		want []string
	}{
		{"单字符不查", "と", nil},
		{"空串不查", "", nil},
		{"完整词形去掉括号后只命中自己", "とおります(通ります)", []string{
			"とおります(通ります)"}},
		{"两字符命中并排序", "とお", []string{
			"とおい(遠い)", "とおります(通ります)", "とおる(通る)"}},
		{"无命中", "zzz", nil},
		{"首尾空白先 trim", "  とお  ", []string{
			"とおい(遠い)", "とおります(通ります)", "とおる(通る)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NearKeys(items, c.w)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("NearKeys(%q) = %v, want %v", c.w, got, c.want)
			}
		})
	}
	t.Run("超过 5 个只留排序后前 5", func(t *testing.T) {
		big := map[string]WordPOS{}
		for _, k := range []string{"ああ1", "ああ2", "ああ3", "ああ4", "ああ5", "ああ6", "ああ7"} {
			big[k] = WordPOS{}
		}
		got := NearKeys(big, "ああ")
		if len(got) != 5 {
			t.Fatalf("NearKeys 应截断到 5 个，实际 %d: %v", len(got), got)
		}
		want := []string{"ああ1", "ああ2", "ああ3", "ああ4", "ああ5"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("NearKeys 截断 = %v, want %v", got, want)
		}
	})
	t.Run("空 map", func(t *testing.T) {
		if got := NearKeys(nil, "ああ"); got != nil {
			t.Errorf("NearKeys(nil) = %v, want nil", got)
		}
	})
}
