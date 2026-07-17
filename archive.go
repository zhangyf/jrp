package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// ARCHIVE PARSER
// ============================================================================

// ParseArchive parses the markdown archive content into an Archive struct.
func ParseArchive(content string, lang string) (*Archive, error) {
	arc := &Archive{Language: lang}
	lines := strings.Split(content, "\n")

	// State machine for parsing
	var currentGroup *WordGroup
	var inChangelog bool
	var inWordTable bool
	var headerLines []string
	var preChangelogLines []string
	var postWordLines []string
	var wordSectionStarted bool

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Detect changelog section
		if strings.HasPrefix(trimmed, "## 📋 版本历史") {
			inChangelog = true
			headerLines = append(headerLines, preChangelogLines...)
			preChangelogLines = nil
			continue
		}

		// Detect word list section
		if strings.HasPrefix(trimmed, "## 📅 单词列表") {
			wordSectionStarted = true
			inChangelog = false
			inWordTable = false
			if currentGroup != nil {
				arc.Groups = append(arc.Groups, *currentGroup)
				currentGroup = nil
			}
			continue
		}

		// Parse changelog entries
		if inChangelog && !wordSectionStarted {
			// Skip separator lines
			if strings.HasPrefix(trimmed, "|---") {
				continue
			}
			// Skip empty lines within changelog (don't exit)
			if trimmed == "" {
				continue
			}
			// Parse changelog row
			if strings.HasPrefix(trimmed, "|") {
				entry := parseChangelogRow(trimmed)
				if entry.Date != "" && entry.Date != "—" {
					arc.Changelog = append(arc.Changelog, entry)
				}
				continue
			}
			// Non-table, non-empty line → exit changelog
			inChangelog = false
			// This line might be part of the header (e.g. title)
			preChangelogLines = append(preChangelogLines, line)
			continue
		}

		// Detect group headers in word section
		if wordSectionStarted {
			if strings.HasPrefix(trimmed, "### ") {
				// Save previous group
				if currentGroup != nil {
					arc.Groups = append(arc.Groups, *currentGroup)
				}
				title := strings.TrimPrefix(trimmed, "### ")
				currentGroup = &WordGroup{Title: title}
				inWordTable = false
				continue
			}

			// Detect word table separator
			if strings.HasPrefix(trimmed, "|---") {
				inWordTable = true
				continue
			}

			// Parse word rows
			if inWordTable && strings.HasPrefix(trimmed, "|") {
				word := parseWordRow(trimmed)
				if word != nil && word.Word != "" && !isNonWordRow(trimmed) {
					if currentGroup != nil {
						word.Group = currentGroup.Title
						currentGroup.Words = append(currentGroup.Words, *word)
					}
				}
				continue
			}

			// Empty line ends word table
			if trimmed == "" && inWordTable {
				inWordTable = false
				continue
			}

			// Non-table content in word section → footer
			if !inWordTable && trimmed != "" {
				postWordLines = append(postWordLines, line)
			}
			continue
		}

		// Before changelog → header
		if !inChangelog {
			preChangelogLines = append(preChangelogLines, line)
		}
	}

	// Save last group
	if currentGroup != nil {
		arc.Groups = append(arc.Groups, *currentGroup)
	}

	// Store raw header and footer
	arc.RawHeader = strings.Join(headerLines, "\n")
	arc.RawFooter = strings.Join(postWordLines, "\n")

	// Extract title and last update from raw content
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "# ") && strings.Contains(t, "学习进度档案") {
			arc.Title = strings.TrimPrefix(t, "# ")
		}
		if strings.HasPrefix(t, "最后更新：") {
			arc.LastUpdate = strings.TrimPrefix(t, "最后更新：")
		}
	}

	return arc, nil
}

// parseChangelogRow parses a single changelog table row.
func parseChangelogRow(line string) ChangelogEntry {
	parts := splitTableRow(line)
	// Pad to 10 fields
	for len(parts) < 10 {
		parts = append(parts, "")
	}
	return ChangelogEntry{
		Date:        parts[0],
		Version:     parts[1],
		Total:       parts[2],
		Mastered:    parts[3],
		Basic:       parts[4],
		NeedsConsol: parts[5],
		Untested:    parts[6],
		Errors:      parts[7],
		NailHouse:   parts[8],
		Description: parts[9],
	}
}

// parseWordRow parses a single word table row.
func parseWordRow(line string) *Word {
	parts := splitTableRow(line)
	if len(parts) < 7 {
		return nil
	}

	w := &Word{
		Word:       parts[0],
		Definition: parts[1],
		Status:     parts[6],
	}

	w.ReviewCount, _ = strconv.Atoi(parts[2])
	w.ErrorCount, _ = strconv.Atoi(parts[3])
	w.ConsecutiveCorrect, _ = strconv.Atoi(parts[4])
	w.LastReview = parts[5]

	return w
}

