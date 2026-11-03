package review

import (
	"math"
	"reflect"
	"testing"
	"time"
)

const (
	ebbTestMastered = "\U0001f7e2已掌握"
	ebbTestBasic    = "\U0001f7e1基本掌握"
	ebbTestConsol   = "\U0001f534待巩固"
	ebbTestUntested = "\U0001f504待测试"
	ebbTestHard     = "\u2620\ufe0f钉子户"
	ebbTestSpot     = "\U0001f7e2抽查"
)

func TestAccuracy(t *testing.T) {
	tests := []struct {
		name string
		word Word
		want float64
		ok   bool
	}{
		{name: "NeverReviewed", word: Word{}, want: 0, ok: false},
		{name: "ZeroReviewsWithErrors", word: Word{ErrorCount: 3}, want: 0, ok: false},
		{name: "NegativeReviews", word: Word{ReviewCount: -1, ErrorCount: 1}, want: 0, ok: false},
		{name: "Perfect", word: Word{ReviewCount: 4}, want: 1, ok: true},
		{name: "AlwaysWrong", word: Word{ReviewCount: 4, ErrorCount: 4}, want: 0, ok: true},
		{name: "HalfCorrect", word: Word{ReviewCount: 4, ErrorCount: 2}, want: 0.5, ok: true},
		{name: "SixtyPercent", word: Word{ReviewCount: 5, ErrorCount: 2}, want: 0.6, ok: true},
		{name: "RepeatingFraction", word: Word{ReviewCount: 3, ErrorCount: 1}, want: 0.6666666666666666, ok: true},
		// 异常计数目前不做截断；这里只锁定旧行为，不把负准确率当作新业务规则。
		{name: "LegacyErrorsExceedReviewsNotClamped", word: Word{ReviewCount: 4, ErrorCount: 6}, want: -0.5, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Accuracy(tt.word)
			if ok != tt.ok || math.IsNaN(got) || math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("Accuracy(%+v) = (%v, %v), want (%v, %v)", tt.word, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestIsHardWord(t *testing.T) {
	tests := []struct {
		name   string
		word   Word
		minAcc float64
		minRev int
		want   bool
	}{
		{name: "NeverReviewed", word: Word{}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "OneReviewTooSmall", word: Word{ReviewCount: 1, ErrorCount: 1}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "TwoReviewsTooSmall", word: Word{ReviewCount: 2, ErrorCount: 2}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "ExactlyThreeReviews", word: Word{ReviewCount: 3, ErrorCount: 2}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: true},
		{name: "ThreeReviewsHighAccuracy", word: Word{ReviewCount: 3, ErrorCount: 1}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "JustBelowSixtyPercent", word: Word{ReviewCount: 100, ErrorCount: 41}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: true},
		{name: "ExactlySixtyPercentNotHard", word: Word{ReviewCount: 5, ErrorCount: 2}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "JustAboveSixtyPercent", word: Word{ReviewCount: 100, ErrorCount: 39}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "ManyHistoricalErrorsButHighAccuracy", word: Word{ReviewCount: 100, ErrorCount: 5}, minAcc: DefaultHardMinAccuracy, minRev: DefaultHardMinReviews, want: false},
		{name: "CustomAccuracyBelow", word: Word{ReviewCount: 4, ErrorCount: 2}, minAcc: 0.75, minRev: 4, want: true},
		{name: "CustomAccuracyEqualityNotHard", word: Word{ReviewCount: 4, ErrorCount: 1}, minAcc: 0.75, minRev: 4, want: false},
		{name: "CustomMinimumBlocks", word: Word{ReviewCount: 4, ErrorCount: 4}, minAcc: 0.75, minRev: 5, want: false},
		{name: "CustomMinimumAllowsFirstReview", word: Word{ReviewCount: 1, ErrorCount: 1}, minAcc: 0.75, minRev: 1, want: true},
		{name: "CustomZeroThresholdIsStrict", word: Word{ReviewCount: 3, ErrorCount: 3}, minAcc: 0, minRev: 3, want: false},
		{name: "NoAccuracyEvenWithoutMinimum", word: Word{ErrorCount: 5}, minAcc: 1, minRev: 0, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsHardWord(tt.word, tt.minAcc, tt.minRev); got != tt.want {
				t.Errorf("IsHardWord(%+v, %v, %d) = %v, want %v", tt.word, tt.minAcc, tt.minRev, got, tt.want)
			}
		})
	}
}

func TestGetInterval(t *testing.T) {
	tests := []struct {
		name  string
		count int
		want  int
	}{
		{name: "LargeNegative", count: -1000000, want: 1},
		{name: "NegativeOne", count: -1, want: 1},
		{name: "Zero", count: 0, want: 1},
		{name: "One", count: 1, want: 2},
		{name: "Two", count: 2, want: 4},
		{name: "Three", count: 3, want: 7},
		{name: "Four", count: 4, want: 10},
		{name: "Five", count: 5, want: 15},
		{name: "Six", count: 6, want: 30},
		{name: "Seven", count: 7, want: 60},
		{name: "Eight", count: 8, want: 90},
		{name: "Nine", count: 9, want: 120},
		{name: "Ten", count: 10, want: 180},
		{name: "LargePositive", count: 1000000, want: 180},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetInterval(tt.count); got != tt.want {
				t.Errorf("GetInterval(%d) = %d, want %d", tt.count, got, tt.want)
			}
		})
	}
}

func TestGetWordInterval(t *testing.T) {
	tests := []struct {
		name string
		word Word
		want int
	}{
		{name: "NoErrorsNegativeReviews", word: Word{ReviewCount: -1, ConsecutiveCorrect: 9}, want: 1},
		{name: "NoErrorsZeroReviews", word: Word{ConsecutiveCorrect: 9}, want: 1},
		{name: "NoErrorsOneReview", word: Word{ReviewCount: 1, ConsecutiveCorrect: 9}, want: 2},
		{name: "NoErrorsTotalReviewsNotStreak", word: Word{ReviewCount: 6, ConsecutiveCorrect: 1}, want: 30},
		{name: "NoErrorsTenReviews", word: Word{ReviewCount: 10, ConsecutiveCorrect: 2}, want: 180},
		{name: "NoErrorsLargeReviews", word: Word{ReviewCount: 1000000, ConsecutiveCorrect: 0}, want: 180},
		{name: "ErrorsNegativeStreak", word: Word{ReviewCount: 100, ErrorCount: 1, ConsecutiveCorrect: -1}, want: 1},
		{name: "ErrorsZeroStreak", word: Word{ReviewCount: 100, ErrorCount: 1, ConsecutiveCorrect: 0}, want: 1},
		{name: "ErrorsOneCorrect", word: Word{ReviewCount: 100, ErrorCount: 1, ConsecutiveCorrect: 1}, want: 2},
		{name: "ErrorsStreakNotTotalReviews", word: Word{ReviewCount: 100, ErrorCount: 7, ConsecutiveCorrect: 3}, want: 7},
		{name: "ErrorsTenCorrect", word: Word{ReviewCount: 100, ErrorCount: 7, ConsecutiveCorrect: 10}, want: 180},
		{name: "ErrorsLargeStreak", word: Word{ReviewCount: 2000000, ErrorCount: 7, ConsecutiveCorrect: 1000000}, want: 180},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetWordInterval(tt.word); got != tt.want {
				t.Errorf("GetWordInterval(%+v) = %d, want %d", tt.word, got, tt.want)
			}
		})
	}
}

