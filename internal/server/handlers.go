package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/s1hon/claude-proxy/internal/claude"
	"github.com/s1hon/claude-proxy/internal/config"
	"github.com/s1hon/claude-proxy/internal/convert"
	"github.com/s1hon/claude-proxy/internal/openai"
	"github.com/s1hon/claude-proxy/internal/rewrite"
	"github.com/s1hon/claude-proxy/internal/session"
	"github.com/s1hon/claude-proxy/internal/stats"
	"github.com/s1hon/claude-proxy/internal/tools"
)

// dumpMu serialises appends to debug_requests.jsonl so concurrent handlers
// do not interleave bytes within a single line.
var dumpMu sync.Mutex

const (
	compactionPrefix  = "The conversation history before this point was compacted into the following summary:"
	maxCompactPrompt  = 1_500_000
	compactionHashLen = 500
)

// Deps wires all dependencies a Handler needs.
type Deps struct {
	Config  config.Config
	Store   *session.Store
	Stats   *stats.Stats
	Limiter *Limiter
}

// Handler implements net/http.Handler.
type Handler struct {
	deps Deps
}

// NewHandler constructs a Handler.
func NewHandler(d Deps) *Handler {
	return &Handler{deps: d}
}

// Routes returns a mux with all API endpoints registered.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.health)
	mux.HandleFunc("/v1/models", h.models)
	mux.HandleFunc("/v1/chat/completions", h.chat)
	mux.HandleFunc("/status", h.status)
	return mux
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.deps.Stats.Snapshot())
}

func (h *Handler) models(w http.ResponseWriter, _ *http.Request) {
	now := time.Now().Unix()
	writeJSON(w, http.StatusOK, openai.ModelsResponse{
		Object: "list",
		Data: []openai.ModelEntry{
			{ID: "claude-opus-latest", Object: "model", Created: now, OwnedBy: "anthropic"},
			{ID: "claude-sonnet-latest", Object: "model", Created: now, OwnedBy: "anthropic"},
			{ID: "claude-haiku-latest", Object: "model", Created: now, OwnedBy: "anthropic"},
		},
	})
}

// invocation bundles everything needed for one Claude CLI run.
type invocation struct {
	routingKey   string
	sessionID    string
	resume       bool
	systemPrompt string
	promptText   string
	mode         string // "new" | "resume" | "refresh"
	pendingHash  int32  // set only when mode == "refresh"
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read raw body so we can both decode it and dump it to
	// debug_requests.jsonl for post-hoc inspection.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("read body: %v", err))
		return
	}
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}
	if !claude.KnownModel(req.Model) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported model: %s", req.Model))
		return
	}

	// Compute the routing key up-front so we can serialise all same-key
	// requests before any session lookup or refresh decision runs. Without
	// this lock, two concurrent requests for the same conversation race on
	// LastCompactionHash / sessionID and the loser's session gets silently
	// overwritten — that was the source of the "agent 回錯訊息" bleed.
	channel := session.ExtractChannelLabelWithHeaders(req.Messages, r.Header)
	agent := session.ExtractAgentName(req.Messages)
	routingKey := session.RoutingKey(channel, agent)
	unlock := h.deps.Store.LockKey(routingKey)
	defer unlock()

	inv := h.prepare(&req, r.Header)
	h.logDiagnostics(r, &req, inv)
	h.dumpRequest(r, rawBody, inv)

	release, ok := h.deps.Limiter.Acquire(inv.routingKey)
	if !ok {
		writeError(w, http.StatusTooManyRequests, "concurrency limit reached")
		return
	}
	defer release()

	h.deps.Stats.TotalRequests.Add(1)
	h.deps.Stats.ActiveRequests.Add(1)
	defer h.deps.Stats.ActiveRequests.Add(-1)

	rw := rewrite.New(h.deps.Config.RewriteTerms)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if req.Stream {
		h.serveStream(ctx, w, &req, inv, rw)
		return
	}
	h.serveJSON(ctx, w, &req, inv, rw)
}

