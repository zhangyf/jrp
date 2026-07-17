package main

import (
	"fmt"
	"time"
)

// GetInterval returns the Ebbinghaus review interval in days.
// For words without errors, it's based on total review count.
// For words with errors, it's based on consecutive correct count.
func GetInterval(reviewOrConsec int) int {
	switch {
	case reviewOrConsec <= 0:
		return 1
	case reviewOrConsec == 1:
		return 2
	case reviewOrConsec == 2:
		return 4
	case reviewOrConsec == 3:
		return 7
	case reviewOrConsec == 4:
		return 10
	default:
		return 15
	}
}

// GetWordInterval returns the interval for a specific word.
// Words with errors use consecutiveCorrect; words without use reviewCount.
func GetWordInterval(w Word) int {
	if w.ErrorCount > 0 {
		return GetInterval(w.ConsecutiveCorrect)
	}
	return GetInterval(w.ReviewCount)
}

// DetermineStatus calculates the status emoji+label for a word.
// Rules (derived from analysis of 603 existing entries):
//   - 🔄待测试: reviewCount == 0
//   - 🔴待巩固: errorRate >= 30%
//   - 🟡基本掌握: reviewCount < 5 OR errorRate >= 15%
//   - 🟢已掌握: reviewCount >= 5 AND errorRate < 15%
func DetermineStatus(w Word) string {
	if w.ReviewCount == 0 {
		return "🔄待测试"
	}

	var errorRate float64
	if w.ReviewCount > 0 {
		errorRate = float64(w.ErrorCount) / float64(w.ReviewCount)
	}

	if errorRate >= 0.30 {
		return "🔴待巩固"
	}

	if w.ReviewCount >= 5 && errorRate < 0.15 {
		return "🟢已掌握"
	}

	return "🟡基本掌握"
}

// ParseDate parses a date string in MM/DD format, using the given year.
func ParseDate(s string, year int) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	t, err := time.Parse("01/02", s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(year, t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
}

// IsDue checks if a word is due for review on the given date.
func IsDue(w Word, today time.Time) bool {
	if w.LastReview == "" {
		return true // never reviewed → due
	}

	lastReview, err := ParseDate(w.LastReview, today.Year())
	if err != nil {
		return true // can't parse date → due (safe default)
	}

	// Handle year boundary: if lastReview is in the future relative to today,
	// it's probably from last year
	if lastReview.After(today) {
		lastReview = lastReview.AddDate(-1, 0, 0)
	}

	interval := GetWordInterval(w)
	dueDate := lastReview.AddDate(0, 0, interval)

	return !today.Before(dueDate)
}

// RecordCorrect updates a word after a correct answer.
func RecordCorrect(w *Word, today time.Time) {
	w.ReviewCount++
	w.ConsecutiveCorrect++
	w.LastReview = today.Format("01/02")
	w.Status = DetermineStatus(*w)
}

// RecordWrong updates a word after a wrong answer.
func RecordWrong(w *Word, today time.Time) {
	w.ReviewCount++
	w.ErrorCount++
	w.ConsecutiveCorrect = 0
	w.LastReview = today.Format("01/02")
	w.Status = DetermineStatus(*w)
}

// CountByStatus counts words by status category.
func CountByStatus(words []Word) (mastered, basic, needsConsol, untested int) {
	for _, w := range words {
		switch {
		case w.Status == "🟢已掌握" || w.ReviewCount >= 5 && w.ErrorCount == 0:
			mastered++
		case w.Status == "🔄待测试" || w.ReviewCount == 0:
			untested++
		case w.Status == "🔴待巩固" || (w.ReviewCount > 0 && float64(w.ErrorCount)/float64(w.ReviewCount) >= 0.30):
			needsConsol++
		default:
			basic++
		}
	}
	return
}

// CountAllWords returns the total number of words across all groups.
func CountAllWords(groups []WordGroup) int {
	total := 0
	for _, g := range groups {
		total += len(g.Words)
	}
	return total
}

// AllWords flattens all groups into a single word slice.
func AllWords(groups []WordGroup) []Word {
	var words []Word
	for _, g := range groups {
		words = append(words, g.Words...)
	}
	return words
}

// FindWord searches for a word by its text (exact match).
func FindWord(groups []WordGroup, wordText string) (*Word, int) {
	for gi, g := range groups {
		for wi, w := range g.Words {
			if w.Word == wordText {
				return &groups[gi].Words[wi], gi
			}
		}
	}
	return nil, -1
}

// TotalErrors returns the sum of all words' error counts.
func TotalErrors(groups []WordGroup) int {
	total := 0
	for _, w := range AllWords(groups) {
		total += w.ErrorCount
	}
	return total
}

// CountNailHouseholds counts words with >= 5 errors or error rate < 50%.
func CountNailHouseholds(groups []WordGroup) int {
	count := 0
	for _, w := range AllWords(groups) {
		if w.ErrorCount >= 5 {
			count++
			continue
		}
		if w.ReviewCount > 0 && float64(w.ErrorCount)/float64(w.ReviewCount) < 0.50 && w.ErrorCount > 0 {
			count++
		}
	}
	return count
}
