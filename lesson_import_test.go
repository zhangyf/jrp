package main

import (
	"testing"

	"github.com/zhangyf/jrp/internal/lessonimport"
	"github.com/zhangyf/jrp/internal/sentence"
)

// save-lesson 自动入库走的就是这两个函数（resolveImportDocs + collectImport），
// 这里用内存文档跑一遍：查重只对"一模一样"生效，重复导不涨条目。
func TestResolveAndCollectImport(t *testing.T) {
	basic := "## 第19课 基本课文\n\n### 基本课文 4 句\n\n| 序号 | 日文 | 中文 |\n|---|---|---|\n" +
		"| 1 | 部屋の かぎを 忘れないで ください。 | 请别忘了房间钥匙。 |\n" +
		"| 2 | あの 人が 吉田課長ですよ。 | 那个人是吉田科长。 |\n"
	applied := "## 第19课 应用课文\n\n| 说话人 | 日文 | 中文 |\n|---|---|---|\n" +
		"| 李 | 小野さん、初めてですから、ちょっと 恐いです。 | 小野女士，我是第一次滑雪，有点害怕。 |\n"
	docs := map[string]string{
		lessonimport.LessonDocName(5, lessonimport.LessonDocKinds[0]): basic,
		lessonimport.LessonDocName(5, lessonimport.LessonDocKinds[1]): applied,
	}
	fetch := func(n string) (string, bool) { c, ok := docs[n]; return c, ok }

	existing := []sentence.BankSentence{
		{ID: "L19B001", Lesson: "第19课", Source: "基本课文", Type: "original", Answer: "部屋の かぎを 忘れないで ください。"},
	}
	unit, list, found := resolveImportDocs(fetch, 19, "")
	if !found || unit != 5 || len(list) != 2 {
		t.Fatalf("应定位到两份文档，实际 unit=%d found=%v docs=%d", unit, found, len(list))
	}

	all, added, reports := collectImport(fetch, existing, 19, list)
	if len(added) != 2 {
		t.Fatalf("3 句里 1 句重复 → 应只加 2 条，实际 %d（%+v）", len(added), reports)
	}
	if len(all) != 3 {
		t.Errorf("句库应从 1 条变 3 条，实际 %d", len(all))
	}

	again, added2, _ := collectImport(fetch, all, 19, list)
	if len(added2) != 0 || len(again) != 3 {
		t.Errorf("再导一次应 0 新增、条数不变，实际 new=%d total=%d", len(added2), len(again))
	}
}
