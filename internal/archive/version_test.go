package archive

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhangyf/jrp/internal/review"
)

// 状态串用 Go 转义书写，避免在测试源码里新增 emoji 字形。
const (
	vtMastered = "\U0001f7e2已掌握"
	vtBasic    = "\U0001f7e1基本掌握"
	vtUntested = "\U0001f504待测试"
)

func TestFilename(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, time.March, 15, 9, 8, 7, 0, time.UTC)
	tests := []struct {
		name   string
		prefix string
		date   time.Time
		major  int
		minor  int
		want   string
	}{
		{name: "中文与下划线前缀", prefix: "日语_学习进度档案", date: date, major: 1, minor: 0, want: "日语_学习进度档案_260315_v1.0.md"},
		{name: "日期逐位补零", prefix: "档案", date: time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC), major: 2, minor: 3, want: "档案_260102_v2.3.md"},
		{name: "多位数版本", prefix: "档案", date: date, major: 12, minor: 345, want: "档案_260315_v12.345.md"},
		{name: "零版本", prefix: "档案", date: date, major: 0, minor: 0, want: "档案_260315_v0.0.md"},
		{name: "空前缀", prefix: "", date: date, major: 7, minor: 8, want: "_260315_v7.8.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Filename(tt.prefix, tt.date, tt.major, tt.minor); got != tt.want {
				t.Errorf("Filename() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Filename 生成的名字必须能被 ParseFilename 还原回同一天与同一版本号。
func TestFilenameParseFilenameRoundTrip(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, time.March, 15, 18, 0, 0, 0, time.UTC)
	name := Filename("日语_学习进度档案", date, 12, 34)
	// 期望值手写，不用 Filename 的结果自证。
	if name != "日语_学习进度档案_260315_v12.34.md" {
		t.Fatalf("Filename() = %q, want 手写值", name)
	}
	gotDate, major, minor, err := ParseFilename(name)
	if err != nil {
		t.Fatalf("ParseFilename(%q) 报错：%v", name, err)
	}
	// time.Parse("060102") 得到 UTC 当日零点，只比对日历字段。
	if gotDate.Year() != 2026 || gotDate.Month() != time.March || gotDate.Day() != 15 {
		t.Errorf("日期 = %v, want 2026-03-15", gotDate)
	}
	if gotDate.Hour() != 0 || gotDate.Minute() != 0 || gotDate.Second() != 0 {
		t.Errorf("解析结果应为当日零点，got %v", gotDate)
	}
	if major != 12 || minor != 34 {
		t.Errorf("版本 = %d.%d, want 12.34", major, minor)
	}
}

func TestParseFilename(t *testing.T) {
	t.Parallel()
	day0315 := time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		filename  string
		wantDate  time.Time
		wantMajor int
		wantMinor int
		wantErrKW string
	}{
		{name: "标准前缀", filename: "日语学习进度档案_260315_v1.2.md", wantDate: day0315, wantMajor: 1, wantMinor: 2},
		{name: "多位数版本", filename: "x_260315_v12.345.md", wantDate: day0315, wantMajor: 12, wantMinor: 345},
		{name: "前缀不同但结尾匹配仍成功", filename: "完全不同的前缀_260315_v3.4.md", wantDate: day0315, wantMajor: 3, wantMinor: 4},
		{name: "前缀自带版本号只认结尾", filename: "a_v9.9_260315_v5.6.md", wantDate: day0315, wantMajor: 5, wantMinor: 6},
		{name: "两位年份99归到19xx", filename: "x_991231_v1.0.md", wantDate: time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC), wantMajor: 1, wantMinor: 0},
		{name: "两位年份69归到19xx", filename: "x_691231_v1.0.md", wantDate: time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC), wantMajor: 1, wantMinor: 0},
		{name: "缺版本后缀", filename: "日语学习进度档案_260315.md", wantErrKW: "invalid archive filename"},
		{name: "缺md扩展名", filename: "日语学习进度档案_260315_v1.2", wantErrKW: "invalid archive filename"},
		{name: "结尾多余字符", filename: "日语学习进度档案_260315_v1.2.md.bak", wantErrKW: "invalid archive filename"},
		{name: "空字符串", filename: "", wantErrKW: "invalid archive filename"},
		// 正则只吃 (\d+)，版本号含字母时整行不匹配，不会出现 "版本号非数字" 的解析错误。
		{name: "版本非数字正则不匹配", filename: "x_260315_vab.cd.md", wantErrKW: "invalid archive filename"},
		{name: "月份13", filename: "x_991399_v1.0.md", wantErrKW: "invalid date in filename"},
		{name: "全零日期", filename: "x_000000_v1.0.md", wantErrKW: "invalid date in filename"},
		{name: "日为零", filename: "x_260300_v1.0.md", wantErrKW: "invalid date in filename"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDate, major, minor, err := ParseFilename(tt.filename)
			if tt.wantErrKW != "" {
				if err == nil {
					t.Fatalf("ParseFilename(%q) 应报错", tt.filename)
				}
				if !strings.Contains(err.Error(), tt.wantErrKW) {
					t.Errorf("错误信息 = %q, 应含 %q", err.Error(), tt.wantErrKW)
				}
				// 所有错误路径都返回零值日期与 0 版本。
				if !gotDate.IsZero() || major != 0 || minor != 0 {
					t.Errorf("错误时应返回 (零时间, 0, 0)，got (%v, %d, %d)", gotDate, major, minor)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFilename(%q) 意外报错：%v", tt.filename, err)
			}
			if !gotDate.Equal(tt.wantDate) {
				t.Errorf("日期 = %v, want %v", gotDate, tt.wantDate)
			}
			if major != tt.wantMajor || minor != tt.wantMinor {
				t.Errorf("版本 = %d.%d, want %d.%d", major, minor, tt.wantMajor, tt.wantMinor)
			}
		})
	}
}

