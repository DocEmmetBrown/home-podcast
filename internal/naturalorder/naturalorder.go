// Package naturalorder provides digit-aware ordering for library paths so that
// numbered files such as book chapters sort as humans expect.
package naturalorder

import (
	"strings"
	"unicode"
)

// Less reports whether the slash-separated path a sorts before b.
func Less(a, b string) bool {
	return Compare(a, b) < 0
}

// Compare returns -1, 0 or 1 comparing two slash-separated paths segment by
// segment, treating runs of digits as numbers ("chapter 2" before "chapter 10").
func Compare(a, b string) int {
	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")

	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareSegment(as[i], bs[i]); c != 0 {
			return c
		}
	}

	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func compareSegment(a, b string) int {
	ar := []rune(a)
	br := []rune(b)

	i, j := 0, 0
	for i < len(ar) && j < len(br) {
		if isDigit(ar[i]) && isDigit(br[j]) {
			iStart, jStart := i, j
			for i < len(ar) && isDigit(ar[i]) {
				i++
			}
			for j < len(br) && isDigit(br[j]) {
				j++
			}

			an := strings.TrimLeft(string(ar[iStart:i]), "0")
			bn := strings.TrimLeft(string(br[jStart:j]), "0")
			if len(an) != len(bn) {
				if len(an) < len(bn) {
					return -1
				}
				return 1
			}
			if c := strings.Compare(an, bn); c != 0 {
				return c
			}
			continue
		}

		ac := unicode.ToLower(ar[i])
		bc := unicode.ToLower(br[j])
		if ac != bc {
			if ac < bc {
				return -1
			}
			return 1
		}
		i++
		j++
	}

	switch {
	case len(ar)-i < len(br)-j:
		return -1
	case len(ar)-i > len(br)-j:
		return 1
	default:
		// Identical apart from case or zero padding: fall back to a stable order.
		return strings.Compare(a, b)
	}
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}
