package app

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	db     *sql.DB
	dbOnce sync.Once
)

const schema = `
CREATE TABLE IF NOT EXISTS proxies (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    weight       INTEGER NOT NULL DEFAULT 1,
    fail_count   INTEGER NOT NULL DEFAULT 0,
    last_used    BIGINT,
    last_error   TEXT,
    created_at   BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS requests (
    id              BIGSERIAL PRIMARY KEY,
    ts              BIGINT NOT NULL,
    model           TEXT NOT NULL,
    upstream_model  TEXT,
    proxy_id        BIGINT,
    proxy_name      TEXT,
    account_id      BIGINT,
    account_label   TEXT,
    status          INTEGER NOT NULL,
    error           TEXT,
    ttfb_ms         BIGINT,
    total_ms        BIGINT NOT NULL,
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
    bucket          BIGINT NOT NULL,    -- unix ts of hour start
    model           TEXT NOT NULL,
    proxy_id        BIGINT NOT NULL,    -- 0 = no proxy
    requests        INTEGER NOT NULL DEFAULT 0,
    successes       INTEGER NOT NULL DEFAULT 0,
    failures        INTEGER NOT NULL DEFAULT 0,
    total_ms        BIGINT NOT NULL DEFAULT 0,
    p50_ms          INTEGER NOT NULL DEFAULT 0,
    p95_ms          INTEGER NOT NULL DEFAULT 0,
    prompt_tokens   BIGINT NOT NULL DEFAULT 0,
    output_tokens   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, model, proxy_id)
);
CREATE INDEX IF NOT EXISTS idx_hourly_bucket ON stats_hourly(bucket);

-- Daily aggregate (永久保留)
CREATE TABLE IF NOT EXISTS stats_daily (
    bucket          BIGINT NOT NULL,
    model           TEXT NOT NULL,
    proxy_id        BIGINT NOT NULL,
    requests        INTEGER NOT NULL DEFAULT 0,
    successes       INTEGER NOT NULL DEFAULT 0,
    failures        INTEGER NOT NULL DEFAULT 0,
    total_ms        BIGINT NOT NULL DEFAULT 0,
    p50_ms          INTEGER NOT NULL DEFAULT 0,
    p95_ms          INTEGER NOT NULL DEFAULT 0,
    prompt_tokens   BIGINT NOT NULL DEFAULT 0,
    output_tokens   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (bucket, model, proxy_id)
);
CREATE INDEX IF NOT EXISTS idx_daily_bucket ON stats_daily(bucket);

CREATE TABLE IF NOT EXISTS sessions (
    token        TEXT PRIMARY KEY,
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS kv (
    k TEXT PRIMARY KEY,
    v TEXT
);

-- Cookie 池：每行一个 Google 登录态账号（一整串 gemini.google.com cookie）。
-- 请求时按 last_used_at 最久优先挑一个 enabled 的，天然轮转 + 分散单 IP 上限。
CREATE TABLE IF NOT EXISTS accounts (
    id            BIGSERIAL PRIMARY KEY,
    label         TEXT NOT NULL DEFAULT '',      -- 用户可命名（一般填邮箱）
    cookie        TEXT NOT NULL,                 -- 完整 cookie 串 "k=v; k=v"
    status        TEXT NOT NULL DEFAULT 'enabled', -- enabled | disabled
    note          TEXT NOT NULL DEFAULT '',
    created_at    BIGINT NOT NULL,
    last_used_at  BIGINT NOT NULL DEFAULT 0,     -- 上次被挑中发请求的时刻
    last_ok_at    BIGINT NOT NULL DEFAULT 0,     -- 上次请求成功的时刻
    last_error    TEXT NOT NULL DEFAULT '',
    fail_count    INTEGER NOT NULL DEFAULT 0,    -- 连续失败次数（成功归零）
    proxy_id      BIGINT NOT NULL DEFAULT 0      -- 绑定的出口，0 = 还没绑
);
CREATE INDEX IF NOT EXISTS idx_accounts_pick ON accounts(status, last_used_at);
`

