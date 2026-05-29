# gemini-web2api-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8.svg)](https://golang.org)
[![Docker](https://img.shields.io/badge/docker-distroless-blue)](Dockerfile)

[中文文档](README.md) | English

Convert Google Gemini's web interface into an OpenAI-compatible API. **Single binary**, **anonymous access works** (or attach a Google cookie for Pro routing), **Chrome 146 TLS fingerprint**, **SQLite persistence**, ships with a built-in **admin dashboard**.

> Protocol layer field-by-field equivalence verified against a community Python single-file reference implementation (also named `gemini-web2api`, stdlib only).

---

## What it does

```
[OpenAI SDK / Cherry Studio / Cursor / dify / newapi / ...]
    ↓ http://localhost:8083/v1/chat/completions
[gemini-web2api-go]
    ↓ Reverse-engineers gemini.google.com web protocol
[Google Gemini Web]
```

Not a wrapper around Google's official API ([generativelanguage.googleapis.com](https://generativelanguage.googleapis.com)) — instead, it directly proxies the **browser-side StreamGenerate protocol**, so **no Google API key, no paid quota required**.

## vs the reference implementation

A community Python single-file implementation (also named `gemini-web2api`, stdlib only) inspired this project's protocol layer. We carried it over verbatim but added engineering features on top:

| | Python single-file | gemini-web2api-go |
|---|---|---|
| Deploy | `python gemini_web2api.py` | Single ~25MB Docker image |
| Dependencies | stdlib only | Static binary, zero runtime deps |
| Fingerprint | urllib default (looks like an SDK) | **utls Chrome 146** (looks like a browser) |
| API auth | None | ✅ Bearer token / x-api-key |
| Persistence | None | ✅ SQLite, 30-day raw + permanent aggregates |
| Admin UI | None | ✅ Web dashboard (CN UI) |
| Rate limit | None | ✅ Per-IP-slot concurrency/RPM/RPH limiter |
| Proxy pool | Single static proxy | ✅ Runtime CRUD + circuit breaker + capacity-aware routing |
| Privacy | n/a | ✅ Prompt/response bodies **never persisted**, metadata only |

## Quick Start

### From source

```bash
go build -o gemini-web2api-go .
./gemini-web2api-go --port 8083 --admin-token your-admin-token
```

### Docker

```bash
docker compose up -d --build
```

Boot banner:

```
gemini-web2api-go v3.0.0
  Listening:   http://0.0.0.0:8083
  Base URL:    http://localhost:8083/v1
  API key:     sk-gemini-XX...XXXX  (mutable in admin UI)
  Admin UI:    http://localhost:8083/admin  (token auth)
  Impersonate: chrome_146
  Tokenizer:   tiktoken cl100k_base
  Per-IP limit: concurrent=5 / RPM=30 / RPH=80
```

### Make a request

```bash
curl http://localhost:8083/v1/chat/completions \
  -H "Authorization: Bearer sk-gemini-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.5-flash",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

OpenAI Python SDK works as-is:

```python
from openai import OpenAI
client = OpenAI(
    base_url="http://localhost:8083/v1",
    api_key="sk-gemini-..."  # see admin dashboard
)
resp = client.chat.completions.create(
    model="gemini-3.5-flash-thinking",
    messages=[{"role": "user", "content": "Explain quantum entanglement"}]
)
print(resp.choices[0].message.content)
```

## Admin dashboard

Open `http://localhost:8083/admin`, log in with your `--admin-token`.

- **Dashboard** — 24h KPIs + request volume / P50 latency dual-axis chart + per-model & per-proxy breakdown + IP slot usage + one-click connectivity probe
- **Requests** — Metadata-only listing (no prompt/response content), filter by status/model + pagination
- **Proxies** — Runtime CRUD + enable/disable + circuit breaker (each proxy = one independent IP slot)
- **Settings** — API key show/copy/rotate/customize + rate-limit config view

## Models

| Model | Description | Output |
|---|---|---|
| `gemini-3.5-flash` | Fast general-purpose | ~12k chars |
| `gemini-3.5-flash-thinking` | Deep thinking, longest output | **~20k chars** |
| `gemini-3.5-flash-thinking-lite` | Adaptive thinking depth | ~15k chars |
| `gemini-3.1-pro` | Pro (cookie required for true Pro routing) | ~12k chars |
| `gemini-auto` | Auto model selection | varies |
| `gemini-flash-lite` | Lightweight fast | ~10k chars |

Append `@think=N` to any model name to override thinking depth (`0` deepest, `4` shallowest):

```
gemini-3.5-flash-thinking@think=2
```

## Configuration

`config.json` (optional, CLI flags take precedence):

```json
{
  "port": 8083,
  "host": "0.0.0.0",
  "impersonate": "chrome_146",
  "db_path": "./data/gemini.db",
  "retention_days": 30,
  "admin_enabled": true,
  "admin_token": "",
  "request_timeout_sec": 180,
  "retry_attempts": 3,
  "default_model": "gemini-3.5-flash",
  "per_ip_concurrent": 5,
  "per_ip_rpm": 30,
  "per_ip_rph": 80
}
```

Supported impersonate profiles: `chrome_146` (default) / `chrome_144` / `chrome_133` / `firefox_147` / `safari_16_0` / `safari_ios_17_0`

Environment variables:
- `ADMIN_TOKEN` — admin dashboard login token
- `API_KEY` — locks the `/v1/*` API key (not editable from admin UI)

## Pro models (optional)

Anonymous access works for all models, but `gemini-3.1-pro` falls back to Flash without a cookie. Attach any **free** Google account cookie (no paid subscription needed):

1. Sign in to [gemini.google.com](https://gemini.google.com)
2. DevTools (F12) → Application → Cookies → `https://gemini.google.com`
3. Copy: `SID` / `HSID` / `SSID` / `APISID` / `SAPISID` / `__Secure-1PSID`
4. Save as `cookie.txt`:
```
SID=...; HSID=...; SSID=...; APISID=...; SAPISID=...; __Secure-1PSID=...
```
5. Pass `--cookie-file cookie.txt`

## Proxy pool

**Why**: A single IP gets redirected to `google.com/sorry/index` after roughly **100 cumulative requests**, blocking the next **6-24 hours** of all requests from that IP.

**Solution**: Add multiple proxies in the admin **Proxies** page. Each proxy is an **independent IP slot** with its own concurrency/RPM/RPH allowance. N proxies → N times total capacity.

Supported proxy schemes:
- `http://user:pass@host:port`
- `https://user:pass@host:port`
- `socks5://user:pass@host:port`
- `socks5h://user:pass@host:port` (remote DNS resolution, bypasses local DNS)

**Routing rules**:
- Once any proxy is configured, requests **never fall back to direct connection** (prevents draining the host IP after the proxy pool fills up)
- 5 consecutive failures → automatic circuit break (manual reset in admin UI)
- All proxies full → returns HTTP 429 (no Google quota consumed; the request can be retried later)

## Fingerprint simulation

Direct connections (no proxy) use [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — TLS handshake + HTTP/2 SETTINGS frames + ALPS all match a real Chrome 146. Indistinguishable from a real browser at the network layer.

Proxied connections use stdlib `net/http` + `http.ProxyURL` (best compatibility), but the application-layer headers (`Sec-CH-UA`, `Sec-Fetch-*`, `User-Agent`) are still spoofed to look like Chrome 146.

**Observed nuance**: A naive SDK call (e.g. Python urllib) hitting Google's risk control gets **HTTP 429**; once we look like Chrome, the same trigger sends **HTTP 302** redirecting to `google.com/sorry/index` (Google's CAPTCHA page). Both mean "this IP is flagged" — but the 302 response confirms Google really treats us as a browser.

## Privacy

- **Prompt and response bodies are never persisted** — only metadata: model, proxy ID, latency, token counts, HTTP status, error type
- Legacy `prompt_preview` / `response_preview` columns are auto-dropped when migrating from older versions
- Token counts use [tiktoken-go](https://github.com/pkoukk/tiktoken-go) cl100k_base BPE (works for Chinese + English), not `len/4` estimation
- Gemini's web protocol does not return token usage (verified empirically — no `token`/`count`/`usage` fields exist anywhere in the response), so local estimation is the only option

## OpenAI protocol coverage

| Endpoint | Status | Notes |
|---|---|---|
| `POST /v1/chat/completions` | ✅ | Including `stream: true` SSE |
| `POST /v1/responses` | ✅ | OpenAI Responses API (used by Codex CLI) |
| `GET /v1/models` | ✅ | Lists all 6 models |
| Function calling | ⚠️ | Prompt-level implementation (model emits ` ```tool_call``` ` blocks parsed via regex), not a true protocol layer. Occasional formatting misses |
| Vision / file input | ❌ | Web protocol does not support file upload |
| Audio | ❌ | Web protocol limitation |

## Project layout

```
main.go              Entry point + flag parsing + route registration
config.go            Config loading + DEFAULT_CONFIG
client.go            tls-client (chrome146) + stdlib (proxy) dual client
gemini.go            80-slot payload + StreamGenerate + wrb.fr stream parser
messages.go          OpenAI messages → prompt + tool_call parsing
server.go            /v1/* handlers + rate-limit gate + metrics writes
ratelimit.go         Per-IP-slot concurrency/RPM/RPH limiter
tokenizer.go         tiktoken cl100k_base singleton
apikey.go            API key management (locked / runtime-mutable dual-mode)
db.go                SQLite schema + sessions + requests + kv
proxy.go             Proxy pool CRUD + capacity-aware routing + circuit breaker
scheduler.go         Hourly/daily aggregation + retention sweep
admin.go             /admin/api/* auth + REST endpoints
admin_ui.go          embed admin_ui/ static assets
admin_ui/index.html  Single-page admin (CN UI, Chart.js via CDN)
Dockerfile           multi-stage (alpine builder → distroless runtime)
docker-compose.yml   Single-container, sqlite volume, local 8083 exposure
```

## Limitations

- **Anonymous use**: single IP gets blocked after ~100 cumulative requests for 6-24 hours → use proxy pool to scale
- **Pro routing**: requires a free Google account cookie (no paid subscription needed)
- **Function calling**: prompt-level emulation, the model doesn't always follow the format perfectly
- **Multimodal**: not supported (limitation of the underlying web protocol)
- **Token counts**: tiktoken estimation (Gemini's actual tokenizer is not public), within ±20% of true values

## Acknowledgments

- Community Python single-file reference (also named `gemini-web2api`) — source of the protocol-layer details (80-slot payload, wrb.fr parsing, model ID mapping)
- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — Chrome TLS fingerprinting library
- [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — BPE tokenizer
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — Pure-Go SQLite (CGO-free, builds on alpine)

## License

MIT — see [LICENSE](LICENSE)
