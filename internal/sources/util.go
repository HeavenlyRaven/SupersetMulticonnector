package sources

import "strconv"

// atoiOrZero returns 0 for a non-numeric string rather than erroring, so
// callers can feed the result straight to validate.Port and get a single
// consistent CodeInvalidPort error for both "not a number" and
// "out of range" — port 0 is invalid either way.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