func TestNextVersion(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.March, 15, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		latestDate  time.Time
		latestMajor int
		latestMinor int
		today       time.Time
		majorBump   bool
		wantMajor   int
		wantMinor   int
	}{
		{name: "新日重置为1.0", latestDate: time.Date(2026, 3, 14, 23, 59, 59, 0, time.UTC), latestMajor: 4, latestMinor: 7, today: base, wantMajor: 1, wantMinor: 0},
		{name: "同日小版本加一", latestDate: base, latestMajor: 2, latestMinor: 3, today: time.Date(2026, 3, 15, 22, 0, 0, 0, time.UTC), wantMajor: 2, wantMinor: 4},
		{name: "同日大版本升级", latestDate: base, latestMajor: 2, latestMinor: 3, today: base, majorBump: true, wantMajor: 3, wantMinor: 0},
		{name: "新日优先于大版本升级", latestDate: time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC), latestMajor: 9, latestMinor: 9, today: base, majorBump: true, wantMajor: 1, wantMinor: 0},
		{name: "跨年同月同日仍算新日", latestDate: time.Date(2025, 3, 15, 8, 0, 0, 0, time.UTC), latestMajor: 5, latestMinor: 5, today: base, wantMajor: 1, wantMinor: 0},
		{name: "同日不同时刻小版本加一", latestDate: time.Date(2026, 3, 15, 0, 0, 1, 0, time.UTC), latestMajor: 1, latestMinor: 0, today: time.Date(2026, 3, 15, 23, 59, 59, 0, time.UTC), wantMajor: 1, wantMinor: 1},
		{name: "跨日差一秒算新日", latestDate: time.Date(2026, 3, 14, 23, 59, 59, 0, time.UTC), latestMajor: 3, latestMinor: 2, today: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), wantMajor: 1, wantMinor: 0},
		{name: "零值latestDate与非零today算新日", latestDate: time.Time{}, latestMajor: 8, latestMinor: 8, today: base, wantMajor: 1, wantMinor: 0},
		// 两个零值时间的 Year/Month/Day 都是 1/1/1，SameDay 判 true，
		// 因此不会走"新日重置"，而是沿用 latestMajor 做 minor+1 或 major+1。
		{name: "零值时间被判同日_仅minor加一", latestDate: time.Time{}, today: time.Time{}, latestMajor: 0, latestMinor: 0, wantMajor: 0, wantMinor: 1},
		{name: "零值时间被判同日_majorBump生效", latestDate: time.Time{}, today: time.Time{}, latestMajor: 5, latestMinor: 2, majorBump: true, wantMajor: 6, wantMinor: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor := NextVersion(tt.latestDate, tt.latestMajor, tt.latestMinor, tt.today, tt.majorBump)
			if major != tt.wantMajor || minor != tt.wantMinor {
				t.Errorf("NextVersion() = (%d, %d), want (%d, %d)", major, minor, tt.wantMajor, tt.wantMinor)
			}
		})
	}
}