// prepare builds the full invocation plan: routing key, session lookup,
// compaction detection, and the final system/prompt text.
func (h *Handler) prepare(req *openai.ChatCompletionRequest, headers http.Header) invocation {
	channel := session.ExtractChannelLabelWithHeaders(req.Messages, headers)
	agent := session.ExtractAgentName(req.Messages)
	routingKey := session.RoutingKey(channel, agent)

	var sessionID string
	var resume bool
	var existing session.Entry
	if e, found := h.deps.Store.GetByChannel(routingKey); found {
		sessionID = e.SessionID
		resume = true
		existing = e
	}

	conv := convert.Messages(req.Messages)
	systemPrompt := conv.SystemPrompt

	// Context refresh: when the upstream has compacted the history, swap the
	// running session for a new one seeded with a size-bounded compact prompt.
	var pendingHash int32
	mode := "new"
	if resume {
		if hash, found := detectCompactionHash(req.Messages); found && hash != existing.LastCompactionHash {
			// Defer refresh if a tool loop is mid-flight — finishing the loop
			// first gives Claude a chance to consume the tool results it just
			// requested, otherwise we would throw them away.
			inToolLoop := convert.ExtractNewMessages(req.Messages, 0) != ""
			if !inToolLoop {
				compact := convert.MessagesCompact(req.Messages, convert.CompactOptions{})
				if len(compact.PromptText) > maxCompactPrompt {
					log.Printf("[chat] refresh SKIPPED: compact prompt too long (%d)", len(compact.PromptText))
				} else {
					log.Printf("[chat] context refresh: hash=%d old=%s → new session (%d chars)",
						hash, truncate(existing.SessionID, 8), len(compact.PromptText))
					sessionID = session.NewSessionID()
					resume = false
					systemPrompt = compact.SystemPrompt
					conv.PromptText = compact.PromptText
					mode = "refresh"
					pendingHash = hash
				}
			} else {
				log.Printf("[chat] refresh DEFERRED: tool loop in progress (hash=%d)", hash)
			}
		}
	}

	// Append tool-calling protocol AFTER the system prompt (whether normal,
	// refresh-sourced, or empty).
	if instr := tools.BuildInstructions(req.Tools); instr != "" {
		systemPrompt += instr
	}

	var promptText string
	switch {
	case mode == "refresh":
		promptText = conv.PromptText
	case resume:
		if inc := convert.ExtractNewMessages(req.Messages, 0); inc != "" {
			promptText = inc
			mode = "resume"
		} else if inc := convert.ExtractNewUserMessages(req.Messages, 0); inc != "" {
			promptText = inc
			mode = "resume"
		} else {
			// Nothing new to send — can't resume; start a new session.
			resume = false
			sessionID = session.NewSessionID()
			promptText = conv.PromptText
		}
	default:
		if sessionID == "" {
			sessionID = session.NewSessionID()
		}
		promptText = conv.PromptText
	}

	return invocation{
		routingKey:   routingKey,
		sessionID:    sessionID,
		resume:       resume,
		systemPrompt: systemPrompt,
		promptText:   promptText,
		mode:         mode,
		pendingHash:  pendingHash,
	}
}

// runWithRetry invokes the Claude CLI and, on error, makes one more attempt
// using a compact refresh prompt + fresh session (unless we already tried
// that). The returned invocation reflects whichever attempt actually ran.
func (h *Handler) runWithRetry(ctx context.Context, req *openai.ChatCompletionRequest,
	inv invocation, rw *rewrite.Rewriter) (*claude.Result, invocation, error) {

	cfg := h.deps.Config
	model := claude.ResolveModel(req.Model, cfg.OpusModel, cfg.SonnetModel, cfg.HaikuModel)

	build := func(i invocation) claude.RunOptions {
		return claude.RunOptions{
			Bin:             cfg.ClaudeBin,
			Model:           model,
			SystemPrompt:    rw.Outbound(i.systemPrompt),
			PromptText:      rw.Outbound(i.promptText),
			SessionID:       i.sessionID,
			Resume:          i.resume,
			Effort:          claude.MapEffort(req.ReasoningEffort),
			DisableThinking: req.ReasoningEffort == "",
			IdleTimeout:     cfg.IdleTimeout,
			HardTimeout:     cfg.HardTimeout,
		}
	}

	res, err := claude.Run(ctx, build(inv))
	if err == nil {
		return res, inv, nil
	}

	// Don't retry if we already ran a refresh, or if the caller's context is
	// gone (client disconnect).
	if inv.mode == "refresh" || ctx.Err() != nil {
		return nil, inv, err
	}

	compact := convert.MessagesCompact(req.Messages, convert.CompactOptions{})
	if len(compact.PromptText) == 0 || len(compact.PromptText) > maxCompactPrompt {
		return nil, inv, err
	}

	log.Printf("[chat] CLI failed (%v), retrying with compact refresh", err)
	retry := invocation{
		routingKey:   inv.routingKey,
		sessionID:    session.NewSessionID(),
		resume:       false,
		systemPrompt: compact.SystemPrompt,
		promptText:   compact.PromptText,
		mode:         "refresh",
		pendingHash:  inv.pendingHash,
	}
	if instr := tools.BuildInstructions(req.Tools); instr != "" {
		retry.systemPrompt += instr
	}
	res, err = claude.Run(ctx, build(retry))
	return res, retry, err
}

