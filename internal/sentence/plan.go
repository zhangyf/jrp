package sentence

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxBankSeq 已有条目里 L<n><tag>xxx 的最大序号，新条目从它后面接着编。
func MaxBankSeq(existing []BankSentence, lesson int, tag string) int {
	prefix := fmt.Sprintf("L%d%s", lesson, tag)
	max := 0
	for _, s := range existing {
		if !strings.HasPrefix(s.ID, prefix) {
			continue
		}
		m := bankIDRe.FindStringSubmatch(s.ID)
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[3]); err == nil && n > max {
			max = n
		}
	}
	return max
}

// ImportPlan 一课的导入方案。只有两种结局：追加 / 跳过。
//
// 曾经试过「很像的旧句就覆盖」，2026-10-16 被老师否掉了：
// 「帰らなければ なりません」和「帰らなくては なりません」意思一样、句式不同，
// 课本可能两句都教 —— 相似度说得出"很像"，说不出"就是同一句"，
// 自动覆盖会把真句子盖没。所以现在的规则就这么简单。
type ImportPlan struct {
	Added   []BankSentence `json:"added"`
	Skipped int            `json:"skipped"`
}

// BuildImportPlan 把抽出来的行变成导入方案：
//
//  1. 句库里已经有完全一样的一句（按 normSentence 比 —— 句号、空格、全角半角、
//     斜杠差异都算同一句）→ 跳过，所以重复导入不会涨条目
//  2. 其他一律追加，ID 接着已有最大号编
//
// 不做覆盖、不做相似度判断、不做待定清单 —— 老师明确不要这套复杂度。
func BuildImportPlan(rows []ImportedRow, lesson int, kind LessonDocKind, doc string, existing []BankSentence) ImportPlan {
	have := make(map[string]bool, len(existing))
	for _, s := range existing {
		if n := Norm(s.Answer); n != "" {
			have[n] = true
		}
	}

	plan := ImportPlan{}
	seq := MaxBankSeq(existing, lesson, kind.IDTag)
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		n := Norm(r.JP)
		if n == "" || seen[n] || have[n] {
			plan.Skipped++
			continue
		}
		seen[n] = true
		seq++
		plan.Added = append(plan.Added, NewBankSentence(lesson, kind, doc, seq, r))
	}
	return plan
}

func NewBankSentence(lesson int, kind LessonDocKind, doc string, seq int, r ImportedRow) BankSentence {
	return BankSentence{
		ID:        fmt.Sprintf("L%d%s%03d", lesson, kind.IDTag, seq),
		Lesson:    LessonLabelOf(lesson),
		Source:    kind.Source,
		SourceDoc: doc,
		Type:      "original",
		Answer:    r.JP,
		Chinese:   r.CN,
	}
}