func TestDetermineStatus(t *testing.T) {
	tests := []struct {
		name string
		word Word
		want string
	}{
		{name: "NeverReviewed", word: Word{}, want: ebbTestUntested},
		{name: "ZeroReviewsBeforeErrorsOrStoredStatus", word: Word{ErrorCount: 5, Status: ebbTestMastered}, want: ebbTestUntested},
		{name: "FirstCorrect", word: Word{ReviewCount: 1}, want: ebbTestBasic},
		{name: "FourCorrectNotMastered", word: Word{ReviewCount: 4}, want: ebbTestBasic},
		{name: "ExactlyFiveCorrect", word: Word{ReviewCount: 5}, want: ebbTestMastered},
		{name: "MoreThanFiveCorrect", word: Word{ReviewCount: 6}, want: ebbTestMastered},
		{name: "ErrorRateBelowFifteen", word: Word{ReviewCount: 100, ErrorCount: 14, Status: ebbTestConsol}, want: ebbTestMastered},
		{name: "ErrorRateExactlyFifteen", word: Word{ReviewCount: 20, ErrorCount: 3}, want: ebbTestBasic},
		{name: "ErrorRateAboveFifteen", word: Word{ReviewCount: 100, ErrorCount: 16}, want: ebbTestBasic},
		{name: "ErrorRateBelowThirty", word: Word{ReviewCount: 100, ErrorCount: 29}, want: ebbTestBasic},
		{name: "ErrorRateExactlyThirty", word: Word{ReviewCount: 10, ErrorCount: 3}, want: ebbTestConsol},
		{name: "ErrorRateAboveThirty", word: Word{ReviewCount: 100, ErrorCount: 31, Status: ebbTestMastered}, want: ebbTestConsol},
		{name: "HighErrorRateBeforeFiveReviews", word: Word{ReviewCount: 3, ErrorCount: 1}, want: ebbTestConsol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetermineStatus(tt.word); got != tt.want {
				t.Errorf("DetermineStatus(%+v) = %q, want %q", tt.word, got, tt.want)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		date    string
		year    int
		want    time.Time
		wantErr bool
	}{
		{name: "FirstDay", date: "01/01", year: 2026, want: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local)},
		{name: "LastDay", date: "12/31", year: 2026, want: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.Local)},
		{name: "SuppliedYearAndLocalMidnight", date: "06/15", year: 2031, want: time.Date(2031, time.June, 15, 0, 0, 0, 0, time.Local)},
		{name: "LeapDay", date: "02/29", year: 2024, want: time.Date(2024, time.February, 29, 0, 0, 0, 0, time.Local)},
		// 仅锁定现状：先按无年份日期解析，再由 time.Date 归一化；不是新的有效日期规则。
		{name: "LegacyNonLeapFebruary29NormalizesToMarch1", date: "02/29", year: 2025, want: time.Date(2025, time.March, 1, 0, 0, 0, 0, time.Local)},
		{name: "Empty", date: "", year: 2026, wantErr: true},
		{name: "InvalidText", date: "not-a-date", year: 2026, wantErr: true},
		{name: "MonthZero", date: "00/10", year: 2026, wantErr: true},
		{name: "MonthThirteen", date: "13/01", year: 2026, wantErr: true},
		{name: "DayZero", date: "01/00", year: 2026, wantErr: true},
		{name: "DayThirtyTwo", date: "01/32", year: 2026, wantErr: true},
		{name: "AprilThirtyOne", date: "04/31", year: 2026, wantErr: true},
		{name: "FebruaryThirty", date: "02/30", year: 2024, wantErr: true},
		{name: "MonthNotPadded", date: "1/02", year: 2026, wantErr: true},
		{name: "DayNotPadded", date: "01/2", year: 2026, wantErr: true},
		{name: "WrongSeparator", date: "01-02", year: 2026, wantErr: true},
		{name: "YearIncluded", date: "2026/01/02", year: 2026, wantErr: true},
		{name: "LeadingSpace", date: " 01/02", year: 2026, wantErr: true},
		{name: "TrailingSpace", date: "01/02 ", year: 2026, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDate(tt.date, tt.year)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDate(%q, %d) error = %v, wantErr %v", tt.date, tt.year, err, tt.wantErr)
			}
			if !got.Equal(tt.want) {
				t.Errorf("ParseDate(%q, %d) = %v, want %v", tt.date, tt.year, got, tt.want)
			}
			if !tt.wantErr && got.Location() != time.Local {
				t.Errorf("location = %v, want time.Local", got.Location())
			}
		})
	}
}

