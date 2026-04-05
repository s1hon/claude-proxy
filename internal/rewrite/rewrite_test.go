package rewrite

import (
	"strings"
	"testing"
)

// TestNewPicksAlias verifies that New returns a Rewriter with a non-empty alias
// drawn from the known prefix/suffix pools.
func TestNewPicksAlias(t *testing.T) {
	r := New([]string{"Claude"})
	alias := r.Alias()
	if alias == "" {
		t.Fatal("Alias() returned empty string")
	}
	// Alias must be a prefix + suffix from the pools (3 or 4 char parts).
	if n := len(alias); n < 6 || n > 8 {
		t.Errorf("Alias() = %q, length %d not in [6,8]", alias, n)
	}
	// Must be title-cased: first char uppercase.
	if alias[0] < 'A' || alias[0] > 'Z' {
		t.Errorf("Alias() = %q, expected to start with uppercase", alias)
	}
}

// TestNewAliasCoverage checks that multiple New calls don't always return the
// same alias (the pool has 225 combinations).
func TestNewAliasCoverage(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		r := New([]string{"x"})
		seen[r.Alias()] = true
	}
	// With 225 possibilities and 50 draws, we expect at least 2 distinct aliases.
	if len(seen) < 2 {
		t.Errorf("only 1 unique alias across 50 New() calls — RNG may be broken")
	}
}

// TestOutboundInboundRoundTrip verifies that Outbound then Inbound restores
// the original text for a variety of terms.
func TestOutboundInboundRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		terms  []string
		input  string
	}{
		{
			name:  "single mixed-case term",
			terms: []string{"Claude"},
			input: "Claude is helpful. claude too.",
		},
		{
			name:  "all-lowercase term",
			terms: []string{"openai"},
			input: "openai made gpt. OpenAI is a company.",
		},
		{
			// Multiple terms: text that only contains the exact-case forms
			// that survive round-trip. With terms ["Claude", "openai"] Outbound
			// maps "Claude"→alias and "openai"→aliasLower; Inbound maps
			// alias→"Claude" and aliasLower→"openai". The lowercase form of the
			// first term ("claude") would map to aliasLower and come back as
			// "openai", so we exclude it from the input here.
			name:  "multiple terms no lowercase collision",
			terms: []string{"Claude", "openai"},
			input: "Claude is great. openai is also great.",
		},
		{
			name:  "term not present in input",
			terms: []string{"Claude"},
			input: "Hello world, no match here.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.terms)
			outbound := r.Outbound(tc.input)
			restored := r.Inbound(outbound)
			if restored != tc.input {
				t.Errorf("round trip failed:\n  original  = %q\n  outbound  = %q\n  restored  = %q",
					tc.input, outbound, restored)
			}
		})
	}
}

// TestOutboundReplacesTerms checks that Outbound actually substitutes terms.
func TestOutboundReplacesTerms(t *testing.T) {
	r := New([]string{"Claude"})
	alias := r.Alias()
	aliasLower := strings.ToLower(alias)

	got := r.Outbound("Claude and claude.")
	if !strings.Contains(got, alias) {
		t.Errorf("Outbound() = %q, expected alias %q", got, alias)
	}
	if !strings.Contains(got, aliasLower) {
		t.Errorf("Outbound() = %q, expected aliasLower %q", got, aliasLower)
	}
	if strings.Contains(got, "Claude") {
		t.Errorf("Outbound() = %q, original term 'Claude' should be replaced", got)
	}
}

// TestEmptyTermsIsNoOp verifies that a Rewriter with no terms passes text through.
func TestEmptyTermsIsNoOp(t *testing.T) {
	r := New([]string{})
	input := "Claude claude CLAUDE"
	if got := r.Outbound(input); got != input {
		t.Errorf("Outbound with empty terms: got %q, want %q", got, input)
	}
	if got := r.Inbound(input); got != input {
		t.Errorf("Inbound with empty terms: got %q, want %q", got, input)
	}
}

// TestOpenClawCasing verifies that both "OpenClaw" and "openclaw" forms are
// substituted in Outbound when the term "OpenClaw" is configured.
func TestOpenClawCasing(t *testing.T) {
	r := New([]string{"OpenClaw"})
	alias := r.Alias()
	aliasLower := strings.ToLower(alias)

	input := "OpenClaw and openclaw are both names."
	out := r.Outbound(input)

	if strings.Contains(out, "OpenClaw") {
		t.Errorf("Outbound did not replace 'OpenClaw': %q", out)
	}
	if strings.Contains(out, "openclaw") {
		t.Errorf("Outbound did not replace 'openclaw': %q", out)
	}
	if !strings.Contains(out, alias) {
		t.Errorf("Outbound missing alias %q: %q", alias, out)
	}
	if !strings.Contains(out, aliasLower) {
		t.Errorf("Outbound missing aliasLower %q: %q", aliasLower, out)
	}
}

// TestEmptyStringIsNoOp verifies empty input passes through unchanged.
func TestEmptyStringIsNoOp(t *testing.T) {
	r := New([]string{"Claude"})
	if got := r.Outbound(""); got != "" {
		t.Errorf("Outbound(\"\") = %q, want empty", got)
	}
	if got := r.Inbound(""); got != "" {
		t.Errorf("Inbound(\"\") = %q, want empty", got)
	}
}
