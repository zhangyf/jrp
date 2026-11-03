package archive

import (
	"strconv"
	"strings"

	"github.com/zhangyf/jrp/internal/review"
)

// ============================================================================
// ARCHIVE PARSER
// ============================================================================

// ParseArchive parses the markdown archive content into an Archive struct.
func ParseArchive(content string, lang string) (*Archive, error) {
	arc := &Archive{Language: lang}
	lines := strings.Split(content, "\n")

	// State machine for parsing
	var currentGroup *review.WordGroup
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
				if entry.Date != "" && entry.Date != "—" && entry.Date != "日期" {
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
				currentGroup = &review.WordGroup{Title: title}
				inWordTable = false
				continue
			}

			// Detect word table separator
			if strings.HasPrefix(trimmed, "|---") {
				inWordTable = true
				continue
			}

			// 词表表头行/全空行：不进 footer。
			// 表头行出现在 |--- 分隔行之前，此时 inWordTable 还是 false，
			// 以前会被当"正文"塞进 RawFooter —— 每次保存每个分组多复制一行
			// 表头（58 个分组 = 每写一次 footer 涨 58 行，2026-09-24 发现时
			// 已积到 1 万行）。全空行（|||||||）同理。两者都直接跳过。
			if strings.HasPrefix(trimmed, "|") && !inWordTable {
				parts := splitTableRow(trimmed)
				if len(parts) >= 2 && parts[1] == "中文释义" {
					continue // 词表表头（第2列固定是"中文释义"，与语言无关）
				}
				allEmpty := true
				for _, p := range parts {
					if p != "" {
						allEmpty = false
						break
					}
				}
				if allEmpty {
					continue
				}
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
func parseWordRow(line string) *review.Word {
	parts := splitTableRow(line)
	if len(parts) < 7 {
		return nil
	}

	w := &review.Word{
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
