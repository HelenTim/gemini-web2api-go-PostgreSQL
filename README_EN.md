# gemini-web2api-go

<img src="docs/banner.svg" alt="gemini-web2api-go" width="100%">

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8.svg)](https://golang.org)
[![Docker](https://img.shields.io/badge/docker-distroless-blue)](Dockerfile)

[中文文档](README.md) | English

Turn the Google Gemini web app into an OpenAI-compatible API. **Single binary**, **no account needed** (anonymous works), **real Chrome 146 fingerprint**, **SQLite persistence**, ships with an **admin dashboard**.

---

## What this is

It turns a call like this:
```
[OpenAI SDK / Cherry Studio / Cursor / dify / newapi / ...]
    ↓ http://localhost:8083/v1/chat/completions
[gemini-web2api-go]
    ↓ reverse-engineered gemini.google.com web protocol
[Google Gemini web app]
```

This is not a wrapper around Google's official API ([generativelanguage.googleapis.com](https://generativelanguage.googleapis.com)) — it proxies **the browser protocol directly**, so **no Google API key and no paid quota are required**.

## Quick start

### Prebuilt binary (simplest)

Grab the one for your platform from
[Releases](https://github.com/zexadev/gemini-web2api-go/releases). No Go, no Docker —
the single file is the whole thing:

```bash
chmod +x gemini-web2api-go_*
./gemini-web2api-go_* --port 8083 --admin-token your-admin-token
```

Data lands in `./data/gemini.db` by default; pass `--db /your/path.db` to move it.

### Docker (no source needed)

```bash
docker run -d --name gemini-web2api \
  -p 127.0.0.1:8083:8083 \
  -v "$PWD/data:/data" \
  -e ADMIN_TOKEN=your-admin-token \
  ghcr.io/zexadev/gemini-web2api-go:latest
```

For compose, just grab the file — no clone required:

```bash
curl -O https://raw.githubusercontent.com/zexadev/gemini-web2api-go/main/docker-compose.yml
ADMIN_TOKEN=your-admin-token docker compose up -d
```

### From source

If you have Go, skip Docker entirely:

```bash
git clone https://github.com/zexadev/gemini-web2api-go
cd gemini-web2api-go
go build -o gemini-web2api-go .
./gemini-web2api-go --port 8083 --admin-token your-admin-token
```

Changed the code and want it in a container? Uncomment the two `build:` lines in
`docker-compose.yml`, then `docker compose up -d --build`.

The startup banner:

```
gemini-web2api-go v4.0.0
  Listening:   http://0.0.0.0:8083
  Base URL:    http://localhost:8083/v1
  API key:     sk-gemini-XX...XXXX  (mutable in admin UI)
  Admin UI:    http://localhost:8083/admin  (token auth)
  DB:          ./data/gemini.db
  Models:      [gemini-3.5-flash-lite gemini-3.6-flash]
  Cookie:      none (anonymous)
  Proxy:       none
  Impersonate: chrome_146
  Tokenizer:   tiktoken cl100k_base
  Per-IP 限流: 并发=5 / RPM=30 / RPH=80
  Retry:       3x / 2s
```

An API key is generated on first boot. The banner masks it; the full value is on the **Settings** page of the admin panel.

### Making a call

```bash
curl http://localhost:8083/v1/chat/completions \
  -H "Authorization: Bearer sk-gemini-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.6-flash",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

The OpenAI Python SDK works as-is:

```python
from openai import OpenAI
client = OpenAI(
    base_url="http://localhost:8083/v1",
    api_key="sk-gemini-..."  # find it in the admin panel
)
resp = client.chat.completions.create(
    model="gemini-3.6-flash",
    messages=[{"role": "user", "content": "Explain quantum entanglement"}]
)
print(resp.choices[0].message.content)
```

On Windows PowerShell, `curl` is an alias for `Invoke-WebRequest` and will reinterpret the JSON quoting, so use `curl.exe` with `--%`:

```powershell
curl.exe --% http://127.0.0.1:8083/v1/chat/completions -H "Content-Type: application/json" -H "Authorization: Bearer sk-gemini-..." -d "{\"model\":\"gemini-3.6-flash\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello!\"}]}"
```

## Connecting clients

Every OpenAI-compatible client (Cherry Studio / ChatBox / Open WebUI / dify / Cursor / …) is configured the same way:

| Field | Value |
|---|---|
| Base URL | `http://localhost:8083/v1` (some clients want just `http://localhost:8083` and append `/v1` themselves) |
| API key | the `sk-gemini-…` shown on the admin panel's Settings page |
| Model | `gemini-3.6-flash` |

**newapi / one-api channel**: pick the OpenAI type, set the base URL to `http://localhost:8083` (use `http://host.docker.internal:8083` or the container name if newapi runs in Docker too), paste the API key, and list the models as `gemini-3.6-flash,gemini-3.5-flash-lite`.

**Codex CLI** uses `/v1/responses` — point its base URL at `http://localhost:8083/v1`. The endpoint is implemented, but it is not incrementally streamed (see the protocol coverage table).

**Gemini CLI cannot connect.** It expects Google's native `/v1beta/models/{model}:generateContent`; this project only exposes OpenAI-shaped endpoints and does not implement `/v1beta`.

There is also an unauthenticated health check at `GET /` returning `{"status":"ok","version":…,"models":[…]}`.

## Admin panel

`http://localhost:8083/admin`, log in with `--admin-token`.

- **Overview** — 24h KPIs, request volume / P50 latency dual-axis chart, per-model and per-proxy breakdowns, IP rate-limit usage, one-click connectivity check
- **Requests** — request log (metadata only, no prompt/response content), status/model filters, pagination
- **Proxy pool** — runtime CRUD, enable/disable, circuit breaker on repeated failures (each proxy is its own IP slot)
- **Cookie pool** — import multiple signed-in Google accounts; requests rotate through them **least-recently-used first**, falling back to the Settings-page cookie only when the pool is empty. The list shows redacted summaries only (cookie count / key entries / last 4 of SAPISID / failure count)
- **Settings** — runtime config form (saved settings take effect immediately), API key rotation, cookie paste box, read-only view of deploy-time config

The frontend is a single HTML file and Chart.js is embedded in the binary, **not loaded from a CDN**, so the panel also works on air-gapped or intranet deployments.

## Models

Gemini's backend only recognises three models (the list comes from `batchexecute?rpcids=otAQ7b`):

| Model | Description |
|---|---|
| `gemini-3.6-flash` | All-round, default |
| `gemini-3.5-flash-lite` | Fast and lightweight |
| `gemini-3.1-pro` | Most capable, **needs a cookie**; every reply carries a reasoning chain |

Without a cookie, `/v1/models` returns only the first two, and asking for `gemini-3.1-pro` fails with an explanation. An anonymous request for it is always silently downgraded to 3.5 Flash-Lite — better to fail at model selection than to hand back a reply that "succeeded" but isn't Pro.

With a valid cookie it really is Pro: six consecutive calls all had the backend report `3.1 Pro` itself.

Only those three are exposed. The old names `gemini-3.5-flash`, `gemini-3.5-flash-thinking`, `gemini-3.5-flash-thinking-lite`, `gemini-auto` and `gemini-flash-lite` were **removed** (they now return 400): the backend has no entries for them, and keeping them only suggested there were five distinct models to choose from.

> **`@think=N` is deprecated.** The suffix was written into `inner[17]` and long treated as "thinking depth", but captures show it is the **turn index within a conversation** (first turn `[[0]]`, the follow-up carrying a conversation id `[[1]]`, incrementing from there) — nothing to do with reasoning depth. We open a fresh conversation for every request, so the value is always 0 and the parameter never did anything. The suffix is still accepted and ignored, so existing client configs don't break.

### Reasoning chain (`reasoning_content`)

`gemini-3.1-pro` emits its own reasoning before every answer. The project exposes it
as `reasoning_content`, the de-facto standard field, so clients like newapi, Cherry
Studio and ChatBox render it as a collapsible "thinking" panel. The other two models
produce no reasoning chain and the field is then omitted entirely.

Non-streaming:

```json
{"choices":[{"message":{
  "role":"assistant",
  "content":"The core rule of this classic puzzle is...",
  "reasoning_content":"**Defining the Constraints**\n\nI've successfully defined..."
}}],
 "usage":{"prompt_tokens":26,"completion_tokens":337,"reasoning_tokens":77,"total_tokens":363}}
```

Streaming sends it as `delta.reasoning_content`, and **the whole reasoning chain is
streamed before any answer text** — that is the order the upstream itself uses, so the
experience matches the Gemini web UI.

`reasoning_tokens` is counted **separately and is not included in `completion_tokens`**:
clients collapse the reasoning by default, so billing it would charge users for output
they never see (newapi bills on `completion_tokens`). Add the two together yourself if
you do want to bill for it.

### Known capability boundaries

Anonymous calls (no cookie) only reach the two text models above plus Gemini's built-in web search. `gemini-3.1-pro` is silently downgraded to 3.5 Flash-Lite anonymously, which is why it isn't exposed at all in that case.

Image generation, music, video, deep research and canvas need a signed-in session **and are not implemented here** — they require extra tool slots in the request parameter array that this project does not send yet. The panel's "actual model" column always shows which model the backend really used, so any downgrade is visible.

**Multi-turn context is implemented by flattening `messages` into a single prompt — it is not protocol-level multi-turn.**

Gemini's web app does support protocol-level multi-turn (the browser sends only the new message plus the conversation id, and the history stays server-side); anonymous sessions included. We could not reproduce it. Even after matching the browser's exact format — `inner[2] = [cid, previous rid, "", …, token]`, `inner[17] = [[turn]]`, `f.sid` on the URL — the server still refuses. The one thing we could not reproduce is the botguard token in `inner[3]`: across three captured turns it was 1404 / 1847 / 2489 bytes, generated by browser JS at runtime and out of reach for a plain HTTP client.

So flattening is the only workable approach today. The cost is resending the whole history each turn, bounded by the single-request input limit.

## Configuration

There are only two places to configure things, split by whether a change needs a restart.

### Runtime config → admin panel, Settings page

Saving takes effect **immediately**, no restart. Values live in the database and take precedence over `config.json` and command-line flags.

| Item | Notes |
|---|---|
| Default model | used when the client doesn't send `model` |
| Per-slot concurrency / RPM / RPH | rate limits, 0 = unlimited |
| Retry attempts / retry delay / upstream timeout | |
| Detail retention days | only request details expire; aggregates are kept forever |
| TLS fingerprint | `chrome_146` (default) / `chrome_144` / `chrome_133` / `firefox_147` / `safari_16_0` / `safari_ios_17_0` |
| Gemini `bl` version | upstream frontend build id, change it here when it expires |
| Static proxy | fallback when the proxy pool is empty; normally use the Proxy pool page |
| Request logging | |

Every value is range-checked on the server (for example `retry_attempts` accepts 1-10, timeouts 5-600 seconds) and illegal values are rejected with a reason — browser-side limits are trivial to bypass, so the real gate is server-side.

### Credentials → also in the panel

| Item | Notes |
|---|---|
| API key | generated on first boot, rotatable or replaceable from the panel |
| Google cookie | paste it on the Settings page, effective on save. Once set, `gemini-3.1-pro` appears in the model list |

Both live in the database. Saved values are never echoed back (the cookie only reports how many cookies were recognised and whether the key ones are present).

### Deploy-time config → `docker-compose.yml`

What's left is only what requires restarting the process:

| Item | Where |
|---|---|
| Listening port | `ports` + `command: --port` |
| Database path | `volumes` + `command: --db` |
| `ADMIN_TOKEN` | `environment`, the panel login token |

There are two optional **lock switches** for deployments that must not be changed at runtime: the `API_KEY` environment variable pins the API key (the panel can't change it), and the file behind `--cookie-file` acts as a fallback when no cookie is stored in the panel. Without them, both default to the panel.

Command-line flags still work and are meant as temporary overrides during local debugging. Precedence: **panel changes > CLI flags / `config.json` > built-in defaults**.

When running the binary directly, copy `config.example.json` to `config.json` as a starting template. Without `--config` the program looks for `./config.json` first, then `$HOME/.config/gemini-web2api/config.json`. Tunables in that file only seed the first boot; once changed in the panel, the panel wins.

All flags:

| Flag | Notes |
|---|---|
| `--port` | listening port, default 8083 |
| `--config` | path to `config.json` |
| `--db` | SQLite path, default `./data/gemini.db` |
| `--admin-token` | panel login token; empty = no auth (only acceptable when bound to 127.0.0.1) |
| `--api-key` | pins the `/v1/*` key so the panel can't change it, same as the `API_KEY` env var |
| `--cookie-file` | fallback cookie file used when the panel has none |
| `--proxy` | static proxy used when the proxy pool is empty |
| `--impersonate` | TLS fingerprint profile |
| `--version` | print version and exit (this is what the Docker healthcheck runs) |

## Cookie (optional)

Attaching a Google account cookie makes requests run as a signed-in session. What it buys you is **`gemini-3.1-pro` plus its reasoning chain** (see the reasoning section above). Verified on a free account: six consecutive calls all reported `3.1 Pro`.

> Signed-in requests must carry an extra XSRF token. The project fetches it from the Gemini page automatically, caches it per cookie and re-fetches on expiry — nothing to configure. (Missing it makes **every** request fail with 400 while anonymous traffic keeps working — an earlier version hit exactly that.)

A signed-in session unlocks image generation (Nano Banana 2), music (Lyria 3), video, deep research, canvas and extended thinking in the web app, but **none of that is implemented here yet**; today a cookie only changes which session the requests belong to.

1. Sign in to [gemini.google.com](https://gemini.google.com)
2. DevTools (F12) → Application → Cookies → `https://gemini.google.com`
3. Copy: `SID` / `HSID` / `SSID` / `APISID` / `SAPISID` / `__Secure-1PSID`
4. Paste it into the panel under Settings → Google Cookie (effective on save), or write it to `cookie.txt` and start with `--cookie-file cookie.txt` (the panel wins if both are set):
```
SID=...; HSID=...; SSID=...; APISID=...; SAPISID=...; __Secure-1PSID=...
```

The JSON form `{"cookie": "SID=...; ...", "sapisid": "..."}` is also accepted. Requests carrying `SAPISID` get a computed `SAPISIDHASH` authorization header, so that entry cannot be missing.

**For multiple accounts use the Cookie pool page.** Each request picks an enabled account least-recently-used first and advances the rotation; the single cookie above is used only when the pool is empty. Either path makes `gemini-3.1-pro` appear in the model list.

An account's "failure count" and "last success" columns update automatically. Only **401/403 counts as the cookie's fault** — network errors, proxy failures and Google blocks (302) are excluded, because residential exits degrade often enough that counting them would turn the failure count into proxy noise and make healthy cookies look like the worst offenders.

Note that "last success" only means a request involving this cookie succeeded; it does **not** prove the cookie is still valid. An expired cookie doesn't error — Gemini just treats you as anonymous. To check validity, send one `gemini-3.1-pro` request: a valid cookie reports `3.1 Pro` and returns a reasoning chain, an expired one is downgraded to 3.5 Flash-Lite.

## Proxy pool (the core of running this for free)

**Why you need it**: a single IP eventually gets redirected to `google.com/sorry/index`. That threshold is **80-180 requests**, and the wide range is driven by **connection strategy and exit quality, not by how fast you send**:

| Connection strategy | Concurrency | Pacing | Successful requests when blocked |
|---|---|---|---|
| Reused connection pool | 10 | no delay | 151 / 172 / 177 |
| Reused connection pool | 3 | no delay | 103 / 111 |
| Fresh connection each time | 10 | no delay | 106 / 109 |
| Fresh connection each time | 1 | 24s apart | 81 / 166 |

Only a 302 to `/sorry/` counts as blocked, and every exit was pre-screened. The one clean single-variable comparison is connection strategy: concurrency pinned at 10, both arms started together, each run lasting only 80 seconds (too short for exits to drift) — reused connections reached 172/177, fresh connections 106/109. **Keeping connections alive buys roughly 60% more requests per exit.**

**Slowing down does not help.** The burst arm spanned 103-177 and the slow arm 81-166 — almost completely overlapping, and the slow arm's own two samples differ by a factor of two. The cause is exits degrading over long runs, not pacing.

**Proxy failures consume the budget early**: the dirtier the path, the sooner the block, because some of those "failed" requests did reach Google and were counted (the slow arm actually sent 195 requests to get 166 successes, 17% more than the success count suggests). Don't expect to squeeze out capacity by retrying failures.

**Once blocked, the block is hard.** Two independent re-probes of 30 requests each, 20s apart, spanning about 10 minutes: **60 requests, zero successes**. Recovery time is untested; all we can say is that nothing slipped through within 20 minutes.

Control group: 10 IPs × 50 requests each (418 requests) produced zero rejections from Google. The default `per_ip_rph=80` sits at the low end of the measured range and needs no adjustment.

**The fix**: add several proxies on the Proxy pool page. **Each proxy is an independent IP slot** with its own concurrency/RPM/RPH allowance, so N proxies means N times the total capacity.

Supported proxy schemes:
- `http://user:pass@host:port`
- `https://user:pass@host:port`
- `socks5://user:pass@host:port`
- `socks5h://user:pass@host:port` (remote DNS resolution, avoids local DNS poisoning)

**Scheduling rules**:
- Once any proxy is configured, requests **never fall back to a direct connection** (so a full pool can't end up burning the host IP)
- 5 failures trip the circuit breaker (resettable from the panel)
- All proxies full → HTTP 429 (no Google quota consumed; retry once a slot frees up)

**Environment variables `HTTPS_PROXY` / `ALL_PROXY` are ignored.** Proxies come only from the pool or the static proxy setting (`--proxy` / `config.json`). Otherwise a stray `export` on the host would silently change the exit IP while the panel still showed a direct connection, which makes troubleshooting misleading.

## Fingerprint simulation

Direct connections (no proxy) go through [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client): the TLS handshake, HTTP/2 SETTINGS frames and ALPS all match a real Chrome 146. From Google's risk-control point of view this is indistinguishable from a real browser.

Proxied connections switch to stdlib `net/http` with `http.ProxyURL` (best compatibility), but the application-layer headers (`Sec-CH-UA`, `Sec-Fetch-*`, `User-Agent`) are still spoofed with Chrome 146's real values.

**Note that through a proxy the TLS fingerprint is still ours, not the exit node's.** HTTP CONNECT only builds a tunnel; the TLS handshake is end-to-end between us and Google. JA3 echo tests confirm it: through the same proxy, stdlib and tls-client produce different JA3s (so the proxy isn't rewriting anything), while stdlib direct and stdlib through a proxy produce identical JA3s (so the proxy layer doesn't touch TLS). **So once a proxy is configured, what Google sees is the Go standard library's fingerprint, not Chrome 146's.**

This does not change the blocking threshold, though: in a same-start comparison, tls-client Chrome_146 reached 111 requests and Go stdlib 103 — a gap of 8, while the variance between exits under identical settings reaches 36% (151 vs 111). Making the proxy path use the Chrome fingerprint needs a different justification (long-term account profiling, say); it won't buy throughput.

**An observation from testing**: a naive SDK call (e.g. Python urllib) that trips risk control gets **HTTP 429**; once disguised as Chrome, tripping it yields **HTTP 302** to `google.com/sorry/index` (the CAPTCHA page). Both are IP blocks, but the 302 confirms Google really does take us for a browser.

## Privacy

- **Prompt and response bodies are never stored** — only metadata: model, proxy id, latency, token counts, status code, error type
- Legacy `prompt_preview` / `response_preview` columns are dropped automatically when migrating from older versions
- Token counts are computed with the [tiktoken-go](https://github.com/pkoukk/tiktoken-go) cl100k_base BPE tokenizer (accurate for both English and Chinese), not a `len/4` estimate
- Gemini's web app does not return real token counts (no token/usage field appears anywhere in the response), so local estimation is the only option

## OpenAI protocol coverage

| Endpoint | Status | Notes |
|---|---|---|
| `POST /v1/chat/completions` | ✅ | Real streaming: every upstream frame is forwarded as a delta (a 400-character Chinese answer produced 40 chunks). Chunk sequence is `delta{role}` → `delta{content}`×N → empty `delta` + `finish_reason` → `[DONE]`. **Degrades to buffered when `tools` are present** — a tool_call block needs the complete text to parse |
| `POST /v1/responses` | ✅ | OpenAI Responses API (used by Codex CLI). **Not incrementally streamed** — still buffered, then emitted as the event sequence |
| `GET /v1/models` | ✅ | 2 models anonymously, the third appears once a cookie is set |
| `GET /` | ✅ | Health check, unauthenticated, returns status/version/models |
| `/v1beta/models/…` (Gemini CLI native) | ❌ | Not implemented, only OpenAI-shaped endpoints are exposed |
| `/v1/embeddings`, `/v1/images/*`, `/v1/audio/*` | ❌ | Not implemented, return 404 |
| Function calling | ⚠️ | Prompt-level implementation (the model emits a ` ```tool_call``` ` block that we parse with a regex), not a real protocol layer. **Reliable for looking up private data or internal systems**, but anything Gemini can answer itself (the weather) gets answered directly, and side-effecting actions (sending email) are refused |
| `tool_choice` | ⚠️ | `none` injects no tool definitions at all; `required` and a named function add mandatory wording and drop the other tools from the prompt. A prompt-level layer **cannot truly force it** — in testing, questions the model can answer itself (weather, 2+2) were answered directly even under `required` |
| `stream_options.include_usage` | ✅ | Emits a usage chunk with an empty `choices` array after `finish_reason` |
| `usage` token counts | ✅ | tiktoken cl100k_base, the same basis as the panel's requests table |
| `n` > 1 | ❌ | Returns 400. Upstream yields a single candidate; silently treating it as 1 would short-change the client |
| Sampling params | ➖ | `temperature` / `top_p` / `max_tokens` / `stop` / `seed` / `presence_penalty` / `frequency_penalty` are **accepted and ignored, not rejected**. Gemini's web protocol has no such knobs |
| `response_format` / `logprobs` | ➖ | Not implemented, accepted and ignored |
| Vision / image input | ❌ | Sending `image_url` returns 400. Anonymous upload **does** work (`content-push.googleapis.com/upload/`, two-step resumable, returns `/contrib_service/ttl_1d/…`), but referencing the uploaded file in a conversation is refused upstream (`BardErrorInfo 1100`); a signed-in session is required |
| Audio | ❌ | Sending `input_audio` returns 400. The web app has music generation (Lyria 3), signed-in only |

## Project layout

```
main.go              entry point + flag parsing + route registration
config.go            config loading + DEFAULT_CONFIG
client.go            tls-client (chrome146) + stdlib (proxied) dual client
gemini.go            model table + 80-slot payload + model header + StreamGenerate + wrb.fr stream parsing
messages.go          OpenAI messages → prompt + tool_call parsing
server.go            /v1/* + rate-limit gate + request validation + metrics writes
sse.go               SSE writer for chat.completion.chunk (lazy headers)
ratelimit.go         per-IP-slot concurrency/RPM/RPH limiter
tokenizer.go         tiktoken cl100k_base singleton
apikey.go            API key management (locked / runtime-mutable)
db.go                SQLite schema + sessions + requests + accounts + kv
proxy.go             proxy pool CRUD + capacity-aware scheduling + circuit breaker
cookie_pool.go       cookie pool data layer (accounts CRUD + least-recently-used pick)
scheduler.go         hourly/daily aggregation + retention
admin.go             /admin/api/* auth + REST endpoints
admin_cookies.go     cookie pool REST endpoints (redacted before returning)
admin_ui.go          embeds admin_ui/ static files
admin_ui/index.html  single-page admin (Chinese UI)
admin_ui/chart.umd.min.js  Chart.js embedded in the binary, no CDN
Dockerfile           multi-stage (alpine builder → distroless runtime)
docker-compose.yml   single container, sqlite volume, exposed locally on 8083
```

## Limitations

- **Per-IP ceiling**: measured at **80-180 requests** before the sorry-page redirect. The range is that wide because it is driven by **connection strategy and exit quality**, not by how fast you send: at the same concurrency of 10, a reused connection pool reached 172/177 while a fresh connection per request only reached 106/109. `per_ip_rph=80` sits at the bottom of that range → use the proxy pool to scale
- **Signed-in features**: image generation, music, video, deep research and canvas are not implemented (the protocol is verified to work; what's missing is the request parameter slots)
- **Function calling**: prompt-level, the model doesn't always answer in the expected format (a real protocol layer isn't available to us)
- **Multimodal**: unsupported. The web protocol does support image/file upload, image, music and video generation, but all require a signed-in session and none is implemented here
- **Token counts**: tiktoken estimates (Gemini's real tokenizer is not public), within about ±20% of the true value
- **Cookie pool never auto-removes a bad account**: outcomes are written back (only 401/403 count as the cookie's fault — network errors and 302 blocks don't), but failures never trigger an automatic disable, so you have to do it from the panel. Also `last_ok_at` only means "a request using this cookie succeeded", not that the cookie is still valid — an expired cookie doesn't error, Gemini just treats you as anonymous and plain text requests still return 200
- **Streaming is only half real**: `/v1/responses` and chat requests carrying `tools` are buffered; only plain chat streams incrementally

## Troubleshooting

| Symptom | Most likely cause | What to do |
|---|---|---|
| We return **429** | Every IP slot is at its concurrency/RPM/RPH limit. **This is not Google refusing** and no upstream quota was consumed | Add proxies, or raise the limits on the Settings page |
| Panel diagnostics show **302 → `google.com/sorry/index`** | This exit IP is blocked by Google (after 80-180 requests, depending on connection strategy and exit quality) | Change exit / add proxies. **The block is hard, not probabilistic** (60 probes after a block, zero successes), so retrying in place is pointless |
| Occasional empty responses, logged as an upstream refusal | Upstream transient refusal (`1155`). There is no predictable threshold — it correlates with neither rate, concurrency, nor cumulative count | Resend once and it usually works. **Lowering RPM does not help**: measured, it is unrelated to request rate |
| Every request times out | This host can't reach `gemini.google.com` | Configure a proxy (panel or `--proxy`). Note that **`HTTPS_PROXY` is not read** |
| `gemini-3.1-pro` errors out immediately | It isn't exposed without a cookie, by design | Attach a cookie (Settings page or Cookie pool) and it becomes available |
| Every request returns 502 after attaching a cookie | The cookie expired, so the XSRF token can't be fetched | Re-export the cookie. Quick check: if `gemini-3.1-pro` reports 3.5 Flash-Lite, it's expired |
| Panel won't open / 401 | `--admin-token` (or `ADMIN_TOKEN`) doesn't match | An empty token disables auth, which is only acceptable when bound to 127.0.0.1 |

With Docker's default bridge network, upstream may return empty content (Google rejects certain NAT ranges). That is reported in the Python reference implementation's README; we have **never reproduced it here**. If you hit it, try `network_mode: host` to confirm whether that is the cause.

## Acknowledgments

- [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client) — real Chrome TLS fingerprints
- [pkoukk/tiktoken-go](https://github.com/pkoukk/tiktoken-go) — BPE tokenizer
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — pure-Go SQLite (CGO-free, builds straight on alpine)

## License

MIT — see [LICENSE](LICENSE)

## Links

- [Sophomoresty/gemini-web2api](https://github.com/Sophomoresty/gemini-web2api) — a Python implementation of the same idea; its approach informed our early work on the Gemini web protocol
- [LINUX DO](https://linux.do) — the community where this project is shared

[![LinuxDo](https://img.shields.io/badge/%E7%A4%BE%E5%8C%BA-LinuxDo-blue?style=for-the-badge)](https://linux.do/)