func TestIsDue(t *testing.T) {
	tests := []struct {
		name  string
		word  Word
		today time.Time
		want  bool
	}{
		{name: "NeverReviewed", word: Word{}, today: time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local), want: true},
		{name: "EmptyDateDespiteReviews", word: Word{ReviewCount: 100}, today: time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local), want: true},
		{name: "MalformedDateConservativelyDue", word: Word{ReviewCount: 100, LastReview: "invalid"}, today: time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local), want: true},
		{name: "InvalidCalendarDateConservativelyDue", word: Word{ReviewCount: 100, LastReview: "04/31"}, today: time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local), want: true},
		{name: "SameDayMidnight", word: Word{LastReview: "06/15"}, today: time.Date(2026, time.June, 15, 0, 0, 0, 0, time.Local), want: false},
		{name: "SameDayEvening", word: Word{LastReview: "06/15"}, today: time.Date(2026, time.June, 15, 23, 59, 59, 999999999, time.Local), want: false},
		{name: "BeforeDueDay", word: Word{ReviewCount: 2, LastReview: "06/15"}, today: time.Date(2026, time.June, 18, 12, 0, 0, 0, time.Local), want: false},
		{name: "OneNanosecondBeforeDue", word: Word{ReviewCount: 2, LastReview: "06/15"}, today: time.Date(2026, time.June, 18, 23, 59, 59, 999999999, time.Local), want: false},
		{name: "ExactlyDue", word: Word{ReviewCount: 2, LastReview: "06/15"}, today: time.Date(2026, time.June, 19, 0, 0, 0, 0, time.Local), want: true},
		{name: "OneNanosecondAfterDue", word: Word{ReviewCount: 2, LastReview: "06/15"}, today: time.Date(2026, time.June, 19, 0, 0, 0, 1, time.Local), want: true},
		{name: "Overdue", word: Word{ReviewCount: 2, LastReview: "06/15"}, today: time.Date(2026, time.June, 20, 12, 0, 0, 0, time.Local), want: true},
		{name: "ErrorsUseStreakAndAreDue", word: Word{ReviewCount: 100, ErrorCount: 5, ConsecutiveCorrect: 1, LastReview: "06/15"}, today: time.Date(2026, time.June, 17, 0, 0, 0, 0, time.Local), want: true},
		{name: "NoErrorsUseTotalAndAreNotDue", word: Word{ReviewCount: 100, ConsecutiveCorrect: 1, LastReview: "06/15"}, today: time.Date(2026, time.June, 17, 0, 0, 0, 0, time.Local), want: false},
		{name: "YearBoundaryDecember31ToJanuary1Due", word: Word{ReviewCount: 10, ErrorCount: 1, LastReview: "12/31"}, today: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local), want: true},
		{name: "YearBoundaryDecember31ToJanuary1BeforeDue", word: Word{ReviewCount: 1, LastReview: "12/31"}, today: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local), want: false},
		{name: "YearBoundaryExactlyDueJanuary2", word: Word{ReviewCount: 1, LastReview: "12/31"}, today: time.Date(2026, time.January, 2, 0, 0, 0, 0, time.Local), want: true},
		{name: "LeapDaySameDay", word: Word{LastReview: "02/29"}, today: time.Date(2024, time.February, 29, 12, 0, 0, 0, time.Local), want: false},
		{name: "LeapDayDueMarch1", word: Word{LastReview: "02/29"}, today: time.Date(2024, time.March, 1, 0, 0, 0, 0, time.Local), want: true},
		{name: "LeapYearFebruary28DueFebruary29", word: Word{LastReview: "02/28"}, today: time.Date(2024, time.February, 29, 0, 0, 0, 0, time.Local), want: true},
		// 与 ParseDate 的 Legacy 行为一致，仅记录当前兼容语义。
		{name: "LegacyNonLeapFebruary29NotDueMarch1", word: Word{LastReview: "02/29"}, today: time.Date(2025, time.March, 1, 0, 0, 0, 0, time.Local), want: false},
		{name: "LegacyNonLeapFebruary29DueMarch2", word: Word{LastReview: "02/29"}, today: time.Date(2025, time.March, 2, 0, 0, 0, 0, time.Local), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDue(tt.word, tt.today); got != tt.want {
				t.Errorf("IsDue(%+v, %v) = %v, want %v", tt.word, tt.today, got, tt.want)
			}
		})
	}
}

