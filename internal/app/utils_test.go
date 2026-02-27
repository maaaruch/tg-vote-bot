package app

import "testing"

func TestSplitPipeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		n    int
		want []string
	}{
		{"basic", "Title | Pass", 2, []string{"Title", "Pass"}},
		{"trim", "  A   |   B  ", 2, []string{"A", "B"}},
		{"keep_remainder", "A|B|C", 2, []string{"A", "B|C"}},
		{"three", "A | B | C", 3, []string{"A", "B", "C"}},
		{"empty_parts_removed", "A||B", 3, []string{"A", "B"}},
		{"leading_empty", " | P", 2, []string{"P"}},
		{"trailing_empty", "T | ", 2, []string{"T"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := splitPipeArgs(tt.s, tt.n)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got=%d want=%d, got=%v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("idx=%d got=%q want=%q (got=%v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func FuzzSplitPipeArgs(f *testing.F) {
	seeds := []struct {
		s string
		n int
	}{
		{"A|B", 2},
		{"A||B", 3},
		{"  A | B | C  ", 3},
		{"|x|", 4},
		{"no pipes", 2},
	}
	for _, s := range seeds {
		f.Add(s.s, s.n)
	}

	f.Fuzz(func(t *testing.T, s string, n int) {
		if n < 0 {
			n = -n
		}
		if n > 20 {
			n = 20
		}

		got := splitPipeArgs(s, n)

		// Инварианты: не паникует, элементы не пустые/не с пробелами по краям,
		// и не может вернуть больше n элементов.
		if n == 0 && len(got) != 0 {
			t.Fatalf("n=0 => expected empty, got=%v", got)
		}
		if n > 0 && len(got) > n {
			t.Fatalf("len(got)=%d > n=%d (got=%v)", len(got), n, got)
		}
		for _, p := range got {
			if p == "" {
				t.Fatalf("empty part in %v", got)
			}
			if p != string([]byte(p)) { // тупо чтобы компилер не ругался на “неиспользуемое”
				t.Fatalf("impossible")
			}
			// trim check
			if p[0] == ' ' || p[len(p)-1] == ' ' {
				t.Fatalf("not trimmed: %q", p)
			}
		}
	})
}
