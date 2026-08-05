package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	db     *sql.DB
	dbOnce sync.Once
)

const schema = `
CREATE TABLE IF NOT EXISTS proxies (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    weight       INTEGER NOT NULL DEFAULT 1,
    fail_count   INTEGER NOT NULL DEFAULT 0,
    last_used    INTEGER,
    last_error   TEXT,
    created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS requests (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              INTEGER NOT NULL,
    model           TEXT NOT NULL,
    upstream_model  TEXT,
    proxy_id        INTEGER,
    proxy_name      TEXT,
    status          INTEGER NOT NULL,
    error           TEXT,
    ttfb_ms         INTEGER,
    total_ms        INTEGER NOT NULL,
    prompt_chars    INTEGER NOT NULL,
    response_chars  INTEGER NOT NULL,
    prompt_tokens   INTEGER NOT NULL,
    output_tokens   INTEGER NOT NULL,
    endpoint        TEXT,
    stream          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_requests_ts ON requests(ts);
CREATE INDEX IF NOT EXISTS idx_requests_proxy ON requests(proxy_id);
CREATE INDEX IF NOT EXISTS idx_requests_model ON requests(model);

-- Hourly aggregate (永久保留，明细只留 30 天)
CREATE TABLE IF NOT EXISTS stats_hourly (
    bucket          INTEGER NOT NULL,    -- unix ts of hour start
    model           TEXT NOT NULL,
    proxy_id        INTEGER NOT NULL,    -- 0 = no proxy
    requests        INTEGER NOT NULL DEFAULT 0,
    successes       INTEGER NOT NULL DEFAULT 0,
    failures        INTEGER NOT NULL DEFAULT 0,
    total_ms        INTEGER NOT NULL DEFAULT 0,
    p50_ms          INTEGER NOT NULL DEFAULT 0,
    p95_ms          INTEGER NOT NULL DEFAULT 0,
    prompt_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, model, proxy_id)
);
CREATE INDEX IF NOT EXISTS idx_hourly_bucket ON stats_hourly(bucket);

-- Daily aggregate (永久保留)
CREATE TABLE IF NOT EXISTS stats_daily (
    bucket          INTEGER NOT NULL,
    model           TEXT NOT NULL,
    proxy_id        INTEGER NOT NULL,
    requests        INTEGER NOT NULL DEFAULT 0,
    successes       INTEGER NOT NULL DEFAULT 0,
    failures        INTEGER NOT NULL DEFAULT 0,
    total_ms        INTEGER NOT NULL DEFAULT 0,
    p50_ms          INTEGER NOT NULL DEFAULT 0,
    p95_ms          INTEGER NOT NULL DEFAULT 0,
    prompt_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, model, proxy_id)
);
CREATE INDEX IF NOT EXISTS idx_daily_bucket ON stats_daily(bucket);

CREATE TABLE IF NOT EXISTS sessions (
    token        TEXT PRIMARY KEY,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS kv (
    k TEXT PRIMARY KEY,
    v TEXT
);

-- Cookie 池：每行一个 Google 登录态账号（一整串 gemini.google.com cookie）。
-- 请求时按 last_used_at 最久优先挑一个 enabled 的，天然轮转 + 分散单 IP 上限。
CREATE TABLE IF NOT EXISTS accounts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    label         TEXT NOT NULL DEFAULT '',      -- 用户可命名（一般填邮箱）
    cookie        TEXT NOT NULL,                 -- 完整 cookie 串 "k=v; k=v"
    status        TEXT NOT NULL DEFAULT 'enabled', -- enabled | disabled
    note          TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER NOT NULL DEFAULT 0,     -- 上次被挑中发请求的时刻
    last_ok_at    INTEGER NOT NULL DEFAULT 0,     -- 上次请求成功的时刻
    last_error    TEXT NOT NULL DEFAULT '',
    fail_count    INTEGER NOT NULL DEFAULT 0      -- 连续失败次数（成功归零）
);
CREATE INDEX IF NOT EXISTS idx_accounts_pick ON accounts(status, last_used_at);
`

func getDB() *sql.DB {
	dbOnce.Do(func() {
		path := cfg.DBPath
		if path == "" {
			path = "./data/gemini.db"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "[db] mkdir failed: %v\n", err)
			os.Exit(1)
		}
		dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
		conn, err := sql.Open("sqlite", dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[db] open failed: %v\n", err)
			os.Exit(1)
		}
		conn.SetMaxOpenConns(8)
		conn.SetMaxIdleConns(4)
		conn.SetConnMaxLifetime(0)
		if _, err := conn.Exec(schema); err != nil {
			fmt.Fprintf(os.Stderr, "[db] schema init failed: %v\n", err)
			os.Exit(1)
		}
		// Migration: drop legacy prompt_preview / response_preview columns
		// from older deployments. SQLite 3.35+ supports DROP COLUMN.
		// Errors are ignored — columns may already be absent.
		// 老库补上服务端自报的模型名列（列已存在时报错，忽略）
		_, _ = conn.Exec(`ALTER TABLE requests ADD COLUMN upstream_model TEXT`)
		_, _ = conn.Exec(`ALTER TABLE requests DROP COLUMN prompt_preview`)
		_, _ = conn.Exec(`ALTER TABLE requests DROP COLUMN response_preview`)
		db = conn
		logf("[db] opened %s", path)
	})
	return db
}

// Request rows ───────────────────────────────────────────────────────────────

type RequestRow struct {
	ID            int64  `json:"id"`
	TS            int64  `json:"ts"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
	ProxyID       *int64 `json:"proxy_id"`
	ProxyName     string `json:"proxy_name"`
	Status        int    `json:"status"`
	Error         string `json:"error"`
	TTFBMs        *int64 `json:"ttfb_ms"`
	TotalMs       int64  `json:"total_ms"`
	PromptChars   int    `json:"prompt_chars"`
	ResponseChars int    `json:"response_chars"`
	PromptTokens  int    `json:"prompt_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	Endpoint      string `json:"endpoint"`
	Stream        int    `json:"stream"`
}

func insertRequest(r *RequestRow) {
	_, err := getDB().Exec(`INSERT INTO requests
        (ts, model, upstream_model, proxy_id, proxy_name, status, error, ttfb_ms, total_ms,
         prompt_chars, response_chars, prompt_tokens, output_tokens,
         endpoint, stream)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.TS, r.Model, r.UpstreamModel, r.ProxyID, r.ProxyName, r.Status, r.Error, r.TTFBMs, r.TotalMs,
		r.PromptChars, r.ResponseChars, r.PromptTokens, r.OutputTokens,
		r.Endpoint, r.Stream)
	if err != nil {
		logf("[db] insert request failed: %v", err)
	}
}

// Sessions ───────────────────────────────────────────────────────────────────

func createSession(token string, ttl time.Duration) {
	now := time.Now().Unix()
	_, err := getDB().Exec(`INSERT OR REPLACE INTO sessions(token, created_at, expires_at) VALUES (?,?,?)`,
		token, now, now+int64(ttl.Seconds()))
	if err != nil {
		logf("[db] session insert failed: %v", err)
	}
}

func validSession(token string) bool {
	if token == "" {
		return false
	}
	var exp int64
	err := getDB().QueryRow(`SELECT expires_at FROM sessions WHERE token=?`, token).Scan(&exp)
	if err != nil {
		return false
	}
	return time.Now().Unix() < exp
}

// KV helpers ─────────────────────────────────────────────────────────────────

func kvGet(k string) string {
	var v string
	if err := getDB().QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v); err != nil {
		return ""
	}
	return v
}

func kvSet(k, v string) error {
	_, err := getDB().Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}
