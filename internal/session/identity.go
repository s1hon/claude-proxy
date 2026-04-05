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
	// convInfoRe captures a "Conversation info (untrusted metadata)" JSON
	// block from a user message. Used as a fallback (non-OpenClaw clients)
	// and also for pulling topic_id out of forum group messages.
	convInfoRe = regexp.MustCompile(`(?s)Conversation info \(untrusted metadata\)[^{]*?(\{.*?\})`)

	// inboundMetaRe captures the OpenClaw routing envelope embedded in the
	// system prompt. Primary source of identity for OpenClaw-driven traffic.
	inboundMetaRe = regexp.MustCompile(`(?s)\{[^{}]*?"schema"\s*:\s*"openclaw\.inbound_meta\.v1"[^{}]*?\}`)

	// agentRe matches a "**Name:** X" line. Allows leading whitespace or
	// bullet markers (OpenClaw embeds these inside `- **Name:** ...` list
	// items inside IDENTITY.md, which the old anchored regex never matched).
	agentRe = regexp.MustCompile(`(?m)^[\s\-*]*\*\*Name:\*\*\s*([^\r\n]+)`)
)

// inboundMeta mirrors the fields we care about inside an
// openclaw.inbound_meta.v1 envelope.
type inboundMeta struct {
	Schema    string `json:"schema"`
	ChatID    string `json:"chat_id"`
	AccountID string `json:"account_id"`
	Channel   string `json:"channel"`
	Provider  string `json:"provider"`
	Surface   string `json:"surface"`
	ChatType  string `json:"chat_type"`
}

// extractInboundMeta scans system/developer messages for an
// openclaw.inbound_meta.v1 JSON envelope and returns it when found.
func extractInboundMeta(msgs []openai.Message) (inboundMeta, bool) {
	for _, m := range msgs {
		if m.Role != "system" && m.Role != "developer" {
			continue
		}
		c := convert.ExtractContent(m.Content)
		if c == "" || !strings.Contains(c, "openclaw.inbound_meta.v1") {
			continue
		}
		match := inboundMetaRe.FindString(c)
		if match == "" {
			continue
		}
		var meta inboundMeta
		if err := json.Unmarshal([]byte(match), &meta); err != nil {
			continue
		}
		if meta.Schema == "openclaw.inbound_meta.v1" {
			return meta, true
		}
	}
	return inboundMeta{}, false
}

// extractTopicID walks user messages looking for a Conversation info JSON
// block with topic_id + is_forum. Used to disambiguate forum topics within
// the same group chat_id.
func extractTopicID(msgs []openai.Message) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		c := convert.ExtractContent(m.Content)
		if c == "" || !strings.Contains(c, "Conversation info") {
			continue
		}
		match := convInfoRe.FindStringSubmatch(c)
		if len(match) < 2 {
			continue
		}
		var info struct {
			TopicID any  `json:"topic_id"`
			IsForum bool `json:"is_forum"`
		}
		if err := json.Unmarshal([]byte(match[1]), &info); err != nil {
			continue
		}
		if !info.IsForum || info.TopicID == nil {
			return ""
		}
		switch v := info.TopicID.(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return strings.TrimRight(strings.TrimRight(formatFloat(v), "0"), ".")
		}
	}
	return ""
}

func formatFloat(v float64) string {
	// Keep it dependency-free: int64 round-trip is enough for Telegram ids.
	return strings.TrimSuffix(strings.TrimSuffix(jsonNumber(v), "0"), ".")
}

func jsonNumber(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ExtractChannelLabel returns a routing-stable channel identifier for the
// current request. Preference order:
//  1. OpenClaw `openclaw.inbound_meta.v1` envelope in the system prompt
//     (gives us provider + chat_id + chat_type directly). Forum topic_id is
//     merged in from the user-message Conversation info block when present.
//  2. Legacy "Conversation info (untrusted metadata)" user-message block
//     with Discord-style guild/channel/username/dm fields.
//
// Returns "" when nothing identifying is found.
func ExtractChannelLabel(msgs []openai.Message) string {
	if meta, ok := extractInboundMeta(msgs); ok && meta.ChatID != "" {
		label := meta.ChatID
		if meta.ChatType == "direct" {
			// Flag DMs so two agents talking to the same chat_id in
			// different modes can't collide with group routing.
			label = "dm:" + label
		}
		if topic := extractTopicID(msgs); topic != "" {
			label += "/topic:" + topic
		}
		return label
	}
	return legacyChannelLabel(msgs)
}

// legacyChannelLabel handles the pre-OpenClaw Discord-style envelope. Kept
// for backward compatibility with clients that ship `guild`/`channel`/`dm`.
func legacyChannelLabel(msgs []openai.Message) string {
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

// ExtractAgentName returns the agent identifier for routing. Preference:
//  1. OpenClaw inbound_meta `account_id` (e.g. "main", "fitness", "stock").
//  2. A `**Name:** X` line in a system/developer message — this supports
//     both plain `**Name:**` and bulleted `- **Name:**` forms.
//
// Falls back to "default" only when neither signal is present.
func ExtractAgentName(msgs []openai.Message) string {
	if meta, ok := extractInboundMeta(msgs); ok && meta.AccountID != "" {
		return meta.AccountID
	}
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
