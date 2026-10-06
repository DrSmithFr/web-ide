package agent

import "strings"

// Hunk of a line diff: lines [A, A+AL) of the old text become [B, B+BL) of the new one.
type Hunk struct{ A, AL, B, BL int }

const diffLimit = 8000

// LineHunks are the hunks between the lines a (old) and b (new); nil, false when the changed
// middle is too big. The common head and tail are cut first: a big file with a small change
// stays cheap.
func LineHunks(a, b []string) ([]Hunk, bool) {
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	ma, mb := a[head:len(a)-tail], b[head:len(b)-tail]
	switch {
	case len(ma) == 0 && len(mb) == 0:
		return []Hunk{}, true
	case len(ma) == 0 || len(mb) == 0:
		return []Hunk{{head, len(ma), head, len(mb)}}, true
	case len(ma) > diffLimit || len(mb) > diffLimit:
		return nil, false
	}
	hunks := myers(ma, mb)
	for i := range hunks {
		hunks[i].A += head
		hunks[i].B += head
	}
	return hunks, true
}

// myers is the O(ND) diff of Myers: the shortest edit script, grouped into hunks.
func myers(a, b []string) []Hunk {
	n, m := len(a), len(b)
	max := n + m
	off := max + 1
	v := make([]int, 2*max+2)
	var trace [][]int
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int{}, v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
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
				return backtrack(trace, a, b, off, d)
			}
		}
	}
	return nil
}

// backtrack walks the trace back to mark the kept lines, then groups the rest into hunks.
func backtrack(trace [][]int, a, b []string, off, d int) []Hunk {
	keepA := make([]bool, len(a))
	keepB := make([]bool, len(b))
	x, y := len(a), len(b)
	for ; d > 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || k != d && v[off+k-1] < v[off+k+1] {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			x--
			y--
			keepA[x], keepB[y] = true, true
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		x--
		y--
		keepA[x], keepB[y] = true, true
	}
	var out []Hunk
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		if i < len(a) && j < len(b) && keepA[i] && keepB[j] {
			i++
			j++
			continue
		}
		h := Hunk{A: i, B: j}
		for i < len(a) && !keepA[i] {
			i++
		}
		for j < len(b) && !keepB[j] {
			j++
		}
		h.AL, h.BL = i-h.A, j-h.B
		out = append(out, h)
	}
	return out
}

// DiffLines is the unified diff of two texts with 2 lines of context, at most 400 lines.
// The gaps are '…' lines whose Text is a key the page translates.
func DiffLines(oldText, newText string) []DiffLine {
	a := strings.Split(oldText, "\n")
	b := strings.Split(newText, "\n")
	hunks, ok := LineHunks(a, b)
	if !ok {
		return []DiffLine{{T: "…", Text: "difference too large to show"}}
	}
	out := []DiffLine{}
	const ctx = 2
	lastA := -1
	for _, h := range hunks {
		from := max(h.A-ctx, lastA+1, 0)
		if lastA >= 0 && from > lastA+1 {
			out = append(out, DiffLine{T: "…"})
		} else if lastA < 0 && from > 0 {
			out = append(out, DiffLine{T: "…", Text: "line {n}", N: from + 1})
		}
		for i := from; i < h.A; i++ {
			out = append(out, DiffLine{T: " ", Text: a[i]})
		}
		for i := h.A; i < h.A+h.AL; i++ {
			out = append(out, DiffLine{T: "-", Text: a[i]})
		}
		for i := h.B; i < h.B+h.BL; i++ {
			out = append(out, DiffLine{T: "+", Text: b[i]})
		}
		end := min(h.A+h.AL+ctx, len(a))
		for i := h.A + h.AL; i < end; i++ {
			out = append(out, DiffLine{T: " ", Text: a[i]})
		}
		lastA = end - 1
		if len(out) > 400 {
			out = append(out[:400], DiffLine{T: "…", Text: "rest of the diff hidden"})
			break
		}
	}
	return out
}
