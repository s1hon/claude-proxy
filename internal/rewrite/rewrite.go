// Package rewrite performs bidirectional keyword → random alias substitution on
// prompts and responses. Each request picks one alias from a 15×15 pool; the
// alias and its lowercase form replace the configured terms on the way out,
// and the inverse substitution is applied on the way back in.
package rewrite

import (
	"math/rand"
	"strings"
	"sync"
	"time"
)

var (
	prefixes = []string{"Chat", "Dev", "Run", "Ask", "Net", "App", "Zen", "Arc", "Dot", "Amp", "Hex", "Orb", "Elm", "Oak", "Sky"}
	suffixes = []string{"Kit", "Box", "Pod", "Hub", "Lab", "Ops", "Bay", "Tap", "Rim", "Fog", "Dew", "Fin", "Gem", "Jet", "Cog"}

	rngMu sync.Mutex
	rng   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// Rewriter holds the alias chosen for one request plus the terms to substitute.
// Zero value is unusable; create with New.
type Rewriter struct {
	alias      string
	aliasLower string
	terms      []string // terms to replace in outbound direction
}

// New creates a Rewriter with a freshly picked alias. If terms is empty, the
// Rewriter is a no-op (Outbound/Inbound return the input unchanged).
func New(terms []string) *Rewriter {
	rngMu.Lock()
	alias := prefixes[rng.Intn(len(prefixes))] + suffixes[rng.Intn(len(suffixes))]
	rngMu.Unlock()
	return &Rewriter{
		alias:      alias,
		aliasLower: strings.ToLower(alias),
		terms:      terms,
	}
}

// Alias returns the alias string chosen for this rewriter (mainly for tests).
func (r *Rewriter) Alias() string { return r.alias }

// Outbound replaces every configured term in s with the alias. Case matters:
// exactly-cased term → alias, lowercased term → aliasLower. Other cases are
// not touched (matches the original implementation).
func (r *Rewriter) Outbound(s string) string {
	if r == nil || len(r.terms) == 0 || s == "" {
		return s
	}
	for _, term := range r.terms {
		if term == "" {
			continue
		}
		lower := strings.ToLower(term)
		if term == lower {
			s = strings.ReplaceAll(s, term, r.aliasLower)
		} else {
			s = strings.ReplaceAll(s, term, r.alias)
			s = strings.ReplaceAll(s, lower, r.aliasLower)
		}
	}
	return s
}

// Inbound reverses the substitution: alias → first matching term, aliasLower →
// its lowercase form. If multiple terms were configured, the first is used as
// the canonical restoration target.
func (r *Rewriter) Inbound(s string) string {
	if r == nil || len(r.terms) == 0 || s == "" {
		return s
	}
	// Find the first upper-case term (contains any upper) and first lower term.
	var upperTerm, lowerTerm string
	for _, t := range r.terms {
		if t == "" {
			continue
		}
		if t == strings.ToLower(t) {
			if lowerTerm == "" {
				lowerTerm = t
			}
		} else if upperTerm == "" {
			upperTerm = t
		}
	}
	if upperTerm == "" && lowerTerm != "" {
		upperTerm = lowerTerm
	}
	if lowerTerm == "" && upperTerm != "" {
		lowerTerm = strings.ToLower(upperTerm)
	}
	if upperTerm != "" {
		s = strings.ReplaceAll(s, r.alias, upperTerm)
	}
	if lowerTerm != "" {
		s = strings.ReplaceAll(s, r.aliasLower, lowerTerm)
	}
	return s
}
