package apt

import "sort"

// Suggest returns package names close to a name that was not found, so a
// typo does not end the run. Candidates are ranked by how they relate to the
// query: a name that contains it beats one that is merely a near miss.
func (ix *Index) Suggest(name string, limit int) []string {
	type scored struct {
		name  string
		score int
	}
	var out []scored
	seen := map[string]bool{}

	for candidate := range ix.byName {
		if seen[candidate] {
			continue
		}
		s, ok := similarity(name, candidate)
		if !ok {
			continue
		}
		seen[candidate] = true
		out = append(out, scored{candidate, s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	names := make([]string, 0, len(out))
	for _, s := range out {
		names = append(names, s.name)
	}
	return names
}

// similarity scores a candidate; ok is false when it is not worth offering.
func similarity(query, candidate string) (int, bool) {
	switch {
	case candidate == query:
		return 1000, true
	case hasPrefix(candidate, query):
		// "postgresql-16" for "postgresql": the most likely intent.
		return 500 - len(candidate), true
	case contains(candidate, query):
		return 300 - len(candidate), true
	case hasPrefix(query, candidate):
		return 200 - len(query) + len(candidate), true
	}
	// Only bother with edit distance for names of a similar length, which
	// keeps this cheap across the ~100k names in a full Ubuntu index.
	if abs(len(candidate)-len(query)) > 3 || len(query) < 4 {
		return 0, false
	}
	d := editDistance(query, candidate, 3)
	if d > 3 {
		return 0, false
	}
	return 100 - d*10, true
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func contains(s, sub string) bool {
	if len(sub) == 0 || len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// editDistance is Levenshtein with an early exit once max is exceeded.
func editDistance(a, b string, max int) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if cur[j] < best {
				best = cur[j]
			}
		}
		if best > max {
			return max + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