func TestRecordCorrect(t *testing.T) {
	today := time.Date(2026, time.January, 2, 15, 4, 5, 6, time.Local)
	tests := []struct {
		name string
		word Word
		want Word
	}{
		{
			name: "FirstCorrectPreservesIdentity",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", Status: ebbTestUntested},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 1, ErrorCount: 0, ConsecutiveCorrect: 1, LastReview: "01/02", Status: ebbTestBasic},
		},
		{
			name: "FifthCorrectBecomesMastered",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 4, ConsecutiveCorrect: 4, LastReview: "12/31", Status: ebbTestBasic},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 5, ErrorCount: 0, ConsecutiveCorrect: 5, LastReview: "01/02", Status: ebbTestMastered},
		},
		{
			name: "CorrectDoesNotEraseErrors",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 2, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "12/31", Status: ebbTestConsol},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 3, ErrorCount: 2, ConsecutiveCorrect: 1, LastReview: "01/02", Status: ebbTestConsol},
		},
		{
			name: "CrossBelowThirtyPercent",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 1, LastReview: "12/31", Status: ebbTestConsol},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 4, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "01/02", Status: ebbTestBasic},
		},
		{
			name: "FifthReviewWithErrorsStillBasic",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 4, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "12/31", Status: ebbTestMastered},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 5, ErrorCount: 1, ConsecutiveCorrect: 3, LastReview: "01/02", Status: ebbTestBasic},
		},
		{
			name: "ExactlyThirtyPercentRemainsConsol",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 9, ErrorCount: 3, ConsecutiveCorrect: 2, LastReview: "12/31", Status: "旧状态"},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 10, ErrorCount: 3, ConsecutiveCorrect: 3, LastReview: "01/02", Status: ebbTestConsol},
		},
		{
			name: "ExactlyFifteenPercentRemainsBasic",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 19, ErrorCount: 3, ConsecutiveCorrect: 4, LastReview: "12/31", Status: ebbTestBasic},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 20, ErrorCount: 3, ConsecutiveCorrect: 5, LastReview: "01/02", Status: ebbTestBasic},
		},
		{
			name: "CrossBelowFifteenPercent",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 20, ErrorCount: 3, ConsecutiveCorrect: 5, LastReview: "12/31", Status: ebbTestBasic},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 21, ErrorCount: 3, ConsecutiveCorrect: 6, LastReview: "01/02", Status: ebbTestMastered},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.word
			RecordCorrect(&got, today)
			if got != tt.want {
				t.Errorf("RecordCorrect() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRecordWrong(t *testing.T) {
	today := time.Date(2026, time.February, 3, 15, 4, 5, 6, time.Local)
	tests := []struct {
		name string
		word Word
		want Word
	}{
		{
			name: "FirstWrongPreservesIdentity",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", Status: ebbTestUntested},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 1, ErrorCount: 1, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestConsol},
		},
		{
			name: "WrongResetsNonzeroStreak",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 4, ConsecutiveCorrect: 4, LastReview: "01/31", Status: ebbTestBasic},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 5, ErrorCount: 1, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestBasic},
		},
		{
			name: "SecondReviewBecomesConsol",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 1, ConsecutiveCorrect: 1, LastReview: "01/31", Status: ebbTestBasic},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 2, ErrorCount: 1, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestConsol},
		},
		{
			name: "ExactlyThirtyPercentBecomesConsol",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 9, ErrorCount: 2, ConsecutiveCorrect: 5, LastReview: "01/31", Status: ebbTestBasic},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 10, ErrorCount: 3, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestConsol},
		},
		{
			name: "ExactlyFifteenPercentLosesMastery",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 19, ErrorCount: 2, ConsecutiveCorrect: 9, LastReview: "01/31", Status: ebbTestMastered},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 20, ErrorCount: 3, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestBasic},
		},
		{
			name: "LowErrorRateCanRemainMastered",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 19, ErrorCount: 1, ConsecutiveCorrect: 9, LastReview: "01/31", Status: ebbTestMastered},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 20, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestMastered},
		},
		{
			name: "RepeatedWrongIncrementsExistingErrors",
			word: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 5, ErrorCount: 3, ConsecutiveCorrect: 0, LastReview: "01/31", Status: "旧状态"},
			want: Word{Word: "café", Definition: "咖啡", Group: "测试组", ReviewCount: 6, ErrorCount: 4, ConsecutiveCorrect: 0, LastReview: "02/03", Status: ebbTestConsol},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.word
			RecordWrong(&got, today)
			if got != tt.want {
				t.Errorf("RecordWrong() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRecordAnswersSequence(t *testing.T) {
	word := Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 4, ConsecutiveCorrect: 4, LastReview: "12/30", Status: ebbTestBasic}
	steps := []struct {
		name    string
		correct bool
		today   time.Time
		want    Word
	}{
		{name: "CorrectToMastered", correct: true, today: time.Date(2025, time.December, 31, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 5, ErrorCount: 0, ConsecutiveCorrect: 5, LastReview: "12/31", Status: ebbTestMastered}},
		{name: "WrongResetsStreakAcrossYear", correct: false, today: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 6, ErrorCount: 1, ConsecutiveCorrect: 0, LastReview: "01/01", Status: ebbTestBasic}},
		{name: "CorrectRecoversMastery", correct: true, today: time.Date(2026, time.January, 2, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 7, ErrorCount: 1, ConsecutiveCorrect: 1, LastReview: "01/02", Status: ebbTestMastered}},
		{name: "WrongReturnsToBasic", correct: false, today: time.Date(2026, time.January, 3, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 8, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "01/03", Status: ebbTestBasic}},
		{name: "CorrectRetainsTwoErrors", correct: true, today: time.Date(2026, time.January, 4, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 9, ErrorCount: 2, ConsecutiveCorrect: 1, LastReview: "01/04", Status: ebbTestBasic}},
		{name: "WrongHitsThirtyPercent", correct: false, today: time.Date(2026, time.January, 5, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 10, ErrorCount: 3, ConsecutiveCorrect: 0, LastReview: "01/05", Status: ebbTestConsol}},
		{name: "CorrectFallsBelowThirtyPercent", correct: true, today: time.Date(2026, time.January, 6, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 11, ErrorCount: 3, ConsecutiveCorrect: 1, LastReview: "01/06", Status: ebbTestBasic}},
		{name: "CorrectExtendsNewStreak", correct: true, today: time.Date(2026, time.January, 7, 12, 0, 0, 0, time.Local), want: Word{Word: "café", Definition: "咖啡", Group: "交错测试组", ReviewCount: 12, ErrorCount: 3, ConsecutiveCorrect: 2, LastReview: "01/07", Status: ebbTestBasic}},
	}
	for _, step := range steps {
		// 后续步骤依赖前一步的实际结果，不用期望值重置 word。
		if !t.Run(step.name, func(t *testing.T) {
			if step.correct {
				RecordCorrect(&word, step.today)
			} else {
				RecordWrong(&word, step.today)
			}
			if word != step.want {
				t.Fatalf("word = %+v, want %+v", word, step.want)
			}
		}) {
			return
		}
	}
}

