// Package config loads runtime settings from environment variables.
// Nothing here has a production secret baked in: JWT_SECRET and DATABASE_URL
// must always come from the environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	JWTSecret       []byte
	JWTIssuer       string
	JWTAudience     string
	LogLevel        slog.Level
	DBMaxConns      int32
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	RunMigrations   bool
	SeedDemoData    bool
	DemoPassword    string
}

// Load reads the environment through getenv so tests don't need to touch
// the real process environment.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	cfg := Config{
		HTTPAddr:     HTTPAddr(getenv),
		DatabaseURL:  strings.TrimSpace(getenv("DATABASE_URL")),
		JWTIssuer:    withDefault(getenv("JWT_ISSUER"), "bankmonitoring"),
		JWTAudience:  withDefault(getenv("JWT_AUDIENCE"), "bankmonitoring-api"),
		DemoPassword: getenv("DEMO_PASSWORD"),
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	secret, err := base64.StdEncoding.DecodeString(strings.TrimSpace(getenv("JWT_SECRET")))
	if err != nil || len(secret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must be a base64-encoded key of at least 32 bytes"))
	}
	cfg.JWTSecret = secret

	if err := cfg.LogLevel.UnmarshalText([]byte(withDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	maxConns, err := strconv.ParseInt(withDefault(getenv("DB_MAX_CONNS"), "20"), 10, 32)
	if err != nil || maxConns < 1 {
		errs = append(errs, errors.New("DB_MAX_CONNS must be a positive integer"))
	}
	cfg.DBMaxConns = int32(maxConns)

	cfg.RequestTimeout = duration(getenv, "REQUEST_TIMEOUT", 10*time.Second, &errs)
	cfg.ShutdownTimeout = duration(getenv, "SHUTDOWN_TIMEOUT", 10*time.Second, &errs)
	cfg.RunMigrations = boolean(getenv, "RUN_MIGRATIONS", true, &errs)
	cfg.SeedDemoData = boolean(getenv, "SEED_DEMO_DATA", false, &errs)

	if cfg.SeedDemoData && len(cfg.DemoPassword) < 8 {
		errs = append(errs, errors.New("DEMO_PASSWORD must have at least 8 characters when SEED_DEMO_DATA is enabled"))
	}
	return cfg, errors.Join(errs...)
}

// HTTPAddr honours PORT because that's what Render and Fly inject.
func HTTPAddr(getenv func(string) string) string {
	if addr := strings.TrimSpace(getenv("HTTP_ADDR")); addr != "" {
		return addr
	}
	if port := strings.TrimSpace(getenv("PORT")); port != "" {
		return ":" + port
	}
	return ":8080"
}

func withDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func duration(getenv func(string) string, name string, fallback time.Duration, errs *[]error) time.Duration {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive duration like 10s", name))
		return fallback
	}
	return d
}

func boolean(getenv func(string) string, name string, fallback bool, errs *[]error) bool {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be true or false", name))
		return fallback
	}
	return b
}
