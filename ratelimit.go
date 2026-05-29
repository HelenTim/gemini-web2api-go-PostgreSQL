package main

import (
	"sync"
	"time"
)

// IPSlot 标识一个独立 IP（id=0 = 直连主机 IP；id>0 = 代理池里的代理）。
// 每个 slot 独立维护并发数 + 滑动窗口 RPM/RPH 计数。
//
// 限额来自 cfg.PerIP* 字段，默认基于实测 Google 单 IP 容忍度：
//   - 瞬时并发：5（实测 50 并发即时全过，但中长期会触发 sorry）
//   - 每分钟：30（留 2× 余量给突发）
//   - 每小时：150（实测 100+ 触发 sorry/index 拦截）

type ipSlot struct {
	mu        sync.Mutex
	inflight  int
	minuteWin []int64 // 滑动窗口里每个请求的 unix 时间戳（秒）
	hourWin   []int64
}

var (
	slotsMu sync.RWMutex
	slots   = map[int64]*ipSlot{} // proxy_id -> slot；0 = direct
)

func getSlot(proxyID int64) *ipSlot {
	slotsMu.RLock()
	s, ok := slots[proxyID]
	slotsMu.RUnlock()
	if ok {
		return s
	}
	slotsMu.Lock()
	defer slotsMu.Unlock()
	if s, ok := slots[proxyID]; ok {
		return s
	}
	s = &ipSlot{}
	slots[proxyID] = s
	return s
}

// trySlotAcquire 尝试占一个 slot；超额返回 false + 原因码。
// 调用方拿到 true 必须 deferred 调 slotRelease()。
//
//	"concurrent" — 已达瞬时并发上限
//	"rpm"        — 每分钟超限
//	"rph"        — 每小时超限
func trySlotAcquire(proxyID int64) (bool, string) {
	s := getSlot(proxyID)
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().Unix()
	// 清掉过期窗口
	cutMin := now - 60
	cutHour := now - 3600
	s.minuteWin = pruneTimestamps(s.minuteWin, cutMin)
	s.hourWin = pruneTimestamps(s.hourWin, cutHour)

	if cfg.PerIPConcurrent > 0 && s.inflight >= cfg.PerIPConcurrent {
		return false, "concurrent"
	}
	if cfg.PerIPRPM > 0 && len(s.minuteWin) >= cfg.PerIPRPM {
		return false, "rpm"
	}
	if cfg.PerIPRPH > 0 && len(s.hourWin) >= cfg.PerIPRPH {
		return false, "rph"
	}

	s.inflight++
	s.minuteWin = append(s.minuteWin, now)
	s.hourWin = append(s.hourWin, now)
	return true, ""
}

// slotRelease 释放并发计数（窗口计数自然过期，不在这里清）。
func slotRelease(proxyID int64) {
	s := getSlot(proxyID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight > 0 {
		s.inflight--
	}
}

// SlotUsage admin UI 看用量用。
type SlotUsage struct {
	ProxyID    int64 `json:"proxy_id"`
	Inflight   int   `json:"inflight"`
	RPM        int   `json:"rpm"`
	RPH        int   `json:"rph"`
	LimitConc  int   `json:"limit_concurrent"`
	LimitRPM   int   `json:"limit_rpm"`
	LimitRPH   int   `json:"limit_rph"`
}

func slotUsage(proxyID int64) SlotUsage {
	s := getSlot(proxyID)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	s.minuteWin = pruneTimestamps(s.minuteWin, now-60)
	s.hourWin = pruneTimestamps(s.hourWin, now-3600)
	return SlotUsage{
		ProxyID:    proxyID,
		Inflight:   s.inflight,
		RPM:        len(s.minuteWin),
		RPH:        len(s.hourWin),
		LimitConc:  cfg.PerIPConcurrent,
		LimitRPM:   cfg.PerIPRPM,
		LimitRPH:   cfg.PerIPRPH,
	}
}

// allSlotUsage 返回所有 slot 的用量快照（直连 + 全部代理）。
func allSlotUsage() []SlotUsage {
	slotsMu.RLock()
	ids := make([]int64, 0, len(slots))
	for id := range slots {
		ids = append(ids, id)
	}
	slotsMu.RUnlock()
	out := make([]SlotUsage, 0, len(ids))
	for _, id := range ids {
		out = append(out, slotUsage(id))
	}
	return out
}

func pruneTimestamps(ts []int64, cutoff int64) []int64 {
	// ts 是按时间递增追加的，找第一个 >= cutoff 的位置切掉前面
	i := 0
	for i < len(ts) && ts[i] < cutoff {
		i++
	}
	if i == 0 {
		return ts
	}
	return ts[i:]
}
