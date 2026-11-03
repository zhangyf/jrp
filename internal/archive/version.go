package archive

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/zhangyf/jrp/internal/review"
)

// ============================================================================
// VERSION MANAGEMENT
// ============================================================================

// Filename generates the filename for an archive.
// Format: 日语学习进度档案_YYMMDD_vA.B.md
//
// 文件名前缀由调用方传入：原来在函数内部按 lang 查根包的
// LangConfigs[lang].FilePrefix，迁到 internal/archive 后不再依赖根包配置，
// 格式与原来完全一致。
func Filename(filePrefix string, date time.Time, major, minor int) string {
	return fmt.Sprintf("%s_%s_v%d.%d.md", filePrefix, date.Format("060102"), major, minor)
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
	if !SameDay(latestDate, today) {
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

// SameDay reports whether two times fall on the same calendar day.
func SameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

// AddChangelogEntry adds a new entry to the changelog and returns the formatted entry.
func AddChangelogEntry(arc *Archive, today time.Time, major, minor int, description string) {
	allWords := review.AllWords(arc.Groups)
	mastered, basic, needsConsol, untested := review.CountByStatus(allWords)

	entry := ChangelogEntry{
		Date:        today.Format("060102"),
		Version:     fmt.Sprintf("v%d.%d", major, minor),
		Total:       strconv.Itoa(len(allWords)),
		Mastered:    strconv.Itoa(mastered),
		Basic:       strconv.Itoa(basic),
		NeedsConsol: strconv.Itoa(needsConsol),
		Untested:    strconv.Itoa(untested),
		Errors:      strconv.Itoa(review.TotalErrors(arc.Groups)),
		NailHouse:   strconv.Itoa(review.CountNailHouseholds(arc.Groups)),
		Description: description,
	}
	arc.Changelog = append(arc.Changelog, entry)
}
