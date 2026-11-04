package sentence

// ReviewBank 回炉变形句库。
type ReviewBank struct {
	Version   int              `json:"version"`
	Updated   string           `json:"updated"`
	Note      string           `json:"note"`
	Sentences []ReviewSentence `json:"sentences"`
}

// ReviewSentence 一条回炉变形句。
//
// Word 是锚点词（档案里的完整词形「かな(漢字)」），挑句时用它去查档案判断
// 这个词现在还算不算「回炉」——最近刚复习过的就跳过，别浪费名额。
// Prototype 指回原型句，sentence-lint 靠它确认变形句是合法改造而非凭空编造。
type ReviewSentence struct {
	ID          string `json:"id"`
	Word        string `json:"word"`
	Pos         string `json:"pos"`
	Sub         string `json:"sub"`
	Lesson      string `json:"lesson"`
	Source      string `json:"source"`
	SourceDoc   string `json:"source_doc"`
	Type        string `json:"type"`
	Prototype   string `json:"prototype"`
	PrototypeID string `json:"prototype_id"`
	Answer      string `json:"answer"`
	Chinese     string `json:"chinese"`
}

// ProtoSuggest 给待补词推荐的原型句骨架（sentence-review-todo 的 JSON 输出）。
type ProtoSuggest struct {
	ID      string `json:"id"`
	Lesson  string `json:"lesson"`
	Source  string `json:"source"`
	Answer  string `json:"answer"`
	Chinese string `json:"chinese"`
}
