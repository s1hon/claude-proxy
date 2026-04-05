package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration read from environment variables.
type Config struct {
	APIPort       int
	StatusPort    int
	ClaudeBin     string
	OpusModel     string
	SonnetModel   string
	HaikuModel    string
	IdleTimeout   time.Duration
	HardTimeout   time.Duration
	MaxPerChannel int
	MaxGlobal     int
	StatePath     string
	RewriteTerms  []string
}

// Load reads configuration from environment variables, applying defaults.
func Load() Config {
	return Config{
		APIPort:       envInt("CLAUDE_PROXY_PORT", 3456),
		StatusPort:    envInt("CLAUDE_PROXY_STATUS_PORT", 3458),
		ClaudeBin:     envStr("CLAUDE_BIN", "claude"),
		OpusModel:     envStr("OPUS_MODEL", "opus"),
		SonnetModel:   envStr("SONNET_MODEL", "sonnet"),
		HaikuModel:    envStr("HAIKU_MODEL", "haiku"),
		IdleTimeout:   time.Duration(envInt("IDLE_TIMEOUT_MS", 120000)) * time.Millisecond,
		HardTimeout:   time.Duration(envInt("HARD_TIMEOUT_MS", 20*60*1000)) * time.Millisecond,
		MaxPerChannel: envInt("MAX_PER_CHANNEL", 2),
		MaxGlobal:     envInt("MAX_GLOBAL", 20),
		StatePath:     envStr("STATE_PATH", "state.json"),
		RewriteTerms:  parseTerms(envStr("REWRITE_TERMS", "OpenClaw,openclaw")),
	}
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func parseTerms(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
