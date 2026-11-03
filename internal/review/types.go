package review

// Word represents a single vocabulary entry.
type Word struct {
	Word               string // target language word (e.g. すし)
	Definition         string // Chinese definition (e.g. 寿司)
	ReviewCount        int    // total review attempts
	ErrorCount         int    // total wrong answers
	ConsecutiveCorrect int    // consecutive correct since last error
	LastReview         string // last review date MM/DD
	Status             string // 🟢已掌握 / 🟡基本掌握 / 🔴待巩固 / 🔄待测试
	Group              string // group title (e.g. "第1课01 基础词（5/25）")
}

// WordGroup is a titled section of words in the archive.
type WordGroup struct {
	Title string
	Words []Word
}
