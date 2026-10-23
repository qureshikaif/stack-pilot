// Package version parses tool versions and checks them against the constraint
// syntaxes found in project files: npm semver ranges (^, ~, >=, x), PEP 440
// specifiers (~=, ==3.12.*, !=), and plain pins like "22" or "3.12.1".
package version

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnsupported means the constraint is valid for some tool (e.g. "lts/*")
// but cannot be evaluated without extra knowledge.
var ErrUnsupported = errors.New("unsupported version constraint")

var versionRe = regexp.MustCompile(`\d+(?:\.\d+){0,2}`)

// Extract returns the first version-looking substring of s, e.g.
// "git version 2.39.3 (Apple Git-145)" -> "2.39.3".
func Extract(s string) string {
	return versionRe.FindString(s)
}

// parts parses "v22.14.0" into [22 14 0]. Wildcard segments ("x", "*") end the
// version and set wildcard; "3.12.*" -> [3 12], true.
func parts(s string) (nums []int, wildcard bool, err error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if s == "" || s == "*" || s == "x" || s == "X" {
		return nil, true, nil
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "x" || seg == "X" || seg == "*" {
			return nums, true, nil
		}
		n, err := strconv.Atoi(seg)
		if err != nil {
			return nil, false, ErrUnsupported
		}
		nums = append(nums, n)
	}
	return nums, false, nil
}

func compare(a, b []int) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// prefixMatch reports whether have starts with every segment of spec.
func prefixMatch(have, spec []int) bool {
	for i, n := range spec {
		if i >= len(have) || have[i] != n {
			return false
		}
	}
	return true
}

// bump returns the exclusive upper bound obtained by incrementing segment i
// and dropping everything after it: bump([3 11 2], 1) -> [3 12].
func bump(v []int, i int) []int {
	out := append([]int(nil), v[:i+1]...)
	out[i]++
	return out
}

var operators = []string{">=", "<=", "==", "!=", "~=", ">", "<", "=", "^", "~"}

func splitOp(tok string) (op, ver string) {
	for _, o := range operators {
		if strings.HasPrefix(tok, o) {
			return o, tok[len(o):]
		}
	}
	return "", tok
}

func satisfiesOne(have []int, tok string) (bool, error) {
	op, raw := splitOp(tok)
	spec, wildcard, err := parts(raw)
	if err != nil {
		return false, err
	}
	switch op {
	case "", "=", "==":
		if wildcard || op == "" {
			// "22", "20.x", "3.12.*" all mean "this prefix".
			return prefixMatch(have, spec), nil
		}
		return compare(have, spec) == 0, nil
	case "!=":
		if wildcard {
			return !prefixMatch(have, spec), nil
		}
		return compare(have, spec) != 0, nil
	case ">=":
		return compare(have, spec) >= 0, nil
	case ">":
		return compare(have, spec) > 0, nil
	case "<=":
		return compare(have, spec) <= 0 || (wildcard && prefixMatch(have, spec)), nil
	case "<":
		return compare(have, spec) < 0, nil
	case "^":
		if len(spec) == 0 {
			return true, nil
		}
		i := 0
		for i < len(spec)-1 && spec[i] == 0 {
			i++
		}
		return compare(have, spec) >= 0 && compare(have, bump(spec, i)) < 0, nil
	case "~":
		if len(spec) == 0 {
			return true, nil
		}
		i := min(1, len(spec)-1)
		return compare(have, spec) >= 0 && compare(have, bump(spec, i)) < 0, nil
	case "~=":
		if len(spec) < 2 {
			return false, ErrUnsupported
		}
		return compare(have, spec) >= 0 && compare(have, bump(spec, len(spec)-2)) < 0, nil
	}
	return false, ErrUnsupported
}

// tokens splits one range ("">= 20 <23", ">=3.11,<3.13") into comparators,
// re-attaching operators that were separated from their version by a space.
func tokens(r string) []string {
	var out []string
	pendingOp := ""
	for _, f := range strings.Fields(strings.ReplaceAll(r, ",", " ")) {
		if op, rest := splitOp(f); op != "" && rest == "" {
			pendingOp = op
			continue
		}
		out = append(out, pendingOp+f)
		pendingOp = ""
	}
	return out
}

// Satisfies reports whether version have meets constraint. Alternatives may be
// joined with "||"; comparators within an alternative are ANDed.
func Satisfies(have, constraint string) (bool, error) {
	hv, _, err := parts(Extract(have))
	if err != nil || len(hv) == 0 {
		return false, ErrUnsupported
	}
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true, nil
	}
	for _, alt := range strings.Split(constraint, "||") {
		ok := true
		toks := tokens(alt)
		if len(toks) == 0 {
			return true, nil
		}
		for _, tok := range toks {
			sat, err := satisfiesOne(hv, tok)
			if err != nil {
				return false, err
			}
			ok = ok && sat
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
