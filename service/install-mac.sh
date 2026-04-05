#!/bin/bash
set -euo pipefail

# claude-proxy — macOS LaunchAgent installer
# Builds the Go binary (if needed), detects claude CLI, reads .env,
# generates a plist, and loads the service.

LABEL="com.claude-proxy"
PLIST_PATH="$HOME/Library/LaunchAgents/${LABEL}.plist"

# --- Detect paths ---

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_PATH="$PROJECT_DIR/bin/claude-proxy"

CLAUDE_BIN="$(which claude 2>/dev/null || true)"
if [[ -z "$CLAUDE_BIN" ]]; then
  echo "ERROR: claude not found in PATH"
  echo "  Install Claude Code: https://docs.anthropic.com/en/docs/claude-code"
  exit 1
fi
CLAUDE_DIR="$(dirname "$CLAUDE_BIN")"

echo "=== claude-proxy macOS installer ==="
echo "  Project dir: $PROJECT_DIR"
echo "  Binary:      $BIN_PATH"
echo "  Claude:      $CLAUDE_BIN"

# --- Build binary if missing or stale ---

if [[ ! -x "$BIN_PATH" ]]; then
  echo ""
  echo "Binary not found, building..."
  GO_BIN="$(which go 2>/dev/null || true)"
  if [[ -z "$GO_BIN" ]]; then
    echo "ERROR: go not found in PATH (needed to build the binary)"
    echo "  Install Go: brew install go"
    exit 1
  fi
  (cd "$PROJECT_DIR" && "$GO_BIN" build -o "$BIN_PATH" ./cmd/claude-proxy)
  echo "  Built: $BIN_PATH"
fi

# --- Check claude auth ---

echo ""
echo "Checking claude auth status..."
if "$CLAUDE_BIN" auth status 2>&1 | grep -qi "logged in\|authenticated"; then
  echo "  Claude auth OK"
else
  echo "WARNING: claude may not be logged in"
  echo "  Run: claude auth login"
  echo "  Continuing anyway..."
fi

# --- Build PATH for launchd (login shells don't inherit user PATH) ---

PATH_PARTS="/usr/local/bin:/usr/bin:/bin"
for dir in "$CLAUDE_DIR" "$HOME/.local/bin" "/opt/homebrew/bin"; do
  if [[ ":$PATH_PARTS:" != *":$dir:"* ]]; then
    PATH_PARTS="$dir:$PATH_PARTS"
  fi
done

# --- Read optional .env into plist EnvironmentVariables ---

ENV_FILE="$PROJECT_DIR/.env"
ENV_ENTRIES=""

xml_escape() {
  local s="$1"
  s="${s//&/&amp;}"
  s="${s//</&lt;}"
  s="${s//>/&gt;}"
  s="${s//\"/&quot;}"
  printf '%s' "$s"
}

if [[ -f "$ENV_FILE" ]]; then
  echo ""
  echo "Reading $ENV_FILE"
  while IFS= read -r line || [[ -n "$line" ]]; do
    # Skip comments and empty lines
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    key="${line%%=*}"
    value="${line#*=}"
    [[ -z "$key" || "$key" == "$line" ]] && continue
    # Strip surrounding quotes
    value="${value#\"}" ; value="${value%\"}"
    value="${value#\'}" ; value="${value%\'}"
    value_escaped="$(xml_escape "$value")"
    ENV_ENTRIES+="        <key>${key}</key>
        <string>${value_escaped}</string>
"
  done < "$ENV_FILE"
else
  echo ""
  echo "No .env file at $ENV_FILE — using built-in defaults"
fi

# --- Generate plist ---

mkdir -p "$(dirname "$PLIST_PATH")"

cat > "$PLIST_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${LABEL}</string>

    <key>ProgramArguments</key>
    <array>
        <string>${BIN_PATH}</string>
    </array>

    <key>WorkingDirectory</key>
    <string>${PROJECT_DIR}</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>${PATH_PARTS}</string>
        <key>HOME</key>
        <string>${HOME}</string>
        <key>CLAUDE_BIN</key>
        <string>${CLAUDE_BIN}</string>
${ENV_ENTRIES}    </dict>

    <key>RunAtLoad</key>
    <true/>

    <key>KeepAlive</key>
    <true/>

    <key>ProcessType</key>
    <string>Background</string>

    <key>StandardOutPath</key>
    <string>${PROJECT_DIR}/claude-proxy.log</string>

    <key>StandardErrorPath</key>
    <string>${PROJECT_DIR}/claude-proxy.err.log</string>
</dict>
</plist>
PLIST

echo ""
echo "Generated plist: $PLIST_PATH"

# --- Load service ---

echo ""
echo "Loading LaunchAgent..."

DOMAIN="gui/$(id -u)"
if launchctl print "$DOMAIN/$LABEL" &>/dev/null; then
  echo "  Unloading existing service..."
  launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
  sleep 1
fi

launchctl bootstrap "$DOMAIN" "$PLIST_PATH"
echo "  Service loaded"

# --- Verify ---

echo ""
sleep 2
if launchctl print "$DOMAIN/$LABEL" &>/dev/null; then
  echo "=== Installed successfully ==="
else
  echo "WARNING: Service may not have started correctly"
  echo "  Check: launchctl print $DOMAIN/$LABEL"
fi

echo ""
echo "Management commands:"
echo "  Status:   launchctl print $DOMAIN/$LABEL | head"
echo "  Restart:  launchctl bootout $DOMAIN/$LABEL && launchctl bootstrap $DOMAIN $PLIST_PATH"
echo "  Stop:     launchctl bootout $DOMAIN/$LABEL"
echo "  Logs:     tail -f $PROJECT_DIR/claude-proxy.log"
echo "  Errors:   tail -f $PROJECT_DIR/claude-proxy.err.log"
echo "  Health:   curl http://127.0.0.1:3456/health"
