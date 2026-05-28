# syntax=docker/dockerfile:1.7

# ─── Build stage ─────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache deps separately for faster rebuilds
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO disabled — modernc/sqlite is pure Go.
# -ldflags strip symbols to shave ~5MB off binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -trimpath \
    -o /out/gemini-web2api .

# ─── Runtime stage ───────────────────────────────────────────────────────────
# distroless/static: ~2MB base, no shell, no package manager — minimal attack surface.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/gemini-web2api /app/gemini-web2api

# data dir for sqlite + cookies (mount as volume)
USER nonroot:nonroot

EXPOSE 8083

ENV DB_PATH=/data/gemini.db
ENTRYPOINT ["/app/gemini-web2api"]
CMD ["--db", "/data/gemini.db", "--port", "8083"]