func TestCountByStatus(t *testing.T) {
	tests := []struct {
		name  string
		words []Word
		want  [4]int // 已掌握、基本掌握、待巩固、待测试。
	}{
		{name: "Nil", words: nil, want: [4]int{0, 0, 0, 0}},
		{name: "Empty", words: []Word{}, want: [4]int{0, 0, 0, 0}},
		{name: "StoredMasteredBeforeZeroReviews", words: []Word{{Status: ebbTestMastered}}, want: [4]int{1, 0, 0, 0}},
		{name: "StoredMasteredBeforeHighErrorRate", words: []Word{{Status: ebbTestMastered, ReviewCount: 10, ErrorCount: 10}}, want: [4]int{1, 0, 0, 0}},
		{name: "MasteryFallbackBeforeStoredUntested", words: []Word{{Status: ebbTestUntested, ReviewCount: 5}}, want: [4]int{1, 0, 0, 0}},
		{name: "MasteryFallbackBeforeStoredConsol", words: []Word{{Status: ebbTestConsol, ReviewCount: 5}}, want: [4]int{1, 0, 0, 0}},
		{name: "MasteryFallbackBeforeStoredBasic", words: []Word{{Status: ebbTestBasic, ReviewCount: 5}}, want: [4]int{1, 0, 0, 0}},
		{name: "StoredUntestedBeforeHighErrorRate", words: []Word{{Status: ebbTestUntested, ReviewCount: 10, ErrorCount: 9}}, want: [4]int{0, 0, 0, 1}},
		{name: "ZeroReviewsBeforeStoredConsol", words: []Word{{Status: ebbTestConsol, ErrorCount: 5}}, want: [4]int{0, 0, 0, 1}},
		{name: "ZeroReviewsBeforeStoredBasic", words: []Word{{Status: ebbTestBasic}}, want: [4]int{0, 0, 0, 1}},
		{name: "StoredConsolBeforeLowErrorRate", words: []Word{{Status: ebbTestConsol, ReviewCount: 10, ErrorCount: 1}}, want: [4]int{0, 0, 1, 0}},
		{name: "HighErrorRateBeforeStoredBasic", words: []Word{{Status: ebbTestBasic, ReviewCount: 10, ErrorCount: 3}}, want: [4]int{0, 0, 1, 0}},
		{name: "UnknownStatusNoReviews", words: []Word{{Status: "unknown"}}, want: [4]int{0, 0, 0, 1}},
		{name: "UnknownStatusFourCleanReviews", words: []Word{{Status: "unknown", ReviewCount: 4}}, want: [4]int{0, 1, 0, 0}},
		{name: "UnknownStatusFiveCleanReviews", words: []Word{{Status: "unknown", ReviewCount: 5}}, want: [4]int{1, 0, 0, 0}},
		// 不能用 DetermineStatus 替代：低错误率但有错，缺少已掌握状态时仍计基本掌握。
		{name: "LowErrorRateWithNoStoredMasteryStaysBasic", words: []Word{{ReviewCount: 10, ErrorCount: 1}}, want: [4]int{0, 1, 0, 0}},
		{name: "BelowThirtyPercentFallback", words: []Word{{ReviewCount: 100, ErrorCount: 29}}, want: [4]int{0, 1, 0, 0}},
		{name: "ExactlyThirtyPercentFallback", words: []Word{{ReviewCount: 10, ErrorCount: 3}}, want: [4]int{0, 0, 1, 0}},
		{
			name: "MixedStatusesAndPriority",
			words: []Word{
				{Status: ebbTestMastered},
				{Status: ebbTestConsol, ReviewCount: 5},
				{Status: ebbTestUntested, ReviewCount: 3, ErrorCount: 3},
				{Status: ebbTestConsol},
				{Status: ebbTestConsol, ReviewCount: 20, ErrorCount: 1},
				{Status: ebbTestBasic, ReviewCount: 10, ErrorCount: 3},
				{ReviewCount: 20, ErrorCount: 1},
				{ReviewCount: 4},
			},
			want: [4]int{2, 2, 2, 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mastered, basic, consol, untested := CountByStatus(tt.words)
			got := [4]int{mastered, basic, consol, untested}
			if got != tt.want {
				t.Errorf("CountByStatus(%+v) = %v, want %v", tt.words, got, tt.want)
			}
		})
	}
}

