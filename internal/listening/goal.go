package listening

// goal.go —— 听力进度条的「满格」目标值：解析、内置默认、合法性校验、回退选择。
//
// 老师定的满格口径（2026-11-05）：N5 = 4500 分钟（75h）、N4 = 6000 分钟（100h）。
// 这是进度条的参照值，不是官方标准 —— 听力没有词汇那种 800/1500 词的公认门槛，
// 这两个数参考了备考机构主流口径（到 N5 累计约 75h、到 N4 约 100h），外置在 COS
// 上随时能改（jrp listening-goal）。
//
// 纯函数、不碰 COS / Storage —— 装载缓存与 CLI 编排留在根包（跟 planconfig 同套路）。

import (
	"encoding/json"
	"log"
)

// 内置默认满格目标（分钟）。COS 上没配 / 配坏了就用这两个数。
const (
	DefaultN5GoalMinutes = 4500 // 75 小时
	DefaultN4GoalMinutes = 6000 // 100 小时
)

// MaxGoalMinutes 目标上限。写个 99999999 上去进度条永远趴在 0%，没意义，判坏回退。
const MaxGoalMinutes = 100000

// Goal 各等级的听力满格目标（分钟）。
type Goal struct {
	N5 int `json:"n5"`
	N4 int `json:"n4"`
}

// ParseGoal 解析配置。任何异常都返回零值 —— 零值 = 「没配」。
func ParseGoal(data []byte) (Goal, error) {
	if len(data) == 0 {
		return Goal{}, nil
	}
	var g Goal
	if err := json.Unmarshal(data, &g); err != nil {
		return Goal{}, err
	}
	return g, nil
}

// BuiltinGoal 内置默认目标。
func BuiltinGoal() Goal {
	return Goal{N5: DefaultN5GoalMinutes, N4: DefaultN4GoalMinutes}
}

// validGoalMinutes 值是否可用。必须为正（0 = 进度条永远 0%，没意义）且不超上限。
func validGoalMinutes(n int) bool { return n > 0 && n <= MaxGoalMinutes }

// ValidGoal 两个等级的值都合法才算合法。
func ValidGoal(g Goal) bool {
	return validGoalMinutes(g.N5) && validGoalMinutes(g.N4)
}

// ChooseGoal 在「COS 上读到的」和「内置的」之间做选择。
// 护栏全在这里：读失败、空的、解析坏、值越界，一律回退内置，进度条照画不误。
func ChooseGoal(cos Goal, err error) (Goal, string) {
	if err != nil {
		log.Printf("listening-goal: COS 配置读不了（%v），用内置默认", err)
		return BuiltinGoal(), "builtin"
	}
	if cos.N5 == 0 && cos.N4 == 0 {
		return BuiltinGoal(), "builtin" // 没配过
	}
	if !ValidGoal(cos) {
		log.Printf("listening-goal: COS 配置越界（n5=%d n4=%d，允许 1-%d 分钟），用内置默认",
			cos.N5, cos.N4, MaxGoalMinutes)
		return BuiltinGoal(), "builtin"
	}
	return cos, "cos"
}
