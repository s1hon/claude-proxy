package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s1hon/claude-proxy/internal/config"
	"github.com/s1hon/claude-proxy/internal/openai"
	"github.com/s1hon/claude-proxy/internal/session"
	"github.com/s1hon/claude-proxy/internal/stats"
)

// writeFakeCLI creates a shell script in a temp dir that prints the given
// stream-json lines and returns the script path.
func writeFakeCLI(t *testing.T, streamLines []string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-claude")

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	sb.WriteString("cat > /dev/null\n")
	sb.WriteString("sleep 0.05\n")
	for _, line := range streamLines {
		sb.WriteString("printf '%s\\n' '")
		escaped := strings.ReplaceAll(line, "'", "'\\''")
		sb.WriteString(escaped)
		sb.WriteString("'\n")
	}

	if err := os.WriteFile(script, []byte(sb.String()), 0o755); err != nil {
		t.Fatalf("writeFakeCLI: %v", err)
	}
	return script
}

// newTestDeps builds minimal Deps suitable for tests.
func newTestDeps(t *testing.T, bin string) Deps {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cfg := config.Config{
		ClaudeBin:     bin,
		OpusModel:     "opus",
		SonnetModel:   "sonnet",
		HaikuModel:    "haiku",
		MaxPerChannel: 2,
		MaxGlobal:     20,
		StatePath:     statePath,
	}
	store := session.NewStore(statePath)
	st := stats.New()
	limiter := NewLimiter(20, 2)
	return Deps{
		Config:  cfg,
		Store:   store,
		Stats:   st,
		Limiter: limiter,
	}
}

// chatRequest serialises a minimal chat completion request body.
func chatRequest(t *testing.T, model string, stream bool, content string) io.Reader {
	t.Helper()
	contentBytes, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	req := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": json.RawMessage(contentBytes)},
		},
		"stream": stream,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReader(string(b))
}

// ---- Tests ----

func TestHealth(t *testing.T) {
	h := NewHandler(newTestDeps(t, ""))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body["status"] = %q, want "ok"`, body["status"])
	}
}

func TestModels(t *testing.T) {
	h := NewHandler(newTestDeps(t, ""))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp openai.ModelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("len(Data) = %d, want 3", len(resp.Data))
	}
	wantIDs := []string{"claude-opus-latest", "claude-sonnet-latest", "claude-haiku-latest"}
	for i, entry := range resp.Data {
		if entry.ID != wantIDs[i] {
			t.Errorf("Data[%d].ID = %q, want %q", i, entry.ID, wantIDs[i])
		}
	}
}

func TestStatus(t *testing.T) {
	h := NewHandler(newTestDeps(t, ""))
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var snap stats.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// Just verify it decoded without error — fields are zero for a fresh Stats.
}

func TestChatCompletions_MethodNotAllowed(t *testing.T) {
	h := NewHandler(newTestDeps(t, ""))
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestChatCompletions_JSON(t *testing.T) {
	resultLine := `{"type":"result","result":"hi there","usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"total_cost_usd":0.0005}`
	bin := writeFakeCLI(t, []string{resultLine})

	deps := newTestDeps(t, bin)
	h := NewHandler(deps)

	body := chatRequest(t, "claude-sonnet-latest", false, "hello")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw: %s", err, rec.Body.String())
	}
	if len(resp.Choices) == 0 {
		t.Fatal("choices is empty")
	}
	if resp.Choices[0].Message.Content != "hi there" {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "hi there")
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", resp.Choices[0].FinishReason, "stop")
	}
	if resp.Usage.PromptTokens <= 0 {
		t.Errorf("prompt_tokens = %d, want > 0", resp.Usage.PromptTokens)
	}
}

func TestChatCompletions_ToolCall(t *testing.T) {
	toolText := "I'll search.\n<tool_call>\n{\"name\":\"web_search\",\"arguments\":{\"q\":\"bitcoin\"}}\n</tool_call>"
	resultLine := `{"type":"result","result":"` + jsonEscape(toolText) + `","usage":{"input_tokens":5,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"total_cost_usd":0.001}`
	bin := writeFakeCLI(t, []string{resultLine})

	deps := newTestDeps(t, bin)
	h := NewHandler(deps)

	body := chatRequest(t, "claude-sonnet-latest", false, "search bitcoin")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v; raw: %s", err, rec.Body.String())
	}
	if len(resp.Choices) == 0 {
		t.Fatal("choices is empty")
	}
	choice := resp.Choices[0]
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(choice.Message.ToolCalls))
	}
	if choice.Message.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("tool name = %q, want %q", choice.Message.ToolCalls[0].Function.Name, "web_search")
	}
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want %q", choice.FinishReason, "tool_calls")
	}
	if strings.Contains(choice.Message.Content, "<tool_call>") {
		t.Errorf("content should not contain <tool_call>, got: %q", choice.Message.Content)
	}
}

func TestChatCompletions_SSE(t *testing.T) {
	resultLine := `{"type":"result","result":"streaming reply","usage":{"input_tokens":4,"output_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"total_cost_usd":0.0002}`
	bin := writeFakeCLI(t, []string{resultLine})

	deps := newTestDeps(t, bin)
	h := NewHandler(deps)

	// Use httptest.NewServer so the ResponseWriter implements http.Flusher.
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	body := chatRequest(t, "claude-sonnet-latest", true, "stream test")
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, b)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", contentType)
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
	}

	rawBody := strings.Join(lines, "\n")

	if !strings.Contains(rawBody, "data: ") {
		t.Errorf("SSE body missing 'data: ' prefix; got:\n%s", rawBody)
	}
	if !strings.Contains(rawBody, "[DONE]") {
		t.Errorf("SSE body missing '[DONE]'; got:\n%s", rawBody)
	}

	// Verify at least one delta chunk with content.
	foundDelta := false
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var chunk openai.StreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				foundDelta = true
			}
		}
	}
	if !foundDelta {
		t.Errorf("no delta chunk with content found in SSE stream:\n%s", rawBody)
	}
}

// jsonEscape returns s safe for embedding in a JSON string value
// (escapes backslashes, double-quotes, and newlines).
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// json.Marshal wraps in quotes; strip them.
	return string(b[1 : len(b)-1])
}