func (h *Handler) serveJSON(ctx context.Context, w http.ResponseWriter,
	req *openai.ChatCompletionRequest, inv invocation, rw *rewrite.Rewriter) {

	result, final, err := h.runWithRetry(ctx, req, inv, rw)
	if err != nil {
		h.deps.Stats.Errors.Add(1)
		log.Printf("[chat] claude run error: %v", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	text := rw.Inbound(result.Text)
	toolCalls := tools.ParseToolCalls(text)
	cleaned := tools.CleanText(text)

	h.persist(final, cleaned)

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}

	resp := openai.ChatCompletionResponse{
		ID:      "chatcmpl-" + session.NewSessionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []openai.Choice{{
			Index: 0,
			Message: openai.ChoiceMessage{
				Role:      "assistant",
				Content:   cleaned,
				ToolCalls: toolCalls,
			},
			FinishReason: finishReason,
		}},
		Usage: buildUsage(result.Usage),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) serveStream(ctx context.Context, w http.ResponseWriter,
	req *openai.ChatCompletionRequest, inv invocation, rw *rewrite.Rewriter) {

	chatID := "chatcmpl-" + session.NewSessionID()
	sse, err := newSSEWriter(w, chatID, req.Model, time.Now().Unix())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = sse.sendRole()

	// Claude CLI only emits the final text in the result event, so we buffer
	// it and emit a single content delta once Run returns. This is less
	// granular than token-level streaming but keeps the SSE contract intact
	// and makes the whole response available for tool_call parsing.
	result, final, err := h.runWithRetry(ctx, req, inv, rw)
	if err != nil {
		h.deps.Stats.Errors.Add(1)
		log.Printf("[chat-stream] claude run error: %v", err)
		reason := "error"
		_ = sse.sendFinish(reason, nil)
		sse.done()
		return
	}

	text := rw.Inbound(result.Text)
	toolCalls := tools.ParseToolCalls(text)
	cleaned := tools.CleanText(text)

	h.persist(final, cleaned)

	if len(toolCalls) > 0 {
		_ = sse.sendToolCalls(toolCalls)
	} else if cleaned != "" {
		_ = sse.sendDelta(cleaned)
	}

	usage := buildUsage(result.Usage)
	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	_ = sse.sendFinish(finish, &usage)
	sse.done()
}

// dumpRequest appends the full incoming request (headers + body) and the
// proxy's parsed decision to <stateDir>/debug_requests.jsonl as a single
// JSON line. Runs on every chat request so operators tailing the file can
// see exactly what every client is sending. Sensitive auth headers are
// redacted. Disabled when CLAUDE_PROXY_DEBUG_DUMP=0.
func (h *Handler) dumpRequest(r *http.Request, raw []byte, inv invocation) {
	if os.Getenv("CLAUDE_PROXY_DEBUG_DUMP") == "0" {
		return
	}
	dir := filepath.Dir(h.deps.Config.StatePath)
	if dir == "" || dir == "." {
		dir = "."
	}
	path := filepath.Join(dir, "debug_requests.jsonl")

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		kl := strings.ToLower(k)
		if kl == "authorization" || kl == "cookie" || kl == "x-api-key" || kl == "proxy-authorization" {
			headers[k] = "<redacted>"
			continue
		}
		headers[k] = strings.Join(v, ", ")
	}

	// Keep bodies bounded so one weird client can't blow up the file.
	const maxBody = 256 * 1024
	var body any
	if len(raw) > maxBody {
		body = map[string]any{
			"_truncated": true,
			"_size":      len(raw),
			"_preview":   string(raw[:4096]),
		}
	} else if err := json.Unmarshal(raw, &body); err != nil {
		body = map[string]any{"_raw": string(raw)}
	}

	entry := map[string]any{
		"ts":         time.Now().Format(time.RFC3339Nano),
		"remote":     r.RemoteAddr,
		"method":     r.Method,
		"path":       r.URL.Path,
		"headers":    headers,
		"body":       body,
		"legacy_rk":  inv.routingKey,
		"sid":        inv.sessionID,
		"mode":       inv.mode,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[dump] marshal error: %v", err)
		return
	}

	dumpMu.Lock()
	defer dumpMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[dump] open %s: %v", path, err)
		return
	}
	defer f.Close()
	_, _ = f.Write(line)
	_, _ = f.Write([]byte{'\n'})
}

