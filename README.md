# claude-proxy

一個用 Go 寫的 OpenAI 相容 HTTP 代理,把任何支援 OpenAI API 的 Agent(例如 [OpenClaw](https://github.com/openclaw/openclaw)、[hermes-agent](https://github.com/nousresearch/hermes-agent))橋接到本機已登入的 [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code)。

只用 Go 標準函式庫,零外部依賴。

```
   Agent (OpenAI 格式)              claude-proxy                  Claude Code CLI
          │                              │                              │
          │  POST /v1/chat/              │                              │
          │  completions                 │  translate messages          │
          ├─────────────────────────────▶│  inject tool protocol        │
          │                              │                              │
          │                              │  stdin  ──▶ claude --print   │
          │                              │  stdout ◀── stream-json      │
          │                              │                              │
          │  JSON / SSE                  │  parse response:             │
          ◀─────────────────────────────│  ├─ text → clean output      │
          │                              │  └─ <tool_call> → OpenAI fmt │
```

**核心理念**:代理本身**不執行任何工具**,只翻譯格式。工具執行的責任完全留給上游 Agent。

---

## 功能

- ✅ **OpenAI 相容 API**:`POST /v1/chat/completions`(JSON + SSE 串流)、`GET /v1/models`、`GET /health`、`GET /status`
- ✅ **Session resume**:以 `channel::agent` 為 key 的 session 查詢,持久化到 `state.json`(atomic rename),重啟不丟上下文
- ✅ **Tool calling 協定**:動態把 OpenAI 的 `tools` array 轉成 `<tool_call>` XML 指引注入 system prompt,解析回 OpenAI 的 `tool_calls`
- ✅ **Extended thinking**:`reasoning_effort` 參數映射到 `claude --effort`
- ✅ **Context refresh**:偵測上游壓縮後的摘要,自動用 `MessagesCompact` 重建新 session;tool loop 進行中會 defer
- ✅ **CLI 失敗自動重試**:第一次失敗時以 compact prompt + 新 session 再試一次
- ✅ **並發控制**:per-channel 與全域 semaphore
- ✅ **Idle / hard timeout**:2 分鐘無輸出 kill、20 分鐘絕對上限
- ✅ **關鍵字別名替換**:雙向替換可設定關鍵字與 15×15 別名池之間的字串(`REWRITE_TERMS` env)
- ✅ **Graceful shutdown**:`SIGTERM` 時等待 in-flight 請求完成再結束
- ❌ 無 Dashboard 前端(純 CLI / HTTP)

---

## 需求

| 項目 | 版本 | 備註 |
|---|---|---|
| Go | >= 1.22 | 僅 build 時需要 |
| Claude Code CLI | 最新 | 必須已登入:`claude auth login` |
| OS | macOS / Linux | 已在 macOS arm64 驗證 |

---

## 快速開始

```bash
# 1. Clone
git clone https://github.com/s1hon/claude-proxy.git
cd claude-proxy

# 2. 編譯
make build

# 3. 啟動(前景)
./bin/claude-proxy
# 或 make run

# 4. 健康檢查
curl http://127.0.0.1:3456/health
```

---

## 設定(環境變數)

所有設定都透過 env 讀取,啟動前 `export` 或寫進 `.env` 後用 shell 載入即可。

| 變數 | 預設 | 說明 |
|---|---|---|
| `CLAUDE_PROXY_PORT` | `3456` | API server port(`127.0.0.1` only) |
| `CLAUDE_PROXY_STATUS_PORT` | `3458` | Status server port(`0.0.0.0`) |
| `CLAUDE_BIN` | `claude` | Claude Code CLI 執行檔路徑 |
| `OPUS_MODEL` | `opus` | Opus 的 CLI alias;要 1M 上下文改 `opus[1m]` |
| `SONNET_MODEL` | `sonnet` | Sonnet 的 CLI alias;要 1M 上下文改 `sonnet[1m]` |
| `HAIKU_MODEL` | `haiku` | Haiku 的 CLI alias(無 1M 變體) |
| `IDLE_TIMEOUT_MS` | `120000` | Claude CLI 無輸出超時(2 分鐘) |
| `HARD_TIMEOUT_MS` | `1200000` | 絕對超時(20 分鐘) |
| `MAX_PER_CHANNEL` | `2` | 每個 channel 最大並發 |
| `MAX_GLOBAL` | `20` | 全域最大並發 |
| `STATE_PATH` | `state.json` | 持久化狀態檔路徑 |
| `REWRITE_TERMS` | `OpenClaw,openclaw` | 逗號分隔的關鍵字,留空停用替換 |

---

## API 端點

| Method | Path | Port | 說明 |
|---|---|---|---|
| `GET` | `/health` | 3456 | `{"status":"ok"}` |
| `GET` | `/v1/models` | 3456 | 可用模型清單 |
| `POST` | `/v1/chat/completions` | 3456 | OpenAI 相容聊天補全(JSON 或 SSE) |
| `GET` | `/status` | 3458 | 執行時統計 snapshot |

### 可用模型 ID

- `claude-opus-latest`
- `claude-sonnet-latest`
- `claude-haiku-latest`

這三個 ID 會被 `internal/claude/model.go` 的 `ResolveModel` 映射到 CLI alias(`opus` / `sonnet` / `haiku`)。

---

## OpenClaw 設定範例

把下面這段貼到 `~/.openclaw/openclaw.json` 的 `models.providers`:

```json
{
  "models": {
    "providers": {
      "claude-proxy": {
        "baseUrl": "http://localhost:3456/v1",
        "apiKey": "not-needed",
        "api": "openai-completions",
        "models": [
          {
            "id": "claude-opus-latest",
            "name": "Claude Opus",
            "contextWindow": 1000000,
            "maxTokens": 128000,
            "reasoning": true
          },
          {
            "id": "claude-sonnet-latest",
            "name": "Claude Sonnet",
            "contextWindow": 1000000,
            "maxTokens": 64000,
            "reasoning": true
          },
          {
            "id": "claude-haiku-latest",
            "name": "Claude Haiku",
            "contextWindow": 200000,
            "maxTokens": 64000,
            "reasoning": false
          }
        ]
      }
    }
  }
}
```

**注意**:

- `apiKey` 隨便填非空字串,proxy 不檢查
- 要 1M 上下文必須啟動 proxy 時設 `OPUS_MODEL=opus[1m] SONNET_MODEL=sonnet[1m]`
- Haiku 沒有 1M 變體,上限為 200K
- `maxTokens` 對 proxy 無效,只是告訴 OpenClaw 模型能吐多少 output token

---

## Session Routing

Proxy 會根據以下優先順序辨識不同的對話,確保 session 隔離:

1. **OpenClaw envelope**:system prompt 中的 `openclaw.inbound_meta.v1` JSON(chat_id + account_id)
2. **Legacy Discord**:user message 中的 `Conversation info (untrusted metadata)` JSON(guild/channel/username)
3. **HTTP header**:`X-Hermes-Session-Id`(hermes-agent)或通用的 `X-Session-Id`
4. **Fallback**:所有請求共用同一個 session

只要 client 送出上述任一信號,proxy 就能自動隔離不同對話。

### Model 驗證

Proxy 只接受以下 model ID,其他 model(如 `google/gemini-3-flash-preview`)會直接回 400:

- `claude-opus-latest` / `claude-opus` / `opus`
- `claude-sonnet-latest` / `claude-sonnet` / `sonnet`
- `claude-haiku-latest` / `claude-haiku` / `haiku`

---

## hermes-agent 設定範例

在 `~/.hermes/config.yaml`(或 `~/.hermes/profiles/<name>/config.yaml`)的 `model:` 區段設定:

```yaml
model:
  default: "claude-opus-latest"
  provider: "custom"
  api_key: "not-needed"
  base_url: "http://localhost:3456/v1"
```

hermes-agent 需要額外 patch `run_agent.py` 在 `_build_api_kwargs()` 的 return 前注入 session header:

```python
if self.session_id:
    api_kwargs["extra_headers"] = {"X-Hermes-Session-Id": self.session_id}
```

### OpenClaw vs hermes-agent 設定差異

| | OpenClaw | hermes-agent |
|---|---|---|
| **Session 隔離** | 自動(system prompt 內嵌 `inbound_meta.v1` envelope) | 需 patch `run_agent.py` 送出 `X-Hermes-Session-Id` header |
| **Model ID** | 直接在 `openclaw.json` 的 models array 裡指定 | 在 `config.yaml` 的 `model.default` 指定 |
| **API key** | `openclaw.json` 填任意非空字串 | `config.yaml` 填 `"not-needed"` |
| **Auxiliary models** | 不經過 proxy | 預設也會經過 proxy(因 `auto` fallback);不影響功能,proxy 會回 400 拒絕不認識的 model |
| **Tool execution** | OpenClaw 負責 | hermes-agent 負責 |
| **關鍵字替換** | `REWRITE_TERMS=OpenClaw,openclaw`(預設啟用) | 不影響(hermes 請求中不含 OpenClaw 字串) |

---

## macOS 服務安裝(launchd)

```bash
./service/install-mac.sh
```

這個腳本會:

1. 偵測 `go` 與 `claude` 路徑
2. 若 `bin/claude-proxy` 不存在會自動 `go build`
3. 檢查 `claude auth status`
4. 讀取 `.env`(若存在)把 env 寫進 plist 的 `EnvironmentVariables`
5. 產生 `~/Library/LaunchAgents/com.claude-proxy.plist` 並 `launchctl bootstrap`

管理指令:

```bash
# 看狀態
launchctl print gui/$(id -u)/com.claude-proxy | head

# 重啟
launchctl bootout gui/$(id -u)/com.claude-proxy && \
  launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.claude-proxy.plist

# 看 log
tail -f claude-proxy.log
tail -f claude-proxy.err.log
```

---

## 開發

```bash
make build      # 編譯 bin/claude-proxy
make run        # build 後啟動
make test       # go test ./...
make vet        # go vet ./...
make tidy       # go mod tidy
make clean      # 移除 bin/ 與 state.json
```

### 專案結構

```
claude-proxy/
├── cmd/claude-proxy/main.go       # 入口 + signal handling
├── internal/
│   ├── config/                    # 環境變數解析
│   ├── openai/                    # OpenAI API types
│   ├── convert/                   # OpenAI messages → Claude CLI 文字
│   │   ├── convert.go             #   完整轉換
│   │   ├── extract.go             #   resume 模式抽取增量訊息
│   │   └── compact.go             #   context refresh 的壓縮版
│   ├── tools/                     # Tool calling 協定
│   │   ├── inject.go              #   建立 system prompt 指引
│   │   └── parse.go               #   從 Claude 回應抽取 <tool_call>
│   ├── claude/                    # Claude CLI 子程序
│   │   ├── runner.go              #   spawn + stream-json 解析
│   │   ├── events.go              #   事件 struct
│   │   └── model.go               #   model / effort 映射
│   ├── session/                   # Session 路由 + 持久化
│   │   ├── identity.go            #   channel/agent 辨識
│   │   └── store.go               #   state.json atomic store
│   ├── rewrite/                   # 關鍵字別名替換(15×15)
│   ├── server/                    # HTTP 層
│   │   ├── server.go              #   http.Server 組裝
│   │   ├── handlers.go            #   chat completions + refresh + retry
│   │   ├── sse.go                 #   SSE 串流
│   │   └── limits.go              #   並發 semaphore
│   └── stats/                     # atomic 計數器
├── service/
│   ├── install-mac.sh             # macOS launchd 安裝腳本
│   └── com.claude-proxy.plist     # plist 範本
├── .env.example
├── Makefile
└── go.mod
```

---

## 安全性

- **Port 3456(API)**:綁定 `127.0.0.1`,外部網路無法連入,OpenClaw 從本機呼叫
- **Port 3458(Status)**:綁定 `0.0.0.0`(LAN 可達),本專案**沒有**做 HTTP Basic Auth,建議用 firewall 限制或改綁 localhost
- **`--tools ""`**:啟動 Claude CLI 時停用所有原生工具(Bash / Read / Write / WebSearch …),Claude **無法**在主機執行任何指令
- **`--dangerously-skip-permissions`**:headless 模式下跳過互動式權限確認,因為原生工具已被停用所以沒有額外攻擊面
- **`.env` 不會 commit**(`.gitignore` 已排除)
- **`state.json` 不會 commit**(同上),裡面只有 session ID 與文字片段,無憑證

---

## 授權

MIT

## 致謝

原始概念與架構參考自 [openclaw/openclaw-claude-bridge](https://github.com/openclaw/openclaw-claude-bridge)(Node.js 版本)。本專案是 Go 重寫 + 精簡版本,已針對惡意程式做完整 code review 後再實作,並省略了 Dashboard 前端。