func TestSameDay(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, time.March, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		a    time.Time
		b    time.Time
		want bool
	}{
		{name: "同一时刻", a: t0, b: t0, want: true},
		{name: "同年同月同日不同时刻", a: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), b: time.Date(2026, 3, 15, 23, 59, 59, 0, time.UTC), want: true},
		{name: "相邻日", a: t0, b: time.Date(2026, 3, 16, 10, 30, 0, 0, time.UTC), want: false},
		{name: "跨月", a: time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), b: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), want: false},
		{name: "跨年", a: time.Date(2025, 12, 31, 12, 0, 0, 0, time.UTC), b: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), want: false},
		{name: "同月同日不同年", a: time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC), b: t0, want: false},
		// SameDay 直接比较各自的 Year/Month/Day，不做时区归一：同一瞬间在
		// UTC 是 3/15、在 UTC+8 已是 3/16，仍判 false。
		{name: "同一瞬间不同时区各自日历日不同", a: time.Date(2026, 3, 15, 23, 0, 0, 0, time.UTC), b: time.Date(2026, 3, 16, 7, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameDay(tt.a, tt.b); got != tt.want {
				t.Errorf("SameDay() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAddChangelogEntry(t *testing.T) {
	t.Parallel()
	// 统计值全部按 review 包规则手算写死，不调用 CountByStatus 生成期望。
	// CountByStatus 优先级：已掌握(Status=🟢 或 RC>=5且EC==0) > 待测试(Status=🔄 或 RC==0)
	//   > 待巩固(Status=🔴 或 EC/RC>=0.30) > 其余基本掌握。
	// TotalErrors = 各词 ErrorCount 之和；CountNailHouseholds = EC>=5 或
	//   (RC>0 且 EC/RC<0.50 且 EC>0) 的词数。
	//   w1 すし RC6  EC0 → 已掌握；钉子户否
	//   w2 みず RC3  EC0 → 基本掌握；钉子户否
	//   w3 はし RC10 EC4 → 0.40>=0.30 待巩固；0.40<0.50且EC>0 → 钉子户
	//   w4 なつ RC0  EC0 Status=🔄 → 待测试；钉子户否
	//   w5 つき RC5  EC5 → 1.0>=0.30 待巩固；EC>=5 → 钉子户
	//   w6 ほし RC4  EC1 → 0.25<0.30 基本掌握；0.25<0.50且EC>0 → 钉子户
	// 汇总：Total=6, 已掌握=1, 基本掌握=2, 待巩固=2, 待测试=1,
	//       Errors=0+0+4+0+5+1=10, 钉子户=3。
	arc := &Archive{
		Language: "ja",
		Groups: []review.WordGroup{
			{Title: "第1课", Words: []review.Word{
				{Word: "すし", Definition: "寿司", ReviewCount: 6, ErrorCount: 0, ConsecutiveCorrect: 6, LastReview: "03/14", Status: vtMastered, Group: "第1课"},
				{Word: "みず", Definition: "水", ReviewCount: 3, ErrorCount: 0, ConsecutiveCorrect: 3, LastReview: "03/13", Status: "", Group: "第1课"},
				{Word: "はし", Definition: "桥", ReviewCount: 10, ErrorCount: 4, ConsecutiveCorrect: 2, LastReview: "03/12", Status: "", Group: "第1课"},
				{Word: "なつ", Definition: "夏天", ReviewCount: 0, ErrorCount: 0, ConsecutiveCorrect: 0, LastReview: "", Status: vtUntested, Group: "第1课"},
			}},
			{Title: "第2课", Words: []review.Word{
				{Word: "つき", Definition: "月亮", ReviewCount: 5, ErrorCount: 5, ConsecutiveCorrect: 0, LastReview: "03/11", Status: "", Group: "第2课"},
				{Word: "ほし", Definition: "星星", ReviewCount: 4, ErrorCount: 1, ConsecutiveCorrect: 3, LastReview: "03/10", Status: vtBasic, Group: "第2课"},
			}},
		},
		Changelog: []ChangelogEntry{
			{Date: "260314", Version: "v1.1", Total: "4", Mastered: "1", Basic: "1", NeedsConsol: "1", Untested: "1", Errors: "4", NailHouse: "1", Description: "初始导入"},
		},
	}

	today := time.Date(2026, time.March, 15, 10, 30, 0, 0, time.UTC)
	AddChangelogEntry(arc, today, 1, 2, "新增第2课")
	// 十个字段全部手写；原有条目仍在最前。
	wantFirst := ChangelogEntry{
		Date: "260315", Version: "v1.2", Total: "6", Mastered: "1",
		Basic: "2", NeedsConsol: "2", Untested: "1", Errors: "10",
		NailHouse: "3", Description: "新增第2课",
	}
	wantOriginal := ChangelogEntry{
		Date: "260314", Version: "v1.1", Total: "4", Mastered: "1",
		Basic: "1", NeedsConsol: "1", Untested: "1", Errors: "4",
		NailHouse: "1", Description: "初始导入",
	}
	if len(arc.Changelog) != 2 {
		t.Fatalf("追加一次后条目数 = %d, want 2", len(arc.Changelog))
	}
	if arc.Changelog[0] != wantOriginal {
		t.Errorf("原有条目被改动或顺序不对：%+v", arc.Changelog[0])
	}
	if arc.Changelog[1] != wantFirst {
		t.Errorf("新条目不符：\n got %+v\nwant %+v", arc.Changelog[1], wantFirst)
	}

	// 第二次追加：Groups 不变，统计值相同，日期/版本/描述随入参变化。
	today2 := time.Date(2026, time.March, 16, 9, 0, 0, 0, time.UTC)
	AddChangelogEntry(arc, today2, 1, 3, "修订释义")
	wantSecond := ChangelogEntry{
		Date: "260316", Version: "v1.3", Total: "6", Mastered: "1",
		Basic: "2", NeedsConsol: "2", Untested: "1", Errors: "10",
		NailHouse: "3", Description: "修订释义",
	}
	wantAll := []ChangelogEntry{wantOriginal, wantFirst, wantSecond}
	if !reflect.DeepEqual(arc.Changelog, wantAll) {
		t.Errorf("多次追加顺序/字段不符：\n got %+v\nwant %+v", arc.Changelog, wantAll)
	}
	// 追加只动 Changelog，不改 Groups。
	if len(arc.Groups) != 2 || len(arc.Groups[0].Words) != 4 || len(arc.Groups[1].Words) != 2 {
		t.Errorf("Groups 不应被改动：%+v", arc.Groups)
	}
}
