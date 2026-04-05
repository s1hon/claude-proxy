// Package session owns session-id resolution, channel/agent identity
// extraction, and durable state persistence.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/s1hon/claude-proxy/internal/convert"
	"github.com/s1hon/claude-proxy/internal/openai"
)

var (
	convInfoRe = regexp.MustCompile(`(?s)Conversation info \(untrusted metadata\)[^{]*?(\{.*?\})`)
	agentRe    = regexp.MustCompile(`(?m)^\*\*Name:\*\*\s*([^\r\n]+)`)
)

// ExtractChannelLabel looks at user messages for a "Conversation info
// (untrusted metadata)" JSON block and returns a human-readable label.
// Returns "" if no label is found.
func ExtractChannelLabel(msgs []openai.Message) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		c := convert.ExtractContent(m.Content)
		if c == "" {
			continue
		}
		match := convInfoRe.FindStringSubmatch(c)
		if len(match) < 2 {
			continue
		}
		var info struct {
			Guild    string `json:"guild"`
			Channel  string `json:"channel"`
			Username string `json:"username"`
			DM       bool   `json:"dm"`
		}
		if err := json.Unmarshal([]byte(match[1]), &info); err != nil {
			continue
		}
		if info.DM && info.Username != "" {
			return "dm:" + info.Username
		}
		if info.Guild != "" && info.Channel != "" {
			return info.Guild + " #" + info.Channel
		}
		if info.Channel != "" {
			return "#" + info.Channel
		}
		if info.Username != "" {
			return "dm:" + info.Username
		}
	}
	return ""
}

// ExtractAgentName looks at developer/system messages for a "**Name:** X"
// line. Returns "default" if nothing is found.
func ExtractAgentName(msgs []openai.Message) string {
	for _, m := range msgs {
		if m.Role != "developer" && m.Role != "system" {
			continue
		}
		c := convert.ExtractContent(m.Content)
		if c == "" {
			continue
		}
		if match := agentRe.FindStringSubmatch(c); len(match) >= 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return "default"
}

// RoutingKey combines channel and agent into the primary session lookup key.
func RoutingKey(channel, agent string) string {
	if channel == "" {
		channel = "_"
	}
	if agent == "" {
		agent = "default"
	}
	return channel + "::" + agent
}

// NewSessionID returns a UUID-ish identifier suitable for --session-id.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	// RFC 4122 v4 bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
