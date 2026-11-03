package sentence

// PlanSentence is a sentence exercise in a review plan.
type PlanSentence struct {
	Number  int    `json:"number"`
	Chinese string `json:"chinese"`
	Answer  string `json:"answer"`
}
