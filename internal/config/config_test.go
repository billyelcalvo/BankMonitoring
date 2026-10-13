package config

import (
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
	"time"
)

var validSecret = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32)))

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
		"JWT_SECRET":   validSecret,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.JWTIssuer != "bankmonitoring" || cfg.JWTAudience != "bankmonitoring-api" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DBMaxConns != 20 || cfg.RequestTimeout != 10*time.Second || !cfg.RunMigrations || cfg.SeedDemoData {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo || len(cfg.JWTSecret) != 32 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL":     "postgres://localhost/db",
		"JWT_SECRET":       validSecret,
		"PORT":             "10000",
		"LOG_LEVEL":        "debug",
		"DB_MAX_CONNS":     "50",
		"REQUEST_TIMEOUT":  "2s",
		"SHUTDOWN_TIMEOUT": "30s",
		"RUN_MIGRATIONS":   "false",
		"SEED_DEMO_DATA":   "true",
		"DEMO_PASSWORD":    "long-enough",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":10000" || cfg.LogLevel != slog.LevelDebug || cfg.DBMaxConns != 50 {
		t.Fatalf("overrides ignored: %+v", cfg)
	}
	if cfg.RequestTimeout != 2*time.Second || cfg.ShutdownTimeout != 30*time.Second || cfg.RunMigrations || !cfg.SeedDemoData {
		t.Fatalf("overrides ignored: %+v", cfg)
	}

	// HTTP_ADDR wins over PORT when both are set.
	cfg, _ = Load(env(map[string]string{"HTTP_ADDR": "127.0.0.1:3000", "PORT": "1"}))
	if cfg.HTTPAddr != "127.0.0.1:3000" {
		t.Fatalf("HTTP_ADDR should take precedence, got %s", cfg.HTTPAddr)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"missing database": {"JWT_SECRET": validSecret},
		"short secret":     {"DATABASE_URL": "x", "JWT_SECRET": base64.StdEncoding.EncodeToString([]byte("short"))},
		"not base64":       {"DATABASE_URL": "x", "JWT_SECRET": "%%%"},
		"log level":        {"DATABASE_URL": "x", "JWT_SECRET": validSecret, "LOG_LEVEL": "loud"},
		"max conns":        {"DATABASE_URL": "x", "JWT_SECRET": validSecret, "DB_MAX_CONNS": "0"},
		"timeout":          {"DATABASE_URL": "x", "JWT_SECRET": validSecret, "REQUEST_TIMEOUT": "soon"},
		"bool":             {"DATABASE_URL": "x", "JWT_SECRET": validSecret, "RUN_MIGRATIONS": "maybe"},
		"demo password":    {"DATABASE_URL": "x", "JWT_SECRET": validSecret, "SEED_DEMO_DATA": "true"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(values)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