func TestCountAllWords(t *testing.T) {
	tests := []struct {
		name   string
		groups []WordGroup
		want   int
	}{
		{name: "Nil", groups: nil, want: 0},
		{name: "Empty", groups: []WordGroup{}, want: 0},
		{name: "EmptyGroups", groups: []WordGroup{{Title: "空组"}, {Title: "空切片", Words: []Word{}}}, want: 0},
		{name: "MixedGroups", groups: []WordGroup{{Title: "空组"}, {Title: "甲组", Words: []Word{{Word: "a"}, {Word: "b"}}}, {Title: "空切片", Words: []Word{}}, {Title: "乙组", Words: []Word{{Word: "c"}}}}, want: 3},
		{name: "DuplicatesAndEmptyWordStillCount", groups: []WordGroup{{Words: []Word{{Word: "same"}, {Word: "same"}, {}}}, {Words: []Word{{Word: "same"}}}}, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountAllWords(tt.groups); got != tt.want {
				t.Errorf("CountAllWords(%+v) = %d, want %d", tt.groups, got, tt.want)
			}
		})
	}
}

func TestAllWords(t *testing.T) {
	first := Word{Word: "zeta", Definition: "首项", ReviewCount: 7, ErrorCount: 2, ConsecutiveCorrect: 1, LastReview: "06/01", Status: ebbTestBasic, Group: "词内甲组"}
	second := Word{Word: "alpha", Definition: "第二项", ReviewCount: 5, ErrorCount: 0, ConsecutiveCorrect: 5, LastReview: "06/02", Status: ebbTestMastered, Group: "词内乙组"}
	third := Word{Word: "zeta", Definition: "重复词形但不同释义", ReviewCount: 3, ErrorCount: 2, ConsecutiveCorrect: 0, LastReview: "06/03", Status: ebbTestConsol, Group: "词内丙组"}
	tests := []struct {
		name   string
		groups []WordGroup
		want   []Word
	}{
		// 兼容当前返回形态：空结果为 nil，而非新分配的空切片；纯迁包不能顺手改变它。
		{name: "Nil", groups: nil, want: nil},
		{name: "Empty", groups: []WordGroup{}, want: nil},
		{name: "EmptyGroups", groups: []WordGroup{{Title: "空组"}, {Title: "空切片", Words: []Word{}}}, want: nil},
		{name: "SingleGroupValueCopy", groups: []WordGroup{{Title: "组标题不覆盖词内组", Words: []Word{first, second}}}, want: []Word{first, second}},
		{name: "GroupAndWordOrderWithDuplicates", groups: []WordGroup{{Title: "前空组"}, {Title: "甲", Words: []Word{first, second}}, {Title: "中空组", Words: []Word{}}, {Title: "乙", Words: []Word{third}}, {Title: "尾空组"}}, want: []Word{first, second, third}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AllWords(tt.groups)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("AllWords() = %+v, want %+v", got, tt.want)
			}
			flatIndex := 0
			for gi := range tt.groups {
				for wi := range tt.groups[gi].Words {
					original := tt.groups[gi].Words[wi]
					copyEdit := Word{Word: "副本改动", Definition: "只改副本", Group: "副本组"}
					got[flatIndex] = copyEdit
					if tt.groups[gi].Words[wi] != original {
						t.Fatalf("修改结果[%d]影响原 groups[%d].Words[%d]", flatIndex, gi, wi)
					}
					tt.groups[gi].Words[wi] = Word{Word: "原组改动", Definition: "只改原组", Group: "原组"}
					if got[flatIndex] != copyEdit {
						t.Fatalf("修改原 groups[%d].Words[%d]影响结果[%d]", gi, wi, flatIndex)
					}
					flatIndex++
				}
			}
		})
	}
}

