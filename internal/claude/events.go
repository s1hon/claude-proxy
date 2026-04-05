package claude

import "encoding/json"

// Event is one line of --output-format stream-json output from Claude CLI.
// Unknown fields are ignored.
type Event struct {
	Type         string          `json:"type"`
	Result       json.RawMessage `json:"result,omitempty"`
	Usage        *RawUsage       `json:"usage,omitempty"`
	TotalCostUSD float64         `json:"total_cost_usd,omitempty"`
}

// RawUsage mirrors Claude CLI's usage breakdown.
type RawUsage struct {
	InputTokens             int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens    int `json:"cache_read_input_tokens"`
	OutputTokens            int `json:"output_tokens"`
}

// Usage is the normalised token accounting returned by Run.
type Usage struct {
	InputTokens         int
	CacheCreationTokens int
	CacheReadTokens     int
	OutputTokens        int
	CostUSD             float64
}
