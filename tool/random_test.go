package tool

import "testing"

// TestRandomNumericLength 校验生成字符串长度
func TestRandomNumericLength(t *testing.T) {
	for _, n := range []int{1, 5, 11, 20} {
		got := RandomNumeric(n)
		if len(got) != n {
			t.Fatalf("expected length %d, got %d (%q)", n, len(got), got)
		}
		for _, c := range got {
			if c < '0' || c > '9' {
				t.Fatalf("unexpected non-digit char %q in %q", c, got)
			}
		}
	}
}

// TestRandomNumericUniqueness 校验连续生成不重复
func TestRandomNumericUniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		s := RandomNumeric(11)
		if seen[s] {
			t.Fatalf("duplicate value %q at iteration %d", s, i)
		}
		seen[s] = true
	}
}

// TestRandomNumericZero 校验 0 长度返回空串
func TestRandomNumericZero(t *testing.T) {
	if s := RandomNumeric(0); s != "" {
		t.Fatalf("expected empty string, got %q", s)
	}
}
