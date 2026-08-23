package naturalorder

import (
	"sort"
	"testing"
)

func TestCompareNumericSegments(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"chapter 2.mp3", "chapter 10.mp3", -1},
		{"chapter 10.mp3", "chapter 9.mp3", 1},
		{"chapter 01.mp3", "chapter 1.mp3", -1},
		{"track1.mp3", "track1.mp3", 0},
		{"Book/03 - Intro.mp3", "Book/12 - Outro.mp3", -1},
		{"book/ch1.mp3", "album/ch1.mp3", 1},
		{"a.mp3", "b/c.mp3", -1},
		{"Alpha.mp3", "beta.mp3", -1},
	}

	for _, tc := range cases {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSortChapterOrder(t *testing.T) {
	paths := []string{
		"Book/Chapter 11.mp3",
		"Book/Chapter 2.mp3",
		"Book/Chapter 1.mp3",
		"Book/Chapter 20.mp3",
		"Book/Chapter 3.mp3",
	}
	sort.Slice(paths, func(i, j int) bool { return Less(paths[i], paths[j]) })

	want := []string{
		"Book/Chapter 1.mp3",
		"Book/Chapter 2.mp3",
		"Book/Chapter 3.mp3",
		"Book/Chapter 11.mp3",
		"Book/Chapter 20.mp3",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("sorted[%d] = %q, want %q (got %v)", i, paths[i], want[i], paths)
		}
	}
}
