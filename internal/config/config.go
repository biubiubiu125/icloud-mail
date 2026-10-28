package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr      string `json:"http_addr"`
	DatabaseURL   string `json:"database_url"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
	APIKey        string `json:"api_key"`
	PublicBaseURL string `json:"public_base_url"`
	CookieSecure  bool   `json:"cookie_secure"`
	SessionHours  int    `json:"session_hours"`
}

func Load(path string) (Config, error) {
	cfg := Config{
		HTTPAddr:     ":8080",
		SessionHours: 12,
	}
	if strings.TrimSpace(path) != "" {
		if raw, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return Config{}, err
			}
		} else if !os.IsNotExist(err) {
			return Config{}, err
		}
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_HTTP_ADDR")); value != "" {
		cfg.HTTPAddr = value
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_DATABASE_URL")); value != "" {
		cfg.DatabaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_ADMIN_USERNAME")); value != "" {
		cfg.AdminUsername = value
	}
	if value := os.Getenv("ICLOUD_MAIL_ADMIN_PASSWORD"); strings.TrimSpace(value) != "" {
		cfg.AdminPassword = value
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_API_KEY")); value != "" {
		cfg.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_PUBLIC_BASE_URL")); value != "" {
		cfg.PublicBaseURL = strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_COOKIE_SECURE")); value != "" {
		cfg.CookieSecure = value == "1" || strings.EqualFold(value, "true")
	}
	if value := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_SESSION_HOURS")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			cfg.SessionHours = parsed
		}
	}
	if cfg.SessionHours <= 0 {
		cfg.SessionHours = 12
	}
	if strings.HasPrefix(strings.ToLower(cfg.PublicBaseURL), "https://") {
		cfg.CookieSecure = true
	}
	return cfg, nil
}
