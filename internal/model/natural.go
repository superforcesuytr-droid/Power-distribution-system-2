package model

// NaturalLess orders identifiers the way an engineer reads them rather than the
// way a computer sorts them: FAC2 comes before FAC10, and MCB-9 before MCB-10,
// where plain alphabetical order would put 10 immediately after 1.
//
// Comparison ignores spaces and is case-insensitive, so a board entered as
// "FAC 2" still lands between FAC1 and FAC3 instead of ahead of both.
func NaturalLess(a, b string) bool {
	ai, bi := 0, 0
	for {
		for ai < len(a) && a[ai] == ' ' {
			ai++
		}
		for bi < len(b) && b[bi] == ' ' {
			bi++
		}
		if ai >= len(a) || bi >= len(b) {
			break
		}
		ac, bc := a[ai], b[bi]
		if isDigit(ac) && isDigit(bc) {
			as, bs := ai, bi
			for ai < len(a) && isDigit(a[ai]) {
				ai++
			}
			for bi < len(b) && isDigit(b[bi]) {
				bi++
			}
			an, bn := trimZeros(a[as:ai]), trimZeros(b[bs:bi])
			// A longer run of digits, once leading zeros are gone, is the
			// larger number; equal lengths compare digit by digit.
			if len(an) != len(bn) {
				return len(an) < len(bn)
			}
			if an != bn {
				return an < bn
			}
			continue
		}
		al, bl := lower(ac), lower(bc)
		if al != bl {
			return al < bl
		}
		ai++
		bi++
	}
	// One ran out: the shorter remainder sorts first. Fall back to the raw
	// strings when both are exhausted, so ordering stays stable and total.
	ra, rb := len(a)-ai, len(b)-bi
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func trimZeros(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}
