package stats

import (
	"regexp"
	"sort"
	"strconv"

	"github.com/zhangyf/jrp/internal/review"
)

// JLPT 等级门槛（参考口径，见 JlptProgress 注释）。
const (
	jlptN5Vocab  = 800
	jlptN5Lesson = 22
	jlptN4Vocab  = 1500
	jlptN4Lesson = 48
)

var lessonRe = regexp.MustCompile(`第(\d+)课`)

// maxLesson 返回单词分组名里最大的课号（跟 StatsDetail.ByLesson 同一口径），
// 解析不出返回 0。
func maxLesson(words []review.Word) int {
	max := 0
	for _, w := range words {
		m := lessonRe.FindStringSubmatch(w.Group)
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// buildJlptProgress 用最新档案算 JLPT 进度。课号从分组名（如「第16课 生词表」）
// 里正则取出，取最大值 —— 老师学到哪课，就由最新的生词表分组决定。
func buildJlptProgress(groups []review.WordGroup) *JlptProgress {
	words := review.AllWords(groups)

	firm := 0
	for _, w := range words {
		if acc, ok := review.Accuracy(w); ok && acc >= 0.8 {
			firm++
		}
	}

	return &JlptProgress{
		VocabTotal: len(words),
		VocabFirm:  firm,
		Lesson:     maxLesson(words),
		N5:         JlptLevel{Vocab: jlptN5Vocab, Lesson: jlptN5Lesson},
		N4:         JlptLevel{Vocab: jlptN4Vocab, Lesson: jlptN4Lesson},
	}
}

// buildStatsDetail extracts per-lesson distribution, accuracy buckets, hard-word
// counts, and top-reviewed words from the latest archive's parsed word groups.
func buildStatsDetail(groups []review.WordGroup) *StatsDetail {
	words := review.AllWords(groups)

	// Per-lesson distribution
	lessonMap := make(map[string]int)
	for _, w := range words {
		lessonMap[w.Group]++
	}
	byLesson := make([]LessonCount, 0, len(lessonMap))
	for lesson, count := range lessonMap {
		byLesson = append(byLesson, LessonCount{Lesson: lesson, Count: count})
	}
	sort.Slice(byLesson, func(i, j int) bool {
		return byLesson[i].Lesson < byLesson[j].Lesson
	})

	// Accuracy distribution (only reviewed words)
	accDist := map[string]int{
		"0-30%": 0, "30-60%": 0, "60-80%": 0, "80-90%": 0, "90-100%": 0,
	}
	for _, w := range words {
		acc, ok := review.Accuracy(w)
		if !ok {
			continue
		}
		switch {
		case acc < 0.3:
			accDist["0-30%"]++
		case acc < 0.6:
			accDist["30-60%"]++
		case acc < 0.8:
			accDist["60-80%"]++
		case acc < 0.9:
			accDist["80-90%"]++
		default:
			accDist["90-100%"]++
		}
	}

	// Hard word counts by severity (using canonical IsHardWord with defaults)
	var hw HardWordCounts
	for _, w := range words {
		if !review.IsHardWord(w, review.DefaultHardMinAccuracy, review.DefaultHardMinReviews) {
			continue
		}
		hw.Total++
		acc, _ := review.Accuracy(w)
		switch {
		case acc < 0.3:
			hw.Severe++
		case acc < 0.45:
			hw.Moderate++
		default:
			hw.Mild++
		}
	}

	// Top 10 most-reviewed words
	sort.Slice(words, func(i, j int) bool {
		return words[i].ReviewCount > words[j].ReviewCount
	})
	topN := 10
	if len(words) < topN {
		topN = len(words)
	}
	topReviewed := make([]TopReviewedWord, 0, topN)
	for i := 0; i < topN; i++ {
		w := words[i]
		acc, _ := review.Accuracy(w)
		topReviewed = append(topReviewed, TopReviewedWord{
			Word:     w.Word,
			Reviews:  w.ReviewCount,
			Errors:   w.ErrorCount,
			Accuracy: acc,
		})
	}

	return &StatsDetail{
		ByLesson:             byLesson,
		AccuracyDistribution: accDist,
		HardWords:            hw,
		TopReviewed:          topReviewed,
	}
}
