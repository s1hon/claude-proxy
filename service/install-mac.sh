#!/bin/bash
set -euo pipefail

# claude-proxy — macOS LaunchAgent installer
#
# Resolves a binary in this order:
#   1. bin/claude-proxy in the project tree (existing build)
#   2. `go build` if Go is installed
#   3. Pre-built release binary downloaded from GitHub (no Go needed)
#
# Then detects claude CLI, reads .env, generates a plist, and loads the service.

LABEL="com.claude-proxy"
PLIST_PATH="$HOME/Library/LaunchAgents/${LABEL}.plist"
RELEASE_REPO="s1hon/claude-proxy"
RELEASE_TAG="${CLAUDE_PROXY_RELEASE:-latest}"

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

# --- Resolve binary: existing → build → download ---

if [[ ! -x "$BIN_PATH" ]]; then
  mkdir -p "$(dirname "$BIN_PATH")"
  GO_BIN="$(which go 2>/dev/null || true)"
  if [[ -n "$GO_BIN" ]]; then
    echo ""
    echo "Building from source with $GO_BIN..."
    (cd "$PROJECT_DIR" && "$GO_BIN" build -ldflags="-s -w" -o "$BIN_PATH" ./cmd/claude-proxy)
    echo "  Built: $BIN_PATH"
  else
    # No Go available — download a pre-built release binary.
    ARCH="$(uname -m)"
    case "$ARCH" in
      arm64|aarch64) ASSET="claude-proxy-darwin-arm64" ;;
      x86_64|amd64)  ASSET="claude-proxy-darwin-amd64" ;;
      *) echo "ERROR: unsupported architecture: $ARCH"; exit 1 ;;
    esac
    if [[ "$RELEASE_TAG" == "latest" ]]; then
      URL="https://github.com/${RELEASE_REPO}/releases/latest/download/${ASSET}"
    else
      URL="https://github.com/${RELEASE_REPO}/releases/download/${RELEASE_TAG}/${ASSET}"
    fi
    echo ""
    echo "Go not found — downloading pre-built binary"
    echo "  $URL"
    if ! curl -fSL --progress-bar -o "$BIN_PATH" "$URL"; then
      echo "ERROR: failed to download $URL"
      echo "  Either install Go (brew install go) and re-run,"
      echo "  or fetch manually from https://github.com/${RELEASE_REPO}/releases"
      rm -f "$BIN_PATH"
      exit 1
    fi
    chmod +x "$BIN_PATH"
    # macOS Gatekeeper: strip quarantine so launchd can execute it.
    xattr -d com.apple.quarantine "$BIN_PATH" 2>/dev/null || true
    echo "  Downloaded: $BIN_PATH"
  fi
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

    <!-- Merge stdout and stderr into a single log file. Go's log package
         writes to stderr by default, so without this both streams would
         be split across two files with the more interesting output ending
         up in claude-proxy.err.log. -->
    <key>StandardOutPath</key>
    <string>${PROJECT_DIR}/claude-proxy.log</string>

    <key>StandardErrorPath</key>
    <string>${PROJECT_DIR}/claude-proxy.log</string>
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
echo "  Health:   curl http://127.0.0.1:3456/health"
