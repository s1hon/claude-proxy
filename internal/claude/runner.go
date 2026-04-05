package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RunOptions configures one CLI invocation.
type RunOptions struct {
	Bin             string
	Model           string
	SystemPrompt    string
	PromptText      string
	SessionID       string // empty → let CLI choose? we always supply one
	Resume          bool   // true → --resume, false → --session-id
	Effort          string // "low"|"medium"|"high"|""
	DisableThinking bool
	IdleTimeout     time.Duration
	HardTimeout     time.Duration
	OnDelta         func(string) // optional streaming callback (text deltas)
}

// Result is returned by Run when the CLI exits successfully.
type Result struct {
	Text  string
	Usage Usage
}

// Run spawns the Claude CLI, feeds the prompt via stdin, and parses the
// stream-json output. It respects context cancellation and idle timeouts.
func Run(ctx context.Context, opts RunOptions) (*Result, error) {
	if opts.Bin == "" {
		opts.Bin = "claude"
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 2 * time.Minute
	}
	if opts.HardTimeout <= 0 {
		opts.HardTimeout = 20 * time.Minute
	}

	runCtx, cancel := context.WithTimeout(ctx, opts.HardTimeout)
	defer cancel()

	args := []string{
		"--print",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
		"--model", opts.Model,
	}
	if opts.SessionID != "" {
		if opts.Resume {
			args = append(args, "--resume", opts.SessionID)
		} else {
			args = append(args, "--session-id", opts.SessionID)
		}
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}
	args = append(args, "--tools", "")
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}

	cmd := exec.CommandContext(runCtx, opts.Bin, args...)
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	if opts.DisableThinking {
		cmd.Env = append(cmd.Env, "MAX_THINKING_TOKENS=0")
	}
	cmd.Stdin = strings.NewReader(opts.PromptText)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	log.Printf("[claude] spawn model=%s resume=%v effort=%q thinking=%v",
		opts.Model, opts.Resume, opts.Effort, !opts.DisableThinking)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	// Idle timer — reset on every stdout chunk; fires cancel() if silent too long.
	var idleMu sync.Mutex
	var idleTimedOut bool
	idleTimer := time.AfterFunc(opts.IdleTimeout, func() {
		idleMu.Lock()
		idleTimedOut = true
		idleMu.Unlock()
		cancel()
	})
	defer idleTimer.Stop()
	resetIdle := func() { idleTimer.Reset(opts.IdleTimeout) }

	// Drain stderr in the background.
	var stderrBuf strings.Builder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if line != "" {
				log.Printf("[claude stderr] %s", line)
				stderrBuf.WriteString(line)
				stderrBuf.WriteByte('\n')
			}
		}
	}()

	// Parse stdout line by line.
	var fullText string
	var usage Usage
	var textBuf strings.Builder
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 256*1024), 16*1024*1024)

	for sc.Scan() {
		resetIdle()
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // non-JSON debug lines
		}
		if ev.Type == "result" && len(ev.Result) > 0 {
			var s string
			if err := json.Unmarshal(ev.Result, &s); err == nil && s != "" {
				if opts.OnDelta != nil && len(s) > textBuf.Len() {
					delta := s[textBuf.Len():]
					opts.OnDelta(delta)
				}
				textBuf.Reset()
				textBuf.WriteString(s)
				fullText = s
			}
			if ev.Usage != nil {
				usage = Usage{
					InputTokens:         ev.Usage.InputTokens,
					CacheCreationTokens: ev.Usage.CacheCreationInputTokens,
					CacheReadTokens:     ev.Usage.CacheReadInputTokens,
					OutputTokens:        ev.Usage.OutputTokens,
					CostUSD:             ev.TotalCostUSD,
				}
			}
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("[claude] stdout scan error: %v", err)
	}

	waitErr := cmd.Wait()
	wg.Wait()

	idleMu.Lock()
	idle := idleTimedOut
	idleMu.Unlock()

	if idle {
		return nil, fmt.Errorf("idle timeout (%s no activity)", opts.IdleTimeout)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("hard timeout (%s)", opts.HardTimeout)
	}
	if waitErr != nil && fullText == "" {
		return nil, fmt.Errorf("claude exited: %v: %s", waitErr, stderrBuf.String())
	}

	return &Result{Text: fullText, Usage: usage}, nil
}
