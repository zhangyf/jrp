package plan

import (
	"testing"

	"github.com/zhangyf/jrp/internal/offline"
)

func mkCapPlan(n int) *offline.ReviewPlan {
	p := &offline.ReviewPlan{}
	for i := 1; i <= n; i++ {
		p.Words = append(p.Words, offline.PlanWord{Number: i, Word: "w"})
	}
	return p
}

func TestCapWordsTruncatesAndRenumbers(t *testing.T) {
	p := mkCapPlan(10)
	if got := CapWords(p, 3); got != 10 {
		t.Fatalf("returned total = %d, want 10", got)
	}
	if len(p.Words) != 3 {
		t.Fatalf("len(Words) = %d, want 3", len(p.Words))
	}
	for i, w := range p.Words {
		if w.Number != i+1 {
			t.Errorf("Words[%d].Number = %d, want %d", i, w.Number, i+1)
		}
	}
}

func TestCapWordsNoLimit(t *testing.T) {
	p := mkCapPlan(4)
	if got := CapWords(p, 0); got != 4 || len(p.Words) != 4 {
		t.Fatalf("n=0 should keep all words: total=%d len=%d", got, len(p.Words))
	}
	for i, w := range p.Words {
		if w.Number != i+1 {
			t.Errorf("words were renumbered with no limit: Words[%d].Number=%d", i, w.Number)
		}
	}
}

func TestCapWordsAboveTotal(t *testing.T) {
	p := mkCapPlan(2)
	if got := CapWords(p, 5); got != 2 || len(p.Words) != 2 {
		t.Fatalf("n>total should be a no-op: total=%d len=%d", got, len(p.Words))
	}
}

func TestCapWordsNil(t *testing.T) {
	if got := CapWords(nil, 5); got != 0 {
		t.Fatalf("nil plan total = %d, want 0", got)
	}
}
