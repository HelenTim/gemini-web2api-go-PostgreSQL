package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

var (
	clientCache   = map[string]tls_client.HttpClient{}
	clientCacheMu sync.RWMutex
)

// resolveProfile maps a config string to a tls-client ClientProfile.
// Defaults to Chrome_146 (matches Python v2's curl_cffi chrome146).
func resolveProfile(name string) profiles.ClientProfile {
	switch strings.ToLower(name) {
	case "chrome_120":
		return profiles.Chrome_120
	case "chrome_124":
		return profiles.Chrome_124
	case "chrome_131":
		return profiles.Chrome_131
	case "chrome_133":
		return profiles.Chrome_133
	case "chrome_144":
		return profiles.Chrome_144
	case "chrome_146", "chrome146":
		return profiles.Chrome_146
	case "firefox_120":
		return profiles.Firefox_120
	case "firefox_123":
		return profiles.Firefox_123
	case "firefox_147", "firefox147":
		return profiles.Firefox_147
	case "safari_16_0":
		return profiles.Safari_16_0
	case "safari_ios_17_0":
		return profiles.Safari_IOS_17_0
	default:
		return profiles.Chrome_146
	}
}

// getHTTPClient returns a cached tls-client per (impersonate, proxy) pair.
// Each unique proxy URL gets its own connection pool, so latency stays low.
func getHTTPClient(proxyURL string) tls_client.HttpClient {
	key := cfg.Impersonate + "|" + proxyURL
	clientCacheMu.RLock()
	if c, ok := clientCache[key]; ok {
		clientCacheMu.RUnlock()
		return c
	}
	clientCacheMu.RUnlock()

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()
	if c, ok := clientCache[key]; ok {
		return c
	}
	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(cfg.RequestTimeout),
		tls_client.WithClientProfile(resolveProfile(cfg.Impersonate)),
		tls_client.WithNotFollowRedirects(),
	}
	if proxyURL != "" {
		opts = append(opts, tls_client.WithProxyUrl(proxyURL))
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[client] init failed: %v\n", err)
		os.Exit(1)
	}
	clientCache[key] = client
	return client
}

// loadCookie reads the cookie file (Netscape one-line format or JSON).
func loadCookie() (string, string) {
	if cfg.CookieFile == "" {
		return "", ""
	}
	data, err := os.ReadFile(cfg.CookieFile)
	if err != nil {
		return "", ""
	}
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "{") {
		var obj struct {
			Cookie  string `json:"cookie"`
			Sapisid string `json:"sapisid"`
		}
		if err := json.Unmarshal([]byte(content), &obj); err != nil {
			return "", ""
		}
		return obj.Cookie, obj.Sapisid
	}
	cookieStr := content
	sapisid := ""
	for _, p := range strings.Split(cookieStr, "; ") {
		if eq := strings.Index(p, "="); eq > 0 {
			if p[:eq] == "SAPISID" {
				sapisid = p[eq+1:]
			}
		}
	}
	return cookieStr, sapisid
}

func makeSAPISIDHash(sapisid string) string {
	ts := time.Now().Unix()
	h := sha1.Sum([]byte(fmt.Sprintf("%d %s https://gemini.google.com", ts, sapisid)))
	return fmt.Sprintf("SAPISIDHASH %d_%s", ts, hex.EncodeToString(h[:]))
}
