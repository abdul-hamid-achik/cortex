package kernel

import (
	"strings"
	"unicode"
)

// This file holds the deliberately heuristic question processing behind
// deep-mode investigation and the git-grep discovery fallback: decomposing a
// compound question into targeted sub-queries, and picking a literal search
// term. It is heuristic, ASCII-careful text handling (no LLM), isolated here so
// the routing/orchestration in investigate.go stays readable.

// maxSubQueries bounds the deep-mode decomposition fan-out so one compound
// question cannot explode into unbounded discovery calls.
const maxSubQueries = 5

// interrogatives are the clause openers that mark a new sub-question after a
// comma or "and" ("…, how is session state validated, and where is …").
var interrogatives = map[string]bool{
	"how": true, "where": true, "what": true, "why": true, "when": true,
	"which": true, "who": true, "does": true, "do": true, "is": true,
	"are": true, "can": true,
}

// grepStopwords are the generic non-interrogative words that carry no searchable
// identity (the interrogatives themselves live in `interrogatives`). Both sets
// are skipped when grepPattern picks a literal search term.
var grepStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "with": true, "by": true, "from": true,
	"and": true, "or": true, "that": true, "this": true, "it": true, "be": true,
	"used": true, "handled": true, "defined": true, "located": true,
	"function": true, "file": true, "code": true, "work": true, "works": true,
}