// logDiagnostics emits one [diag] line per request with the routing key,
// the session ID we ended up using, and a short preview of the last user
// message. Useful for tailing the log during live traffic to verify that
// distinct conversations land in distinct buckets.
func (h *Handler) logDiagnostics(_ *http.Request, req *openai.ChatCompletionRequest, inv invocation) {
	preview := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		c := convert.ExtractContent(req.Messages[i].Content)
		if c == "" {
			continue
		}
		// Strip metadata envelopes so the preview shows the actual user
		// utterance, not the Conversation info header.
		if idx := strings.LastIndex(c, "```\n\n"); idx >= 0 {
			c = c[idx+5:]
		}
		preview = strings.TrimSpace(c)
		if len(preview) > 80 {
			preview = preview[:80]
		}
		break
	}
	log.Printf("[diag] rk=%q sid=%s mode=%s model=%s nmsgs=%d preview=%q",
		inv.routingKey, truncate(inv.sessionID, 8), inv.mode, req.Model,
		len(req.Messages), preview)
}

// persist writes the result of one successful invocation back to the store
// (channel mapping, response-key fallback, and — if this run was a refresh —
// the new compaction hash).
func (h *Handler) persist(inv invocation, cleaned string) {
	h.deps.Store.SetChannel(inv.routingKey, inv.sessionID)
	if inv.mode == "refresh" && inv.pendingHash != 0 {
		h.deps.Store.SetCompactionHash(inv.routingKey, inv.pendingHash)
	}
	if cleaned != "" {
		h.deps.Store.SetResponseKey(cleaned, inv.sessionID)
	}
	if err := h.deps.Store.Save(); err != nil {
		log.Printf("[store] save error: %v", err)
	}
}

// detectCompactionHash scans user messages for the upstream compaction summary
// marker. Returns the first 500-char hash and true if one is found.
func detectCompactionHash(msgs []openai.Message) (int32, bool) {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		c := convert.ExtractContent(m.Content)
		if strings.HasPrefix(c, compactionPrefix) {
			snippet := c
			if len(snippet) > compactionHashLen {
				snippet = snippet[:compactionHashLen]
			}
			return javaStringHash(snippet), true
		}
	}
	return 0, false
}

// javaStringHash reproduces the 32-bit `((h << 5) - h + c) | 0` hash used by
// the upstream Node.js bridge. It iterates over runes rather than UTF-16 code
// units, so for non-ASCII content the numeric hash will differ from the JS
// implementation — that is fine because Go and JS stores are independent.
func javaStringHash(s string) int32 {
	var h int32
	for _, c := range s {
		h = h*31 + int32(c)
	}
	return h
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func buildUsage(u claude.Usage) openai.Usage {
	prompt := u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens
	return openai.Usage{
		PromptTokens:     prompt,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      prompt + u.OutputTokens,
		PromptTokensDetails: openai.PromptTokensDetails{
			CachedTokens:        u.CacheReadTokens,
			CacheCreationTokens: u.CacheCreationTokens,
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": "proxy_error"},
	})
}

// ErrServerClosed re-exports for main.
var ErrServerClosed = errors.New("server closed")
