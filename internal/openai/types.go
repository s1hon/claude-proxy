package openai

import "encoding/json"

// ChatCompletionRequest is the incoming OpenAI-compatible request body.
type ChatCompletionRequest struct {
	Model           string          `json:"model"`
	Messages        []Message       `json:"messages"`
	Tools           []Tool          `json:"tools,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Temperature     json.RawMessage `json:"temperature,omitempty"`
	MaxTokens       json.RawMessage `json:"max_tokens,omitempty"`
}

// Message is one entry in the chat history. content can be a string or an array
// of content parts, so we keep it as RawMessage and decode in convert.
type Message struct {
	Role       string          `json:"role"`
	Name       string          `json:"name,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// Tool is an OpenAI function tool definition.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function tool.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is an outbound request for the client to execute a tool.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction carries the name and JSON-encoded arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatCompletionResponse is the full (non-streaming) response.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one alternative response.
type Choice struct {
	Index        int            `json:"index"`
	Message      ChoiceMessage  `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

// ChoiceMessage is the assistant message in a Choice.
type ChoiceMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Usage reports token counts.
type Usage struct {
	PromptTokens        int                 `json:"prompt_tokens"`
	CompletionTokens    int                 `json:"completion_tokens"`
	TotalTokens         int                 `json:"total_tokens"`
	PromptTokensDetails PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// PromptTokensDetails breaks down cached vs new input tokens.
type PromptTokensDetails struct {
	CachedTokens         int `json:"cached_tokens,omitempty"`
	CacheCreationTokens  int `json:"cache_creation_tokens,omitempty"`
}

// ModelsResponse is the /v1/models response.
type ModelsResponse struct {
	Object string       `json:"object"`
	Data   []ModelEntry `json:"data"`
}

// ModelEntry describes one available model.
type ModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