func TestFindWord(t *testing.T) {
	first := Word{Word: "cat", Definition: "猫", ReviewCount: 3, ErrorCount: 1, ConsecutiveCorrect: 2, LastReview: "06/01", Status: ebbTestConsol, Group: "甲组"}
	duplicate := Word{Word: "cat", Definition: "另一个词条", Group: "乙组"}
	tests := []struct {
		name      string
		groups    []WordGroup
		query     string
		want      Word
		wantGroup int
		wantIndex int
	}{
		{name: "Nil", groups: nil, query: "cat", wantGroup: -1, wantIndex: -1},
		{name: "Empty", groups: []WordGroup{}, query: "cat", wantGroup: -1, wantIndex: -1},
		{name: "EmptyGroups", groups: []WordGroup{{}, {Words: []Word{}}}, query: "cat", wantGroup: -1, wantIndex: -1},
		{name: "FirstItem", groups: []WordGroup{{Words: []Word{first, {Word: "dog"}}}}, query: "cat", want: first, wantGroup: 0, wantIndex: 0},
		{name: "SecondItem", groups: []WordGroup{{Words: []Word{{Word: "dog"}, first}}}, query: "cat", want: first, wantGroup: 0, wantIndex: 1},
		{name: "LaterGroupKeepsOriginalGroupIndex", groups: []WordGroup{{}, {Words: []Word{{Word: "dog"}}}, {}, {Words: []Word{first}}}, query: "cat", want: first, wantGroup: 3, wantIndex: 0},
		{name: "FirstDuplicateWithinGroup", groups: []WordGroup{{Words: []Word{first, duplicate}}}, query: "cat", want: first, wantGroup: 0, wantIndex: 0},
		{name: "FirstDuplicateAcrossGroups", groups: []WordGroup{{}, {Words: []Word{first}}, {Words: []Word{duplicate}}}, query: "cat", want: first, wantGroup: 1, wantIndex: 0},
		{name: "CaseSensitive", groups: []WordGroup{{Words: []Word{first}}}, query: "Cat", wantGroup: -1, wantIndex: -1},
		{name: "NoWhitespaceTrimming", groups: []WordGroup{{Words: []Word{first}}}, query: " cat ", wantGroup: -1, wantIndex: -1},
		{name: "NoPrefixMatching", groups: []WordGroup{{Words: []Word{first}}}, query: "ca", wantGroup: -1, wantIndex: -1},
		{name: "Missing", groups: []WordGroup{{Words: []Word{first}}}, query: "bird", wantGroup: -1, wantIndex: -1},
		{name: "EmptyTextExactMatch", groups: []WordGroup{{Words: []Word{first, {Word: "", Definition: "空词形"}}}}, query: "", want: Word{Word: "", Definition: "空词形"}, wantGroup: 0, wantIndex: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, groupIndex := FindWord(tt.groups, tt.query)
			if groupIndex != tt.wantGroup {
				t.Fatalf("FindWord(%q) group = %d, want %d", tt.query, groupIndex, tt.wantGroup)
			}
			if tt.wantGroup == -1 {
				if got != nil {
					t.Errorf("FindWord(%q) = %+v, want nil", tt.query, got)
				}
				return
			}
			if got != &tt.groups[tt.wantGroup].Words[tt.wantIndex] {
				t.Fatalf("FindWord(%q) 必须返回原 groups[%d].Words[%d] 的指针", tt.query, tt.wantGroup, tt.wantIndex)
			}
			if *got != tt.want {
				t.Fatalf("FindWord(%q) = %+v, want %+v", tt.query, *got, tt.want)
			}
			replacement := Word{Word: "回写词形", Definition: "回写释义", ReviewCount: 9, ErrorCount: 2, ConsecutiveCorrect: 3, LastReview: "07/08", Status: ebbTestBasic, Group: "回写组"}
			*got = replacement
			if tt.groups[tt.wantGroup].Words[tt.wantIndex] != replacement {
				t.Error("返回指针的修改未回写原 groups")
			}
		})
	}
}

func TestTotalErrors(t *testing.T) {
	tests := []struct {
		name   string
		groups []WordGroup
		want   int
	}{
		{name: "Nil", groups: nil, want: 0},
		{name: "Empty", groups: []WordGroup{}, want: 0},
		{name: "EmptyGroups", groups: []WordGroup{{}, {Words: []Word{}}}, want: 0},
		{name: "OnlyErrorCountsMatter", groups: []WordGroup{{Words: []Word{{ReviewCount: 100}, {ReviewCount: 3, ErrorCount: 2}}}, {}, {Words: []Word{{ReviewCount: 50, ErrorCount: 4}}}}, want: 6},
		{name: "DuplicatesEachContribute", groups: []WordGroup{{Words: []Word{{Word: "same", ErrorCount: 2}}}, {Words: []Word{{Word: "same", ErrorCount: 3}}}}, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TotalErrors(tt.groups); got != tt.want {
				t.Errorf("TotalErrors(%+v) = %d, want %d", tt.groups, got, tt.want)
			}
		})
	}
}

func TestCountNailHouseholds(t *testing.T) {
	// 两列期望分别手写，明确历史计数与复习分类的正确率规则并不等价。
	tests := []struct {
		name       string
		groups     []WordGroup
		wantLegacy int
		wantHard   int
	}{
		{name: "Nil", groups: nil, wantLegacy: 0, wantHard: 0},
		{name: "Empty", groups: []WordGroup{}, wantLegacy: 0, wantHard: 0},
		{name: "EmptyGroups", groups: []WordGroup{{}, {Words: []Word{}}}, wantLegacy: 0, wantHard: 0},
		{name: "NeverReviewed", groups: []WordGroup{{Words: []Word{{}}}}, wantLegacy: 0, wantHard: 0},
		{name: "NoErrorsNotHistoricalNail", groups: []WordGroup{{Words: []Word{{ReviewCount: 100}}}}, wantLegacy: 0, wantHard: 0},
		{name: "ExactlyHalfWithFewReviews", groups: []WordGroup{{Words: []Word{{ReviewCount: 2, ErrorCount: 1}}}}, wantLegacy: 0, wantHard: 0},
		{name: "ExactlyHalfBelowFiveErrors", groups: []WordGroup{{Words: []Word{{ReviewCount: 8, ErrorCount: 4}}}}, wantLegacy: 0, wantHard: 1},
		{name: "LowErrorRateOnlyHistoricalNail", groups: []WordGroup{{Words: []Word{{ReviewCount: 3, ErrorCount: 1}}}}, wantLegacy: 1, wantHard: 0},
		{name: "HighErrorRateOnlyReviewHardWord", groups: []WordGroup{{Words: []Word{{ReviewCount: 3, ErrorCount: 2}}}}, wantLegacy: 0, wantHard: 1},
		{name: "FourErrorsNotEnoughForHistoricalThreshold", groups: []WordGroup{{Words: []Word{{ReviewCount: 4, ErrorCount: 4}}}}, wantLegacy: 0, wantHard: 1},
		{name: "FiveErrorsHistoricalThreshold", groups: []WordGroup{{Words: []Word{{ReviewCount: 5, ErrorCount: 5}}}}, wantLegacy: 1, wantHard: 1},
		{name: "FiveErrorsAtHalfStillHistoricalNail", groups: []WordGroup{{Words: []Word{{ReviewCount: 10, ErrorCount: 5}}}}, wantLegacy: 1, wantHard: 1},
		{name: "ManyReviewsHighAccuracyStillHistoricalNail", groups: []WordGroup{{Words: []Word{{ReviewCount: 100, ErrorCount: 5}}}}, wantLegacy: 1, wantHard: 0},
		{name: "FiveErrorsWithoutReviewsStillHistoricalNail", groups: []WordGroup{{Words: []Word{{ErrorCount: 5}}}}, wantLegacy: 1, wantHard: 0},
		{name: "BelowFiveErrorsWithoutReviewsNotHistoricalNail", groups: []WordGroup{{Words: []Word{{ErrorCount: 4}}}}, wantLegacy: 0, wantHard: 0},
		{name: "LowErrorRateAndLowAccuracyCanOverlap", groups: []WordGroup{{Words: []Word{{ReviewCount: 9, ErrorCount: 4}}}}, wantLegacy: 1, wantHard: 1},
		{
			name: "MixedGroupsKeepMetricsDistinct",
			groups: []WordGroup{
				{Title: "空组"},
				{Title: "甲组", Words: []Word{{ReviewCount: 3, ErrorCount: 1}, {ReviewCount: 3, ErrorCount: 2}, {ReviewCount: 100, ErrorCount: 5}}},
				{Title: "乙组", Words: []Word{{ReviewCount: 6, ErrorCount: 6}, {}, {ReviewCount: 20}}},
			},
			wantLegacy: 3,
			wantHard:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountNailHouseholds(tt.groups); got != tt.wantLegacy {
				t.Errorf("CountNailHouseholds() = %d, want %d", got, tt.wantLegacy)
			}
			gotHard := 0
			for _, group := range tt.groups {
				for _, word := range group.Words {
					if IsHardWord(word, DefaultHardMinAccuracy, DefaultHardMinReviews) {
						gotHard++
					}
				}
			}
			if gotHard != tt.wantHard {
				t.Errorf("IsHardWord 命中数 = %d, want %d", gotHard, tt.wantHard)
			}
		})
	}
}

