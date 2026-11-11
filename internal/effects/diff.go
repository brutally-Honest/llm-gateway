// Package effects runs the file and read sensors of spec 014: checkpoints of a session's
// watched folders on each stored hook event, and read effects from stored hook events.
// It knows no client: a profile hands it a core.EffectRules.
package effects

import (
	"fmt"
	"strings"
)

// DiffMaxLines is the most lines a diff holds before a "diff truncated" line ends it.
const DiffMaxLines = 2000

// diffContext is the unchanged lines shown around each change.
const diffContext = 3

// maxEdits bounds the Myers search. Past it the changed middle is shown as removed then
// added whole, which is still a correct diff, so a huge rewrite costs bounded memory.
const maxEdits = 1000

type opKind byte

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

// op is one line of the edit script: a line of a (equal, delete) or of b (insert).
type op struct {
	kind opKind
	a, b int // line indexes in a and b at this op
}

// Diff returns a unified line diff from before to after, with diffContext lines of
// context, and whether it was cut at maxLines (a last line then says so). Equal inputs
// give "".
func Diff(before, after string, maxLines int) (string, bool) {
	a, b := splitLines(before), splitLines(after)
	ops := editScript(a, b)
	return render(ops, a, b, maxLines)
}

// splitLines splits s into lines without their newline; a last line with no newline is
// kept, and "" is no line at all.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// editScript is the full edit script from a to b: the common prefix and suffix as equal
// lines, and the Myers shortest script for the middle.
func editScript(a, b []string) []op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := make([]op, 0, len(a)+len(b))
	for i := range pre {
		ops = append(ops, op{opEqual, i, i})
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	mid, ok := myers(ma, mb, maxEdits)
	if !ok {
		mid = mid[:0]
		for i := range ma {
			mid = append(mid, op{opDelete, i, 0})
		}
		for j := range mb {
			mid = append(mid, op{opInsert, len(ma), j})
		}
	}
	for _, o := range mid {
		ops = append(ops, op{o.kind, o.a + pre, o.b + pre})
	}
	for i := range suf {
		ops = append(ops, op{opEqual, len(a) - suf + i, len(b) - suf + i})
	}
	return ops
}

// span is the values of the Myers V array for diagonals lo..lo+len(vals)-1.
type span struct {
	lo   int
	vals []int
}

func (s span) at(k int) int { return s.vals[k-s.lo] }

// myers is the shortest edit script from a to b (Myers 1986), or false when it needs
// more than limit edits.
func myers(a, b []string, limit int) ([]op, bool) {
	n, m := len(a), len(b)
	total := n + m
	if total == 0 {
		return nil, true
	}
	off := total + 1
	v := make([]int, 2*total+3)
	var trace []span
	for d := 0; d <= total; d++ {
		if d > limit {
			return nil, false
		}
		trace = append(trace, span{lo: -d - 1, vals: append([]int(nil), v[off-d-1:off+d+2]...)})
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, n, m), true
			}
		}
	}
	return nil, false
}

// backtrack walks the saved V arrays from the end back to the start and returns the
// edit script in order.
func backtrack(trace []span, n, m int) []op {
	var rev []op
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v.at(k-1) < v.at(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v.at(pk)
		py := px - pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, op{opEqual, x, y})
		}
		if d > 0 {
			if x == px {
				rev = append(rev, op{opInsert, px, py})
			} else {
				rev = append(rev, op{opDelete, px, py})
			}
		}
		x, y = px, py
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// render writes the edit script as unified hunks.
func render(ops []op, a, b []string, maxLines int) (string, bool) {
	var out []string
	truncated := false
	emit := func(s string) bool {
		if len(out) >= maxLines {
			truncated = true
			return false
		}
		out = append(out, s)
		return true
	}
	i := 0
	for i < len(ops) {
		// Find the next change.
		for i < len(ops) && ops[i].kind == opEqual {
			i++
		}
		if i == len(ops) {
			break
		}
		start := max(0, i-diffContext)
		// Extend the hunk while changes are within 2*context of each other.
		end := i
		for end < len(ops) {
			if ops[end].kind != opEqual {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == opEqual {
				run++
			}
			if run == len(ops) || run-end > 2*diffContext {
				end = min(len(ops), end+diffContext)
				break
			}
			end = run
		}
		hunk := ops[start:end]
		aStart, bStart := hunk[0].a, hunk[0].b
		var aLen, bLen int
		for _, o := range hunk {
			switch o.kind {
			case opEqual:
				aLen++
				bLen++
			case opDelete:
				aLen++
			case opInsert:
				bLen++
			}
		}
		if !emit(fmt.Sprintf("@@ -%s +%s @@", hunkRange(aStart, aLen), hunkRange(bStart, bLen))) {
			break
		}
		ok := true
		for _, o := range hunk {
			var line string
			switch o.kind {
			case opEqual:
				line = " " + a[o.a]
			case opDelete:
				line = "-" + a[o.a]
			case opInsert:
				line = "+" + b[o.b]
			}
			if ok = emit(line); !ok {
				break
			}
		}
		if !ok {
			break
		}
		i = end
	}
	if truncated {
		out = append(out, "… diff truncated")
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, "\n") + "\n", truncated
}

// hunkRange is a unified diff range: 1-based start and length; an empty range starts at
// the line before it.
func hunkRange(start, n int) string {
	if n == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if n == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

// LineCounts is how many lines the diff from before to after adds and removes, by the same
// edit script Diff renders, so a line count never disagrees with the diff shown.
func LineCounts(before, after string) (added, removed int) {
	for _, o := range editScript(splitLines(before), splitLines(after)) {
		switch o.kind {
		case opInsert:
			added++
		case opDelete:
			removed++
		}
	}
	return added, removed
}