// databaseDSN 返回最终要连的 PostgreSQL 连接串：database_url 配置 > DATABASE_URL
// 环境变量 > db_path（兼容旧配置，现在直接填 DSN）。
func databaseDSN() string {
	if cfg.DatabaseURL != "" {
		return cfg.DatabaseURL
	}
	if d := os.Getenv("DATABASE_URL"); d != "" {
		return d
	}
	return cfg.DBPath
}

func getDB() *sql.DB {
	dbOnce.Do(func() {
		dsn := databaseDSN()
		if dsn == "" {
			fmt.Fprintln(os.Stderr, "[db] 未配置数据库：请设置 DATABASE_URL（或 database_url / --db）"+
				"指向 PostgreSQL，例如 postgres://user:pass@host:5432/db")
			os.Exit(1)
		}
		conn, err := sql.Open("pgx", dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[db] open failed: %v\n", err)
			os.Exit(1)
		}
		conn.SetMaxOpenConns(8)
		conn.SetMaxIdleConns(4)
		conn.SetConnMaxLifetime(0)
		// 启动即建表（幂等）。托管 PostgreSQL（Neon / Render / Supabase）的连接串
		// 一般都带 sslmode=require，直接用即可。
		if err := execSchema(conn); err != nil {
			fmt.Fprintf(os.Stderr, "[db] 初始化失败: %v\n", err)
			os.Exit(1)
		}
		db = conn
		logf("[db] connected to PostgreSQL")
	})
	return db
}

// execSchema 逐条执行建表语句。pgx 的扩展协议（默认）不支持一次 Exec 塞多条语句，
// 所以按分号拆开逐条跑 —— 每条都是幂等的 CREATE ... IF NOT EXISTS。
func execSchema(conn *sql.DB) error {
	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := conn.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// int64Ptr 把可空的 *int64 转成 database/sql 能直接吃的值（nil 即 NULL）。
func int64Ptr(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// Request rows ───────────────────────────────────────────────────────────────

type RequestRow struct {
	ID            int64  `json:"id"`
	TS            int64  `json:"ts"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
	ProxyID       *int64 `json:"proxy_id"`
	ProxyName     string `json:"proxy_name"`
	AccountID     *int64 `json:"account_id"`
	AccountLabel  string `json:"account_label"`
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
        (ts, model, upstream_model, proxy_id, proxy_name, account_id, account_label,
         status, error, ttfb_ms, total_ms,
         prompt_chars, response_chars, prompt_tokens, output_tokens,
         endpoint, stream)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		r.TS, r.Model, r.UpstreamModel, int64Ptr(r.ProxyID), r.ProxyName, int64Ptr(r.AccountID), r.AccountLabel,
		r.Status, r.Error, int64Ptr(r.TTFBMs), r.TotalMs,
		r.PromptChars, r.ResponseChars, r.PromptTokens, r.OutputTokens,
		r.Endpoint, r.Stream)
	if err != nil {
		logf("[db] insert request failed: %v", err)
	}
}

// Sessions ───────────────────────────────────────────────────────────────────

func createSession(token string, ttl time.Duration) {
	now := time.Now().Unix()
	_, err := getDB().Exec(`INSERT INTO sessions(token, created_at, expires_at) VALUES ($1,$2,$3)
		ON CONFLICT(token) DO UPDATE SET created_at=excluded.created_at, expires_at=excluded.expires_at`,
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
	err := getDB().QueryRow(`SELECT expires_at FROM sessions WHERE token=$1`, token).Scan(&exp)
	if err != nil {
		return false
	}
	return time.Now().Unix() < exp
}

// KV helpers ─────────────────────────────────────────────────────────────────

func kvGet(k string) string {
	var v string
	if err := getDB().QueryRow(`SELECT v FROM kv WHERE k=$1`, k).Scan(&v); err != nil {
		return ""
	}
	return v
}

func kvSet(k, v string) error {
	_, err := getDB().Exec(`INSERT INTO kv(k,v) VALUES($1,$2) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}