func TestGetReviewCategory(t *testing.T) {
	tests := []struct {
		name string
		word Word
		want string
	}{
		{name: "NeverReviewed", word: Word{}, want: ebbTestUntested},
		{name: "ZeroReviewsBeforeErrorsAndStoredStatus", word: Word{ErrorCount: 5, Status: ebbTestMastered}, want: ebbTestUntested},
		{name: "OneWrongTooFewForHard", word: Word{ReviewCount: 1, ErrorCount: 1}, want: ebbTestConsol},
		{name: "TwoWrongTooFewForHard", word: Word{ReviewCount: 2, ErrorCount: 2}, want: ebbTestConsol},
		{name: "ThreeReviewsAllowHard", word: Word{ReviewCount: 3, ErrorCount: 2, Status: ebbTestMastered}, want: ebbTestHard},
		{name: "JustBelowSixtyPercentHard", word: Word{ReviewCount: 100, ErrorCount: 41}, want: ebbTestHard},
		{name: "ExactlySixtyPercentNotHard", word: Word{ReviewCount: 5, ErrorCount: 2}, want: ebbTestConsol},
		{name: "JustAboveSixtyPercentNotHard", word: Word{ReviewCount: 100, ErrorCount: 39}, want: ebbTestConsol},
		{name: "FourCleanReviewsBasic", word: Word{ReviewCount: 4}, want: ebbTestBasic},
		{name: "FiveCleanReviewsSpotCheck", word: Word{ReviewCount: 5}, want: ebbTestSpot},
		{name: "ErrorRateBelowFifteenSpotCheck", word: Word{ReviewCount: 100, ErrorCount: 14, Status: ebbTestConsol}, want: ebbTestSpot},
		{name: "ErrorRateExactlyFifteenBasic", word: Word{ReviewCount: 20, ErrorCount: 3}, want: ebbTestBasic},
		{name: "ErrorRateAboveFifteenBasic", word: Word{ReviewCount: 100, ErrorCount: 16}, want: ebbTestBasic},
		{name: "ErrorRateBelowThirtyBasic", word: Word{ReviewCount: 100, ErrorCount: 29}, want: ebbTestBasic},
		{name: "ErrorRateExactlyThirtyConsol", word: Word{ReviewCount: 10, ErrorCount: 3}, want: ebbTestConsol},
		{name: "ErrorRateAboveThirtyConsol", word: Word{ReviewCount: 100, ErrorCount: 31}, want: ebbTestConsol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetReviewCategory(tt.word); got != tt.want {
				t.Errorf("GetReviewCategory(%+v) = %q, want %q", tt.word, got, tt.want)
			}
		})
	}
}

func TestStatusPriority(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   int
	}{
		{name: "HardFirst", status: ebbTestHard, want: 0},
		{name: "ConsolSecond", status: ebbTestConsol, want: 1},
		{name: "UntestedThird", status: ebbTestUntested, want: 2},
		{name: "BasicFourth", status: ebbTestBasic, want: 3},
		{name: "SpotCheckFifth", status: ebbTestSpot, want: 4},
		{name: "MasteredIsNotSpotCheckCategory", status: ebbTestMastered, want: 5},
		{name: "UnknownLast", status: "unknown", want: 5},
		{name: "EmptyLast", status: "", want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusPriority(tt.status); got != tt.want {
				t.Errorf("StatusPriority(%q) = %d, want %d", tt.status, got, tt.want)
			}
		})
	}
}
