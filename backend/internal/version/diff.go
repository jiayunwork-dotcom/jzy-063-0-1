// Package version implements line-level diffing between two configuration
// versions. It is a straightforward LCS diff over text lines.
package version

import (
	"strings"

	"configplat/internal/model"
)

// DiffVersions computes a line-level diff between two version snapshots.
//
// Each version stores values per environment; the comparison is performed on
// the chosen environment's raw text (env "" means the single/first env).
// Output rows are tagged add / remove / equal with 1-based line numbers.
func DiffVersions(from, to map[string]string, env string) []model.DiffLine {
	a := pickLines(from, env)
	b := pickLines(to, env)
	return LCS(a, b)
}

func pickLines(values map[string]string, env string) []string {
	var text string
	if env != "" {
		text = values[env]
	} else {
		for _, v := range values {
			text = v
			break
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// LCS returns the line diff using the classic dynamic-programming LCS.
// Removed lines (only in a) come before added lines (only in b) inside each
// changed hunk, which keeps the output readable for config files.
func LCS(a, b []string) []model.DiffLine {
	n, m := len(a), len(b)
	// lcs[i][j] = length of LCS of a[i:] and b[j:]
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	type op struct {
		kind               string // '=' , '-' , '+'
		text               string
		oldNum, newNum     int
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{"=", a[i], i + 1, j + 1})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{"-", a[i], i + 1, 0})
			i++
		default:
			ops = append(ops, op{"+", b[j], 0, j + 1})
			j++
		}
	}
	for i < n {
		ops = append(ops, op{"-", a[i], i + 1, 0})
		i++
	}
	for j < m {
		ops = append(ops, op{"+", b[j], 0, j + 1})
		j++
	}

	// Reorder each run of '-'/'+' so removals precede additions (they already
	// do given the backtrace above when LCS prefers removal on ties, but a
	// plain sequence is fine for the UI too).
	out := make([]model.DiffLine, 0, len(ops))
	for _, o := range ops {
		var t string
		switch o.kind {
		case "-":
			t = "remove"
		case "+":
			t = "add"
		default:
			t = "equal"
		}
		out = append(out, model.DiffLine{
			Type: t, OldNum: o.oldNum, NewNum: o.newNum, Text: o.text,
		})
	}
	return out
}

// Summary counts add/remove/equal lines and reports whether anything changed.
type Summary struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed bool `json:"changed"`
}

func Summarize(lines []model.DiffLine) Summary {
	var s Summary
	for _, l := range lines {
		switch l.Type {
		case "add":
			s.Added++
		case "remove":
			s.Removed++
		}
	}
	s.Changed = s.Added > 0 || s.Removed > 0
	return s
}
