package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/s1hon/claude-proxy/internal/openai"
)

// sseWriter writes OpenAI-compatible streaming chunks to the HTTP response.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	id      string
	model   string
	created int64
}

func newSSEWriter(w http.ResponseWriter, id, model string, created int64) (*sseWriter, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &sseWriter{w: w, flusher: f, id: id, model: model, created: created}, nil
}

// sendDelta writes one content delta chunk.
func (s *sseWriter) sendDelta(text string) error {
	chunk := openai.StreamChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openai.StreamChoice{{
			Index: 0,
			Delta: openai.StreamDelta{Content: text},
		}},
	}
	return s.writeChunk(chunk)
}

// sendRole writes the initial role marker (required by some clients).
func (s *sseWriter) sendRole() error {
	chunk := openai.StreamChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openai.StreamChoice{{
			Index: 0,
			Delta: openai.StreamDelta{Role: "assistant"},
		}},
	}
	return s.writeChunk(chunk)
}

// sendToolCalls emits tool_calls as a delta and marks finish_reason.
func (s *sseWriter) sendToolCalls(calls []openai.ToolCall) error {
	stop := "tool_calls"
	chunk := openai.StreamChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openai.StreamChoice{{
			Index:        0,
			Delta:        openai.StreamDelta{ToolCalls: calls},
			FinishReason: &stop,
		}},
	}
	return s.writeChunk(chunk)
}

// sendFinish writes the terminating chunk with finish_reason=stop.
func (s *sseWriter) sendFinish(reason string, usage *openai.Usage) error {
	chunk := openai.StreamChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []openai.StreamChoice{{
			Index:        0,
			Delta:        openai.StreamDelta{},
			FinishReason: &reason,
		}},
		Usage: usage,
	}
	return s.writeChunk(chunk)
}

// done writes the terminating [DONE] marker.
func (s *sseWriter) done() {
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	s.flusher.Flush()
}

func (s *sseWriter) writeChunk(c openai.StreamChunk) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
