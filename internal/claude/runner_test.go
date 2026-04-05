package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeCLI creates an executable shell script in t.TempDir() that prints
// the given stream-json lines to stdout and ignores all arguments/stdin.
// It returns the full path to the script.
func writeFakeCLI(t *testing.T, streamLines []string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("cat > /dev/null\n") // drain stdin
	sb.WriteString("sleep 0.05\n")      // let runner finish writing stdin before we emit
	for _, line := range streamLines {
		// Use printf with %s to avoid shell interpretation of the JSON
		sb.WriteString("printf '%s\\n' ")
		sb.WriteString("'")
		// escape single quotes inside line
		escaped := strings.ReplaceAll(line, "'", "'\\''")
		sb.WriteString(escaped)
		sb.WriteString("'\n")
	}

	if err := os.WriteFile(script, []byte(sb.String()), 0o755); err != nil {
		t.Fatalf("writeFakeCLI: %v", err)
	}
	return script
}

// writeSleepCLI creates a fake CLI that just sleeps for the given duration.
func writeSleepCLI(t *testing.T, seconds int) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")
	content := "#!/bin/sh\ncat > /dev/null\nsleep " + strings.Repeat("1\nsleep 1\n", seconds)
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\nsleep 10\n"), 0o755); err != nil {
		t.Fatalf("writeSleepCLI: %v", err)
	}
	_ = content
	return script
}

func TestRun_HappyPath(t *testing.T) {
	line := `{"type":"result","result":"hello world","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":0},"total_cost_usd":0.001}`
	bin := writeFakeCLI(t, []string{line})

	result, err := Run(context.Background(), RunOptions{
		Bin:         bin,
		Model:       "test-model",
		PromptText:  "hi",
		IdleTimeout: 10 * time.Second,
		HardTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Text != "hello world" {
		t.Errorf("Text = %q, want %q", result.Text, "hello world")
	}
	if result.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", result.Usage.OutputTokens)
	}
	if result.Usage.CacheReadTokens != 2 {
		t.Errorf("CacheReadTokens = %d, want 2", result.Usage.CacheReadTokens)
	}
	if result.Usage.CostUSD != 0.001 {
		t.Errorf("CostUSD = %v, want 0.001", result.Usage.CostUSD)
	}
}

func TestRun_ContextCancel(t *testing.T) {
	bin := writeSleepCLI(t, 10)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	_, err := Run(ctx, RunOptions{
		Bin:         bin,
		Model:       "test-model",
		PromptText:  "hi",
		IdleTimeout: 30 * time.Second,
		HardTimeout: 60 * time.Second,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Run took %v, expected fast cancellation", elapsed)
	}
}

func TestRun_NonJSONLinesIgnored(t *testing.T) {
	lines := []string{
		"[debug] claude starting up",
		"some other debug output",
		`{"type":"result","result":"parsed correctly","usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"total_cost_usd":0.0}`,
		"trailing debug line",
	}
	bin := writeFakeCLI(t, lines)

	result, err := Run(context.Background(), RunOptions{
		Bin:         bin,
		Model:       "test-model",
		PromptText:  "hi",
		IdleTimeout: 10 * time.Second,
		HardTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Text != "parsed correctly" {
		t.Errorf("Text = %q, want %q", result.Text, "parsed correctly")
	}
	if result.Usage.InputTokens != 3 {
		t.Errorf("InputTokens = %d, want 3", result.Usage.InputTokens)
	}
}
