package main

import (
	"testing"
	"time"
)

func TestCircleGroupOf(t *testing.T) {
	cases := []struct {
		in   CircleItem
		want string
	}{
		{CircleItem{Lesson: 21}, "第21课 手册补录"},
		{CircleItem{Lesson: 8}, "第8课 手册补录"},
		{CircleItem{Lesson: 0}, "未分组"},
		{CircleItem{Lesson: 5, Group: "自定义组"}, "自定义组"},
	}
	for _, c := range cases {
		if got := circleGroupOf(c.in); got != c.want {
			t.Errorf("circleGroupOf(%+v) = %q, want %q", c.in, got, c.want)
		}
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
	}
	for _, c := range cases {
		if got := lessonOfGroup(c.title); got != c.want {
			t.Errorf("lessonOfGroup(%q) = %d, want %d", c.title, got, c.want)
		}
	}
}

func TestHoldSet(t *testing.T) {
	// meta 为 nil → 一个都不排除（fail-open：读不到配置不能把计划清空）
	if got := holdSet(nil); got != nil {
		t.Errorf("holdSet(nil) = %v, want nil", got)
	}
	// 没有 hold → 也是 nil
	m := &WordMeta{Items: map[string]WordPOS{"あ": {Pos: "名词"}}}
	if got := holdSet(m); got != nil {
		t.Errorf("holdSet(no hold) = %v, want nil", got)
	}
	// 有 hold → 只收 hold 的
	m.Items["ほーるど"] = WordPOS{Hold: true}
	got := holdSet(m)
	if !got["ほーるど"] {
		t.Error("holdSet 应该包含 hold 的词")
	}
	if got["あ"] {
		t.Error("holdSet 不该包含没挂起的词")
	}
}

// BuildDuePlan 必须跳过挂起的词 —— 这是「补录的 21–24 课不涌进每日练习」的守门测试。
func TestBuildDuePlanSkipsHold(t *testing.T) {
	arc := &Archive{
		Language: "ja",
		Groups: []WordGroup{
			{Title: "第1课 生词表", Words: []Word{
				{Word: "みず(水)", Definition: "水", Group: "第1课 生词表"},
			}},
			{Title: "第21课 手册补录", Words: []Word{
				{Word: "わたす(渡す)", Definition: "交给", Group: "第21课 手册补录"},
			}},
		},
	}
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)

	// 不传 hold：两个词都在（ReviewCount=0 → 待测试 → IsDue）
	plan := BuildDuePlan(arc, "ja", today, nil)
	if len(plan.Words) != 2 {
		t.Fatalf("不带 hold 时应出 2 词，实际 %d", len(plan.Words))
	}

	// 传 hold：只剩没挂起的那个
	plan = BuildDuePlan(arc, "ja", today, map[string]bool{"わたす(渡す)": true})
	if len(plan.Words) != 1 {
		t.Fatalf("带 hold 时应出 1 词，实际 %d", len(plan.Words))
	}
	if plan.Words[0].Word != "みず(水)" {
		t.Errorf("不该把挂起的词出出来，实际 %q", plan.Words[0].Word)
	}
}

func TestArchiveAppendAndSetDefinition(t *testing.T) {
	arc := &Archive{Language: "ja"}

	if created := arc.AppendWord("第9课 手册补录", Word{Word: "え(絵)", Definition: "画儿"}); !created {
		t.Error("第一次追加应该新建组")
	}
	if created := arc.AppendWord("第9课 手册补录", Word{Word: "せ(背)", Definition: "个子"}); created {
		t.Error("第二次追加不该新建组")
	}
	if got := len(arc.Groups); got != 1 {
		t.Fatalf("分组数 = %d, want 1", got)
	}
	if got := arc.Groups[0].Words[1].Group; got != "第9课 手册补录" {
		t.Errorf("词的 Group 没回填：%q", got)
	}
	// 空分组名兜底
	arc.AppendWord("   ", Word{Word: "x", Definition: "y"})
	if arc.Groups[len(arc.Groups)-1].Title != "未分组" {
		t.Errorf("空分组名应落「未分组」，实际 %q", arc.Groups[len(arc.Groups)-1].Title)
	}

	if !arc.SetDefinition("え(絵)", "画儿（绘画作品）") {
		t.Error("SetDefinition 应该找到词")
	}
	if arc.Groups[0].Words[0].Definition != "画儿（绘画作品）" {
		t.Errorf("释义没改：%q", arc.Groups[0].Words[0].Definition)
	}
	if arc.SetDefinition("そんな言葉はない", "x") {
		t.Error("不存在的词不该改成功")
	}
}
