package app

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Proxy struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	Weight    int    `json:"weight"`
	FailCount int    `json:"fail_count"`
	LastUsed  int64  `json:"last_used"`
	LastError string `json:"last_error"`
	CreatedAt int64  `json:"created_at"`
}

var (
	proxyMu     sync.RWMutex
	proxyCache  []Proxy
	proxyCursor uint64
)

// loadProxies 从 DB 刷新内存里的代理列表。
//
// 读一半失败时**保留上一次的池子**，绝不用半截结果覆盖。
// 旧写法有两个静默失效点：Scan 出错 continue（悄悄漏掉一个代理）、rows.Err()
// 完全不查（遍历中断当成正常读完）。两者都会让 proxyCache 变短甚至变空，而
// acquireSlot 用 len(proxyCache)==0 判断"没配代理池"，于是**池子一空就退回直连**
// —— 部署者的真实 IP 直接暴露给上游，日志上只看到偶发的直连请求。
//
// 这条路径每个请求都会走（recordProxyResult 结束就调），而 WAL 模式下并发
// UPDATE 期间 rows.Next() 完全可能返回 SQLITE_BUSY，所以"偶发"就是这么来的。
func loadProxies() {
	rows, err := getDB().Query(`SELECT id, name, url, enabled, weight, fail_count,
        IFNULL(last_used,0), IFNULL(last_error,''), created_at FROM proxies ORDER BY id`)
	if err != nil {
		logf("[proxy] 读取失败，保留上一次的代理池: %v", err)
		return
	}
	defer rows.Close()
	var list []Proxy
	for rows.Next() {
		var p Proxy
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.URL, &enabled, &p.Weight, &p.FailCount,
			&p.LastUsed, &p.LastError, &p.CreatedAt); err != nil {
			logf("[proxy] 有行读不出来，保留上一次的代理池: %v", err)
			return
		}
		p.Enabled = enabled == 1
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		logf("[proxy] 遍历中断，保留上一次的代理池: %v", err)
		return
	}
	proxyMu.Lock()
	proxyCache = list
	proxyMu.Unlock()
}

// pickProxyWithCapacity 找一个 enabled + fail<5 + 当前限流没满的代理。
// 返回 (proxy, ok)。所有代理都满时返回 ok=false。
//
// 跟旧的 pickProxy 区别：会问 trySlotAcquire 看 slot 是否有容量；
// 调用方拿到的 slot 必须配套调 slotRelease(proxy.ID)。
func pickProxyWithCapacity() (Proxy, bool) {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	if len(proxyCache) == 0 {
		return Proxy{}, false
	}
	var pool []Proxy
	for _, p := range proxyCache {
		if p.Enabled && p.FailCount < 5 {
			pool = append(pool, p)
		}
	}
	if len(pool) == 0 {
		return Proxy{}, false
	}
	// 从轮询起点开始,找第一个 slot 没满的
	start := atomic.AddUint64(&proxyCursor, 1) - 1
	for i := 0; i < len(pool); i++ {
		p := pool[(int(start)+i)%len(pool)]
		if ok, _ := trySlotAcquire(p.ID); ok {
			return p, true
		}
	}
	return Proxy{}, false
}

// recordProxyResult 回写一次请求的结果，并同步更新内存里那一条。
//
// 只改内存里的那一条，不整表重读：这个函数每个请求都会调，重读一次就是一次全表
// SELECT，而它自己刚发起过 UPDATE —— 高并发下正是这对读写在 WAL 上撞出 SQLITE_BUSY，
// 也就是代理池被读空、请求退回直连的触发条件。
//
// 代价是内存里的 FailCount++ 和 DB 的 fail_count+1 各算各的，别的进程直接改库会
// 让两边漂移。单进程持有这个库，重启也会从 DB 重新加载，可以接受。
func recordProxyResult(id int64, success bool, errStr string) {
	if id == 0 {
		return
	}
	now := time.Now().Unix()
	if success {
		_, _ = getDB().Exec(`UPDATE proxies SET fail_count=0, last_used=?, last_error='' WHERE id=?`, now, id)
	} else {
		_, _ = getDB().Exec(`UPDATE proxies SET fail_count=fail_count+1, last_used=?, last_error=? WHERE id=?`,
			now, errStr, id)
	}
	proxyMu.Lock()
	for i := range proxyCache {
		if proxyCache[i].ID != id {
			continue
		}
		proxyCache[i].LastUsed = now
		if success {
			proxyCache[i].FailCount = 0
			proxyCache[i].LastError = ""
		} else {
			proxyCache[i].FailCount++
			proxyCache[i].LastError = errStr
		}
		break
	}
	proxyMu.Unlock()
}

// CRUD ───────────────────────────────────────────────────────────────────────

func proxyCreate(name, url string, weight int) (int64, error) {
	if name == "" || url == "" {
		return 0, errors.New("name and url required")
	}
	if err := validateProxyURL(url); err != nil {
		return 0, err
	}
	if weight <= 0 {
		weight = 1
	}
	res, err := getDB().Exec(`INSERT INTO proxies(name, url, enabled, weight, created_at)
        VALUES (?,?,?,?,?)`, name, url, 1, weight, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	loadProxies()
	return id, nil
}

// validateProxyURL 校验代理 URL 协议（参考 Kiro-Gogogo 实现）。
// 支持 http / https / socks5 / socks5h。
func validateProxyURL(s string) error {
	if !strings.HasPrefix(s, "http://") &&
		!strings.HasPrefix(s, "https://") &&
		!strings.HasPrefix(s, "socks5://") &&
		!strings.HasPrefix(s, "socks5h://") {
		return errors.New("代理 URL 必须以 http:// / https:// / socks5:// / socks5h:// 开头")
	}
	return nil
}

func proxyUpdate(id int64, name, url string, enabled *bool, weight *int) error {
	q := `UPDATE proxies SET `
	args := []interface{}{}
	parts := []string{}
	if name != "" {
		parts = append(parts, "name=?")
		args = append(args, name)
	}
	if url != "" {
		parts = append(parts, "url=?")
		args = append(args, url)
	}
	if enabled != nil {
		v := 0
		if *enabled {
			v = 1
		}
		parts = append(parts, "enabled=?")
		args = append(args, v)
	}
	if weight != nil {
		parts = append(parts, "weight=?")
		args = append(args, *weight)
	}
	if len(parts) == 0 {
		return nil
	}
	q += joinComma(parts) + " WHERE id=?"
	args = append(args, id)
	_, err := getDB().Exec(q, args...)
	if err == nil {
		loadProxies()
	}
	return err
}

func proxyDelete(id int64) error {
	_, err := getDB().Exec(`DELETE FROM proxies WHERE id=?`, id)
	if err == nil {
		loadProxies()
	}
	return err
}

func proxyResetFailures(id int64) error {
	_, err := getDB().Exec(`UPDATE proxies SET fail_count=0, last_error='' WHERE id=?`, id)
	if err == nil {
		loadProxies()
	}
	return err
}

func listProxies() []Proxy {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	out := make([]Proxy, len(proxyCache))
	copy(out, proxyCache)
	return out
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
