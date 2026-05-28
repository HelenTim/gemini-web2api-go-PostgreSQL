package main

import (
	"encoding/json"
	"os"
	"sync"
)

type Config struct {
	Port            int    `json:"port"`
	Host            string `json:"host"`
	RetryAttempts  int    `json:"retry_attempts"`
	RetryDelaySec  int    `json:"retry_delay_sec"`
	RequestTimeout int    `json:"request_timeout_sec"`
	GeminiBL       string `json:"gemini_bl"`
	DefaultModel   string `json:"default_model"`
	LogRequests    bool   `json:"log_requests"`
	CookieFile     string `json:"cookie_file"`
	Proxy          string `json:"proxy"`
	Impersonate    string `json:"impersonate"`
	DBPath         string `json:"db_path"`
	AdminToken     string `json:"admin_token"`
	AdminEnabled   bool   `json:"admin_enabled"`
	RetentionDays  int    `json:"retention_days"`
}

var (
	cfg     Config
	cfgOnce sync.Once
)

func defaultConfig() Config {
	return Config{
		Port:           8083,
		Host:           "0.0.0.0",
		RetryAttempts:  3,
		RetryDelaySec:  2,
		RequestTimeout: 180,
		GeminiBL:       "boq_assistant-bard-web-server_20260525.09_p0",
		DefaultModel:   "gemini-3.5-flash",
		LogRequests:    true,
		CookieFile:     "",
		Proxy:          "",
		Impersonate:    "chrome_146",
		DBPath:         "./data/gemini.db",
		AdminToken:     "",
		AdminEnabled:   true,
		RetentionDays:  30,
	}
}

func loadConfig(path string) error {
	cfg = defaultConfig()
	if path == "" {
		for _, p := range []string{"./config.json", os.ExpandEnv("$HOME/.config/gemini-web2api/config.json")} {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &cfg)
}
