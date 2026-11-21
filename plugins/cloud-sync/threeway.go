package main

import (
	"slices"
	"strings"
)

// threeWayAutoMerge merges local and remote text against base at line granularity.
// Non-overlapping or identical edits are merged cleanly and return (merged, true).
// Any overlapping differing edit returns ("", false) so the caller can stage
// the document for ArcMD's interactive three-way merge dialog.
func threeWayAutoMerge(baseText, localText, remoteText string) (string, bool) {
	trailingNL := strings.HasSuffix(localText, "\n") || strings.HasSuffix(remoteText, "\n") || strings.HasSuffix(baseText, "\n")
	base := splitMergeLines(baseText)
	local := splitMergeLines(localText)
	remote := splitMergeLines(remoteText)

	if len(base) == 0 && len(local) > 0 && len(remote) > 0 {
		base = lcsMergeLines(local, remote)
	}

	b2l := matchMergeAnchors(base, local)
	b2r := matchMergeAnchors(base, remote)

	type anchor struct{ b, l, r int }
	var anchors []anchor
	for bi := range base {
		if b2l[bi] >= 0 && b2r[bi] >= 0 {
			anchors = append(anchors, anchor{b: bi, l: b2l[bi], r: b2r[bi]})
		}
	}

	var out []string
	pb, pl, pr := 0, 0, 0
	emitRegion := func(bEnd, lEnd, rEnd int) bool {
		bSlice := base[pb:bEnd]
		lSlice := local[pl:lEnd]
		rSlice := remote[pr:rEnd]
		if len(bSlice) == 0 && len(lSlice) == 0 && len(rSlice) == 0 {
			return true
		}
		lEqB := slices.Equal(lSlice, bSlice)
		rEqB := slices.Equal(rSlice, bSlice)
		lEqR := slices.Equal(lSlice, rSlice)
		switch {
		case lEqB && rEqB:
			out = append(out, bSlice...)
		case !lEqB && rEqB:
			out = append(out, lSlice...)
		case lEqB && !rEqB:
			out = append(out, rSlice...)
		case lEqR:
			out = append(out, lSlice...)
		default:
			return false
		}
		return true
	}

	i := 0
	for i < len(anchors) {
		a := anchors[i]
		if a.b > pb || a.l > pl || a.r > pr {
			if !emitRegion(a.b, a.l, a.r) {
				return "", false
			}
		}
		j := i + 1
		for j < len(anchors) &&
			anchors[j].b == anchors[j-1].b+1 &&
			anchors[j].l == anchors[j-1].l+1 &&
			anchors[j].r == anchors[j-1].r+1 {
			j++
		}
		runLen := j - i
		out = append(out, base[a.b:a.b+runLen]...)
		pb = a.b + runLen
		pl = a.l + runLen
		pr = a.r + runLen
		i = j
	}
	if pb < len(base) || pl < len(local) || pr < len(remote) {
		if !emitRegion(len(base), len(local), len(remote)) {
			return "", false
		}
	}

	if len(out) == 0 {
		return "", true
	}
	joined := strings.Join(out, "\n")
	if trailingNL {
		joined += "\n"
	}
	return joined, true
}

func splitMergeLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

func matchMergeAnchors(a, b []string) []int {
	m := make([]int, len(a))
	for i := range m {
		m[i] = -1
	}
	if len(a) == 0 || len(b) == 0 {
		return m
	}

	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		m[head] = head
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		m[len(a)-1-tail] = len(b) - 1 - tail
		tail++
	}

	ma := a[head : len(a)-tail]
	mb := b[head : len(b)-tail]
	if len(ma) == 0 || len(mb) == 0 || len(ma)*len(mb) > 4_000_000 {
		return m
	}

	dp := make([][]int32, len(ma)+1)
	for i := range dp {
		dp[i] = make([]int32, len(mb)+1)
	}
	for i := len(ma) - 1; i >= 0; i-- {
		for j := len(mb) - 1; j >= 0; j-- {
			if ma[i] == mb[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(ma) && j < len(mb) {
		switch {
		case ma[i] == mb[j]:
			m[head+i] = head + j
			i++
			j++
		case dp[i][j+1] >= dp[i+1][j]:
			j++
		default:
			i++
		}
	}
	return m
}

func lcsMergeLines(a, b []string) []string {
	m := matchMergeAnchors(a, b)
	var out []string
	for i, j := range m {
		if j >= 0 {
			out = append(out, a[i])
		}
	}
	return out
}
