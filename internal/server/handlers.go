package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
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

	// Be lenient with unknown fields — many OpenAI clients add extras.
	var req openai.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	inv := h.prepare(&req)

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
func (h *Handler) prepare(req *openai.ChatCompletionRequest) invocation {
	channel := session.ExtractChannelLabel(req.Messages)
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