// grepPattern derives a single literal search term from a natural-language
// question for the git-grep discovery fallback. It deliberately stays simple —
// one fixed-string pattern, preferring an identifier-like token (camelCase,
// snake_case, or a digit-bearing identifier) over a plain word, then the longest
// non-stopword of at least four runes. Returns "" when nothing usable remains.
func grepPattern(question string) string {
	tokens := strings.FieldsFunc(question, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	var identifier, longest string
	for _, tok := range tokens {
		lower := strings.ToLower(tok)
		if interrogatives[lower] || grepStopwords[lower] {
			continue
		}
		if identifier == "" && looksLikeIdentifier(tok) {
			identifier = tok
		}
		if len([]rune(tok)) > len([]rune(longest)) {
			longest = tok
		}
	}
	if identifier != "" {
		return identifier
	}
	if len([]rune(longest)) >= 4 {
		return longest
	}
	return ""
}

// looksLikeIdentifier reports whether a token looks like a code identifier
// (snake_case, camelCase/PascalCase, or a digit-bearing name like sha256) rather
// than an ordinary word — these make the most distinctive literal search terms.
func looksLikeIdentifier(tok string) bool {
	if strings.Contains(tok, "_") {
		return true
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range tok {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if hasUpper && hasLower {
		return true // camelCase / PascalCase / OAuth
	}
	return hasDigit && (hasUpper || hasLower) // e.g. base64, sha256
}

// subQuestions decomposes a compound question into targeted sub-queries using
// a deliberately heuristic split (no LLM): hard separators (?, ;, :) first,
// then comma/"and" boundaries that introduce a new interrogative clause.
// Fragments under three words are dropped, duplicates collapse, and the result
// is capped at max. A question that does not decompose returns nil — callers
// keep the original question.
func subQuestions(q string, max int) []string {
	if max < 2 {
		return nil
	}
	const marker = "\x00"
	// Hard separators (?, ;, :) split only when followed by whitespace or the
	// end of the question — a ? / ; / : glued to the next character is code or
	// a URL ("std::sort", "https://…", "a?b:c"), not a clause boundary, and
	// splitting there mangles the question into garbage sub-queries.
	mb := make([]byte, 0, len(q))
	for i := 0; i < len(q); i++ {
		ch := q[i]
		if (ch == '?' || ch == ';' || ch == ':') &&
			(i+1 == len(q) || q[i+1] == ' ' || q[i+1] == '\t' || q[i+1] == '\n') {
			mb = append(mb, marker[0])
			continue
		}
		mb = append(mb, ch)
	}
	marked := string(mb)
	// Soft separators: a comma or " and " followed by an interrogative word.
	var b strings.Builder
	for i := 0; i < len(marked); {
		cut := 0
		for _, sep := range []string{", ", " and "} {
			if hasFoldPrefix(marked[i:], sep) && interrogatives[firstWordLower(marked[i+len(sep):])] {
				cut = len(sep)
				break
			}
		}
		if cut > 0 {
			b.WriteString(marker)
			i += cut
			continue
		}
		b.WriteByte(marked[i])
		i++
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(b.String(), marker) {
		p = strings.Trim(strings.TrimSpace(p), ",")
		p = strings.TrimSpace(p)
		if len(splitWS(p)) < 3 {
			continue // too short to stand alone as a query
		}
		// An object split emits the ORIGINAL fragment first, then the two
		// targeted conjunct queries. The original must survive: the split is
		// heuristic and sometimes fires on non-object conjunctions ("search
		// and replace"), and at the cap a partial emit would silently drop
		// the right conjunct's terms from the whole query set (panel review
		// 2026-07-16) — original-first means whatever the cap keeps still
		// covers all the question's terms.
		parts := []string{p}
		parts = append(parts, objectConjunctSplit(p)...)
		for _, part := range parts {
			key := strings.ToLower(part)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, part)
			if len(out) == max {
				break
			}
		}
		if len(out) == max {
			break
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// lastIndexFoldASCII returns the last index of sub in s comparing ASCII
// case-insensitively over the ORIGINAL bytes. strings.LastIndex over a
// ToLower copy is not equivalent: Unicode case mapping changes UTF-8 byte
// length for some runes (Ⱥ, İ, the Kelvin sign), so an index computed in the
// lowered copy panics or splits mid-rune when applied to the original (panel
// review 2026-07-16, reproduced). sub must be ASCII.
func lastIndexFoldASCII(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// objectConjunctSplit splits a clause whose tail conjoins two parallel objects
// ("… enforce idempotency and size limits") into one query per object. The
// clause-boundary pass above cannot see these: the conjunct after " and "
// opens with a noun, not an interrogative, so the question reaches the
// embedder as one averaged-out query (dogfooding 2026-07-16: "how does the
// jobs queue ingress enforce idempotency and size limits?" did not decompose
// and discovery returned doc mush). Only the RIGHTMOST " and " is considered
// (object lists sit at a clause's end), the right conjunct must be a short
// phrase (2–3 words) that does not open a new interrogative clause, and the
// left side must be long enough (≥5 words) to carry the shared context. The
// second query drops the left side's last word — the first object's head —
// and grafts the right conjunct into its slot, keeping the verb ("…enforce
// idempotency and size limits" → "…enforce size limits"). The caller keeps
// the original clause alongside these, so a heuristic miss adds noise but
// never loses the real query. Returns nil when the shape doesn't hold.
func objectConjunctSplit(p string) []string {
	i := lastIndexFoldASCII(p, " and ")
	if i < 0 {
		return nil
	}
	left := strings.TrimSpace(p[:i])
	right := strings.TrimSpace(p[i+len(" and "):])
	lw, rw := splitWS(left), splitWS(right)
	if len(lw) < 5 || len(rw) < 2 || len(rw) > 3 || interrogatives[strings.ToLower(rw[0])] {
		return nil
	}
	second := strings.Join(append(append([]string{}, lw[:len(lw)-1]...), rw...), " ")
	return []string{left, second}
}

// decomposeSearchSteps rewrites each discovery search step over a compound
// question into one targeted search per sub-question, returning the rewritten
// steps and the sub-queries that were actually installed into a search step.
// Non-search steps (codemap impact/find, artifact listings) pass through
// unchanged; a question that does not decompose — or a route with no search
// step at all — leaves the steps untouched and returns nil sub-queries, so
// callers can report decomposition only when it really happened. Each tool's
// search is expanded once even if the route named it twice.
func decomposeSearchSteps(steps []step, question string, candLimit int) ([]step, []string) {
	subs := subQuestions(question, maxSubQueries)
	if len(subs) == 0 {
		return steps, nil
	}
	expandedTools := map[string]bool{}
	var out []step
	for _, s := range steps {
		if s.op != "search" {
			out = append(out, s)
			continue
		}
		if expandedTools[s.tool] {
			continue
		}
		expandedTools[s.tool] = true
		for _, sub := range subs {
			out = append(out, step{tool: s.tool, op: "search", input: map[string]any{"query": sub, "limit": candLimit}})
		}
	}
	if len(expandedTools) == 0 {
		return steps, nil
	}
	return out, subs
}

// hasFoldPrefix reports whether s begins with prefix, ASCII case-insensitively.
func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// firstWordLower returns the first whitespace-delimited word of s, lowercased.
func firstWordLower(s string) string {
	fields := splitWS(strings.TrimSpace(s))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}
