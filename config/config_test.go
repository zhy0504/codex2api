package config

import "testing"

func resetEnvForLoadTests(t *testing.T) {
	t.Helper()

	keys := []string{
		"CODEX_PORT",
		"PORT",
		"APP_ENV",
		"ADMIN_SECRET",
		"CODEX_API_KEYS",
		"CORS_ALLOWED_ORIGINS",
		"CREDENTIALS_ENCRYPTION_KEY",
		"CREDENTIALS_ENCRYPTION_KEY_FILE",
		"DATABASE_HOST",
		"DATABASE_PORT",
		"DATABASE_USER",
		"DATABASE_PASSWORD",
		"DATABASE_NAME",
		"DATABASE_SSLMODE",
		"REDIS_ADDR",
		"REDIS_PASSWORD",
		"REDIS_DB",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}

func TestLoadDefaultsToPostgresAndRedis(t *testing.T) {
	resetEnvForLoadTests(t)
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")

	// 只提供各驱动所需最小参数。
	t.Setenv("DATABASE_HOST", "postgres")
	t.Setenv("REDIS_ADDR", "redis:6379")

	cfg, err := Load("__not_exists__.env")
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}

	if got := cfg.Database.Host; got != "postgres" {
		t.Fatalf("Database.Host = %q, want %q", got, "postgres")
	}
	if got := cfg.Redis.Addr; got != "redis:6379" {
		t.Fatalf("Redis.Addr = %q, want %q", got, "redis:6379")
	}
	if got := cfg.Database.Port; got != 5432 {
		t.Fatalf("Database.Port = %d, want %d", got, 5432)
	}
	if got := cfg.Database.SSLMode; got != "disable" {
		t.Fatalf("Database.SSLMode = %q, want %q", got, "disable")
	}
	if got := cfg.Port; got != 8080 {
		t.Fatalf("Port = %d, want %d", got, 8080)
	}
	if got := cfg.BootstrapAdminSecret; got != "" {
		t.Fatalf("BootstrapAdminSecret = %q, want empty", got)
	}
}

func TestLoadParsesStaticAPIKeysAndCORSOrigins(t *testing.T) {
	resetEnvForLoadTests(t)
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DATABASE_HOST", "postgres")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("CODEX_API_KEYS", "k1, k2, k1, ,k3")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.example.com, https://b.example.com, https://a.example.com")

	cfg, err := Load("__not_exists__.env")
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}

	if len(cfg.StaticAPIKeys) != 3 {
		t.Fatalf("StaticAPIKeys len = %d, want %d", len(cfg.StaticAPIKeys), 3)
	}
	if cfg.StaticAPIKeys[0] != "k1" || cfg.StaticAPIKeys[1] != "k2" || cfg.StaticAPIKeys[2] != "k3" {
		t.Fatalf("StaticAPIKeys = %#v, want [k1 k2 k3]", cfg.StaticAPIKeys)
	}

	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("CORSAllowedOrigins len = %d, want %d", len(cfg.CORSAllowedOrigins), 2)
	}
	if cfg.CORSAllowedOrigins[0] != "https://a.example.com" || cfg.CORSAllowedOrigins[1] != "https://b.example.com" {
		t.Fatalf("CORSAllowedOrigins = %#v", cfg.CORSAllowedOrigins)
	}
}

func TestLoadReadsAdminSecretFromEnv(t *testing.T) {
	resetEnvForLoadTests(t)
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")

	t.Setenv("DATABASE_HOST", "postgres")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("ADMIN_SECRET", "from-env-secret")

	cfg, err := Load("__not_exists__.env")
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}

	if got := cfg.BootstrapAdminSecret; got != "from-env-secret" {
		t.Fatalf("BootstrapAdminSecret = %q, want %q", got, "from-env-secret")
	}
}
