package archive

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhangyf/jrp/internal/review"
)

// ============================================================================
// ARCHIVE WRITER
// ============================================================================

// WriteArchive serializes an Archive back to markdown.
//
// 语言相关的两个字段由调用方传入：langName 是档案标题里的语言名（如「日语」），
// wordColumn 是词表第一列的列名（如「日语单词」）。原来在函数内部按
// arc.Language 查根包的 LangConfigs，迁到 internal/archive 后不再依赖根包，
// 改由调用方查表后传进来，输出内容一字不变。
func WriteArchive(arc *Archive, langName, wordColumn string) string {
	var b strings.Builder

	// Version history
	b.WriteString("## 📋 版本历史\n\n")
	b.WriteString("|日期|版本|总词数|🟢已掌握|🟡基本掌握|🔴待巩固|🔄待测试|累计答错|钉子户|变化内容|\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, e := range arc.Changelog {
		b.WriteString(fmt.Sprintf("|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|\n",
			e.Date, e.Version, e.Total, e.Mastered, e.Basic,
			e.NeedsConsol, e.Untested, e.Errors, e.NailHouse, e.Description))
	}
	b.WriteString("\n\n")

	// Title
	b.WriteString("# " + langName + "单词学习进度档案\n\n\n\n")

	// Metadata
	now := time.Now()
	b.WriteString(fmt.Sprintf("最后更新：%s\n", now.Format("01/02")))
	b.WriteString("数据来源：原表复习次数 + 本轮新增复习和错误\n")
	b.WriteString("管理方式：艾宾浩斯记忆曲线复习，每日复习+新词测试\n\n")

	// Mastery standards
	b.WriteString("掌握标准（艾宾浩斯记忆曲线）：\n")
	b.WriteString("- 🟢 已掌握 = 复习≥5次 且 答错率<15%，按间隔到期复习\n")
	b.WriteString("- 🟡 基本掌握 = 复习1~4次 或 答错率15%~30%，按间隔到期复习\n")
	b.WriteString("- 🔴 待巩固 = 答错率≥30%，间隔1天，连续写对才逐步拉长间隔\n")
	b.WriteString("- 🔄 待测试 = 已加入列表，尚未测试\n\n\n")

	// Ebbinghaus intervals
	b.WriteString("📅 艾宾浩斯复习间隔：\n\n")
	b.WriteString("|||\n|---|---|\n")
	b.WriteString("|0（刚学或刚错）|1天|\n")
	b.WriteString("|1次|2天|\n")
	b.WriteString("|2次|4天|\n")
	b.WriteString("|3次|7天|\n")
	b.WriteString("|4次|10天|\n")
	b.WriteString("|5次|15天|\n")
	b.WriteString("|6次|30天|\n")
	b.WriteString("|7次|60天|\n")
	b.WriteString("|8次|90天|\n")
	b.WriteString("|9次|120天|\n")
	b.WriteString("|10次以上|180天|\n\n\n")

	// Overall progress
	allWords := review.AllWords(arc.Groups)
	mastered, basic, needsConsol, untested := review.CountByStatus(allWords)
	totalWords := len(allWords)
	totalErrors := review.TotalErrors(arc.Groups)
	nailHouse := review.CountNailHouseholds(arc.Groups)

	b.WriteString("## 📊 总体进度\n\n")
	b.WriteString("|||\n|---|---|\n")
	b.WriteString(fmt.Sprintf("|总单词数|%d|\n", totalWords))
	b.WriteString(fmt.Sprintf("|🟢 已掌握（正确率≥85%%，≥5次）|%d|\n", mastered))
	b.WriteString(fmt.Sprintf("|🟡 基本掌握（正确率≥70%%，≥2次）|%d|\n", basic))
	b.WriteString(fmt.Sprintf("|🔴 待巩固（正确率<70%%或次）|%d|\n", needsConsol))
	if untested > 0 {
		b.WriteString(fmt.Sprintf("|🔄 待测试|%d|\n", untested))
	}
	b.WriteString(fmt.Sprintf("|累计答错次数|%d|\n", totalErrors))
	b.WriteString("\n\n\n")

	b.WriteString(fmt.Sprintf("💡 当前%d词中：🟢%d + 🟡%d + 🔴%d + 🔄%d = %d。其中高频钉子户(答错≥5次/正确率<50%%): %d词\n\n\n",
		totalWords, mastered, basic, needsConsol, untested, totalWords, nailHouse))

	// Word lists
	b.WriteString("## 📅 单词列表（按课分组）\n\n")
	for _, g := range arc.Groups {
		b.WriteString(fmt.Sprintf("### %s\n\n", g.Title))
		// 7-column table with headers
		b.WriteString(fmt.Sprintf("|%s|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n", wordColumn))
		b.WriteString("|---|---|---|---|---|---|---|\n")
		for _, w := range g.Words {
			b.WriteString(fmt.Sprintf("|%s|%s|%d|%d|%d|%s|%s|\n",
				w.Word, w.Definition, w.ReviewCount, w.ErrorCount,
				w.ConsecutiveCorrect, w.LastReview, w.Status))
		}
		b.WriteString("\n\n")
	}

	// Footer (preserve any extra content)
	if arc.RawFooter != "" {
		b.WriteString(arc.RawFooter)
		b.WriteString("\n")
	}

	return b.String()
}