// isNonWordRow checks if a row is not a valid word entry
// (e.g., listening training data mixed into word tables).
func isNonWordRow(line string) bool {
	parts := splitTableRow(line)
	if len(parts) == 0 {
		return true
	}
	// Check if first field looks like a header or non-word content
	first := parts[0]
	nonWordPatterns := []string{"日期", "正确", "总数", "正确率", "语速", "------"}
	for _, p := range nonWordPatterns {
		if first == p || strings.HasPrefix(first, p) {
			return true
		}
	}
	// Check if review count is a number
	if len(parts) >= 3 {
		if _, err := strconv.Atoi(parts[2]); err != nil {
			return true
		}
	}
	return false
}

// splitTableRow splits a markdown table row into cell values.
func splitTableRow(line string) []string {
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// ============================================================================
// ARCHIVE WRITER
// ============================================================================

// WriteArchive serializes an Archive back to markdown.
func WriteArchive(arc *Archive) string {
	var b strings.Builder
	cfg := LangConfigs[arc.Language]

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
	b.WriteString("# " + cfg.Name + "单词学习进度档案\n\n\n\n")

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
	b.WriteString("|5次以上|15天|\n\n\n")

	// Overall progress
	allWords := AllWords(arc.Groups)
	mastered, basic, needsConsol, untested := CountByStatus(allWords)
	totalWords := len(allWords)
	totalErrors := TotalErrors(arc.Groups)
	nailHouse := CountNailHouseholds(arc.Groups)

	b.WriteString("## 📊 总体进度\n\n")
	b.WriteString("|||\n|---|---|\n")
	b.WriteString(fmt.Sprintf("|总单词数|%d|\n", totalWords))
	b.WriteString(fmt.Sprintf("|🟢 已掌握（正确率≥85%，≥5次）|%d|\n", mastered))
	b.WriteString(fmt.Sprintf("|🟡 基本掌握（正确率≥70%，≥2次）|%d|\n", basic))
	b.WriteString(fmt.Sprintf("|🔴 待巩固（正确率<70%或次）|%d|\n", needsConsol))
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
		b.WriteString(fmt.Sprintf("|%s|中文释义|复习总次数|答错次数|连续正确次数|最近复习日期|状态|\n", cfg.WordColumn))
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

// ============================================================================
// VERSION MANAGEMENT
// ============================================================================

// ArchiveFilename generates the filename for an archive.
// Format: 日语学习进度档案_YYMMDD_vA.B.md
func ArchiveFilename(lang string, date time.Time, major, minor int) string {
	cfg := LangConfigs[lang]
	return fmt.Sprintf("%s_%s_v%d.%d.md", cfg.FilePrefix, date.Format("060102"), major, minor)
}

// ParseFilename extracts date and version from an archive filename.
func ParseFilename(filename string) (date time.Time, major, minor int, err error) {
	// Match pattern: prefix_YYMMDD_vA.B.md
	re := regexp.MustCompile(`_(\d{6})_v(\d+)\.(\d+)\.md$`)
	matches := re.FindStringSubmatch(filename)
	if matches == nil {
		return time.Time{}, 0, 0, fmt.Errorf("invalid archive filename: %s", filename)
	}
	date, err = time.Parse("060102", matches[1])
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("invalid date in filename: %s", matches[1])
	}
	major, _ = strconv.Atoi(matches[2])
	minor, _ = strconv.Atoi(matches[3])
	return
}

// NextVersion calculates the next version number for a new or updated archive.
// Rules:
//   - New day: major=1, minor=0
//   - Same day update: minor+1
//   - Major bump (format change / 20+ words / user request): major+1, minor=0
func NextVersion(latestDate time.Time, latestMajor, latestMinor int, today time.Time, majorBump bool) (int, int) {
	if !sameDay(latestDate, today) {
		// New day: A=1, B=0
		return 1, 0
	}

	if majorBump {
		// Major bump: A+1, B=0
		return latestMajor + 1, 0
	}

	// Same day, minor update: B+1
	return latestMajor, latestMinor + 1
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// AddChangelogEntry adds a new entry to the changelog and returns the formatted entry.
func AddChangelogEntry(arc *Archive, today time.Time, major, minor int, description string) {
	allWords := AllWords(arc.Groups)
	mastered, basic, needsConsol, untested := CountByStatus(allWords)

	entry := ChangelogEntry{
		Date:        today.Format("060102"),
		Version:     fmt.Sprintf("v%d.%d", major, minor),
		Total:       strconv.Itoa(len(allWords)),
		Mastered:    strconv.Itoa(mastered),
		Basic:       strconv.Itoa(basic),
		NeedsConsol: strconv.Itoa(needsConsol),
		Untested:    strconv.Itoa(untested),
		Errors:      strconv.Itoa(TotalErrors(arc.Groups)),
		NailHouse:   strconv.Itoa(CountNailHouseholds(arc.Groups)),
		Description: description,
	}
	arc.Changelog = append(arc.Changelog, entry)
}
