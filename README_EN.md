# gemini-web2api-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8.svg)](https://golang.org)
[![Docker](https://img.shields.io/badge/docker-distroless-blue)](Dockerfile)

[中文文档](README.md) | English

Convert Google Gemini's web interface into an OpenAI-compatible API. **Single binary**, **anonymous access works** **Chrome 146 TLS fingerprint**, **SQLite persistence**, ships with a built-in **admin dashboard**.

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
    "model": "gemini-3.6-flash",
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
    model="gemini-3.6-flash",
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

Gemini's backend only recognises three models (list comes from
`batchexecute?rpcids=otAQ7b`):

| Model | Description |
|---|---|
| `gemini-3.6-flash` | All-around, default |
| `gemini-3.5-flash-lite` | Fastest, lightweight |
| `gemini-3.1-pro` | Not reachable — silently downgraded, see below |

These three are the only ones exposed. The legacy names `gemini-3.5-flash`,
`gemini-3.5-flash-thinking`, `gemini-3.5-flash-thinking-lite`, `gemini-auto` and
`gemini-flash-lite` have been **removed** (they now return 400) — the backend has
no entries for them, and keeping them only suggested five distinct models exist.

> **`@think=N` is deprecated.** The suffix went into `inner[17]` and was treated
> as "thinking depth", but packet captures show it is the **turn index within a
> conversation** (first turn `[[0]]`, the follow-up carrying a conversation id
> `[[1]]`, incrementing from there) — nothing to do with reasoning depth. We open
> a fresh conversation for every request, so the value is always 0 and the
> parameter never did anything. The suffix is still accepted but ignored.

### Known capability boundaries

Anonymous calls (no cookie) only reach the two text models above plus Gemini's
built-in web search. Image generation, music, video, deep research, canvas and
extended thinking all require a signed-in session; anonymous requests are either
rejected or silently downgraded to plain text. `gemini-3.1-pro` is never reachable: **anonymously it is downgraded to 3.5
Flash-Lite, and with a free account's cookie to "3.6 Flash 扩展"**; paid
subscriptions untested. The admin panel's "actual model" column flags such downgrades.

**Multi-turn context works by flattening `messages` into a single prompt — it is
not protocol-level multi-turn.**

Gemini's web UI does support protocol-level multi-turn (the browser sends only the
new message plus conversation ids; history stays server-side), anonymous sessions
included. We could not reproduce it: even after matching the browser byte for byte
— `inner[2] = [cid, previous rid, "", …, token]`, `inner[17] = [[turn]]`, `f.sid`
on the URL — the backend still refuses. The one piece we cannot reproduce is the
botguard token in `inner[3]` (1404 / 1847 / 2489 bytes across three turns in the
capture), generated by browser JS at runtime and out of reach for a plain HTTP
client.

Flattening is therefore the only workable approach today. The cost is resending
the whole history each turn, bounded by the single-request input limit.

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
  "default_model": "gemini-3.6-flash",
  "per_ip_concurrent": 5,
  "per_ip_rpm": 30,
  "per_ip_rph": 80
}
```

Supported impersonate profiles: `chrome_146` (default) / `chrome_144` / `chrome_133` / `firefox_147` / `safari_16_0` / `safari_ios_17_0`

Environment variables:
- `ADMIN_TOKEN` — admin dashboard login token
- `API_KEY` — locks the `/v1/*` API key (not editable from admin UI)

## Cookie (optional)

Attaching a Google account cookie makes requests run as a signed-in session.
**It does not unlock `gemini-3.1-pro`** — with a free account the Pro model id is
still downgraded and the backend reports 3.6 Flash. Paid subscriptions untested.

A signed-in session unlocks image generation (Nano Banana 2), music (Lyria 3),
video, deep research, canvas and extended thinking on the web UI, but **none of
that is implemented here yet**; today the cookie only changes rate-limit attribution.

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
| `POST /v1/chat/completions` | ✅ | Real streaming — each upstream frame is diffed and forwarded as it arrives (400-char Chinese answer produced 40 chunks). Sequence: `delta{role}` → `delta{content}`×N → empty `delta` + `finish_reason` → `[DONE]`. **Falls back to buffered when `tools` are present** — tool_call blocks need the complete text to parse |
| `POST /v1/responses` | ✅ | OpenAI Responses API (used by Codex CLI). **Not incrementally streamed** — still buffered, then emitted as the event sequence |
| `GET /v1/models` | ✅ | 3 models |
| Function calling | ⚠️ | Prompt-level implementation (model emits ` ```tool_call``` ` blocks parsed via regex), not a true protocol layer. **Reliable for private-data / internal-system lookups**, but anything Gemini can answer itself (weather) gets answered directly, and side-effecting actions (sending email) are refused |
| `tool_choice` | ⚠️ | `none` skips tool definitions entirely; `required` and a named function add a mandatory instruction and drop the other tools from the prompt. A prompt-level layer **cannot truly force it** — in testing, questions the model can answer itself (weather, 2+2) were answered directly even under `required` |
| `stream_options.include_usage` | ✅ | Emits a usage chunk with an empty `choices` array after `finish_reason` |
| `usage` token counts | ✅ | tiktoken cl100k_base, same basis as the admin requests table |
| `n` > 1 | ❌ | Returns 400. Upstream yields a single candidate; silently treating it as 1 would short-change the client |
| Sampling params | ➖ | `temperature` / `top_p` / `max_tokens` / `stop` / `seed` / penalties are **accepted and ignored, not rejected**. Gemini's web protocol has no such knobs |
| `response_format` / `logprobs` | ➖ | Not implemented, accepted and ignored |
| Vision / file input | ❌ | Sending `image_url` returns 400. Anonymous upload **succeeds** (`content-push.googleapis.com/upload/`, two-step resumable, returns `/contrib_service/ttl_1d/…`) but referencing the file is refused upstream (`BardErrorInfo 1100`); needs a signed-in session |
| Audio | ❌ | Sending `input_audio` returns 400. The web UI has music generation (Lyria 3), signed-in only |

## Project layout

```
main.go              Entry point + flag parsing + route registration
config.go            Config loading + DEFAULT_CONFIG
client.go            tls-client (chrome146) + stdlib (proxy) dual client
gemini.go            model table + 80-slot payload + model header + StreamGenerate + wrb.fr stream parser
messages.go          OpenAI messages → prompt + tool_call parsing
server.go            /v1/* handlers + rate-limit gate + request validation + metrics writes
sse.go               chat.completion.chunk SSE writer (lazy header)
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
- **Pro routing**: not reachable. Free accounts get downgraded to 3.6 Flash even when signed in; paid subscriptions untested
- **Function calling**: prompt-level emulation, the model doesn't always follow the format perfectly
- **Multimodal**: not supported here. The web protocol does have image/file upload, image, music and video generation, but all require a signed-in session and none is implemented yet
- **Token counts**: tiktoken estimation (Gemini's actual tokenizer is not public), within ±20% of true values

## Acknowledgments

- Community Python single-file reference (also named `gemini-web2api`) — source of the protocol-layer details (80-slot payload, wrb.fr parsing, model ID mapping)
- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — Chrome TLS fingerprinting library
- [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — BPE tokenizer
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — Pure-Go SQLite (CGO-free, builds on alpine)

## License

MIT — see [LICENSE](LICENSE)
