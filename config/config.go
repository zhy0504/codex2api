package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// DatabaseConfig PostgreSQL 核心配置
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// DSN 返回 PostgreSQL 连接字符串
func (d *DatabaseConfig) DSN() string {
	sslMode := d.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, sslMode)
}

// RedisConfig Redis 核心配置
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// Config 全局核心环境配置（物理隔离的服务器参数）
// 业务逻辑参数（如 ProxyURL，APIKeys，MaxConcurrency）已全部移至数据库 SystemSettings 进行化
type Config struct {
	Port                     int
	AppEnv                   string
	BootstrapAdminSecret     string
	CredentialsEncryptionKey string
	StaticAPIKeys            []string
	CORSAllowedOrigins       []string
	Database                 DatabaseConfig
	Redis                    RedisConfig
}

// IsProduction 判断当前是否为生产环境
func (c *Config) IsProduction() bool {
	if c == nil {
		return false
	}
	env := strings.ToLower(strings.TrimSpace(c.AppEnv))
	return env == "production" || env == "prod"
}

func parseCommaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func credentialsEncryptionKeyFilePath() string {
	if v := strings.TrimSpace(os.Getenv("CREDENTIALS_ENCRYPTION_KEY_FILE")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".codex2api", "credentials_encryption_key")
	}
	return ".credentials_encryption_key"
}

func readCredentialsEncryptionKeyFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(content))
	if key == "" {
		return "", fmt.Errorf("凭据加密密钥文件为空: %s", path)
	}
	if len(key) < 32 {
		return "", fmt.Errorf("凭据加密密钥长度不足（至少 32 字符）: %s", path)
	}
	return key, nil
}

func generateCredentialsEncryptionKey() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成凭据加密密钥失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeCredentialsEncryptionKeyFile(path, key string) error {
	if key == "" {
		return errors.New("凭据加密密钥不能为空")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建凭据加密密钥目录失败 (%s): %w", dir, err)
		}
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("写入凭据加密密钥文件失败 (%s): %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(key + "\n"); err != nil {
		return fmt.Errorf("写入凭据加密密钥内容失败 (%s): %w", path, err)
	}
	return nil
}

func resolveCredentialsEncryptionKey() (string, error) {
	if v := strings.TrimSpace(os.Getenv("CREDENTIALS_ENCRYPTION_KEY")); v != "" {
		return v, nil
	}

	keyFilePath := credentialsEncryptionKeyFilePath()
	if key, err := readCredentialsEncryptionKeyFile(keyFilePath); err == nil {
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	generated, err := generateCredentialsEncryptionKey()
	if err != nil {
		return "", err
	}

	if err := writeCredentialsEncryptionKeyFile(keyFilePath, generated); err != nil {
		if errors.Is(err, os.ErrExist) {
			return readCredentialsEncryptionKeyFile(keyFilePath)
		}
		return "", err
	}

	return generated, nil
}

// Load 从 .env 文件加载核心环境配置，支持环境变量覆盖
func Load(envPath string) (*Config, error) {
	// 尝试加载 .env 文件（可选，如果文件不存在则忽略并使用当前环境变量）
	if envPath == "" {
		envPath = ".env"
	}
	_ = godotenv.Load(envPath)

	cfg := &Config{Port: 8080, AppEnv: "development"}

	if env := strings.TrimSpace(os.Getenv("APP_ENV")); env != "" {
		cfg.AppEnv = strings.ToLower(env)
	}
	cfg.BootstrapAdminSecret = strings.TrimSpace(os.Getenv("ADMIN_SECRET"))
	credentialsKey, err := resolveCredentialsEncryptionKey()
	if err != nil {
		return nil, err
	}
	cfg.CredentialsEncryptionKey = credentialsKey
	cfg.StaticAPIKeys = parseCommaSeparated(os.Getenv("CODEX_API_KEYS"))
	cfg.CORSAllowedOrigins = parseCommaSeparated(os.Getenv("CORS_ALLOWED_ORIGINS"))

	// Web服务端口
	if port := os.Getenv("CODEX_PORT"); port != "" {
		fmt.Sscanf(port, "%d", &cfg.Port)
	} else if port := os.Getenv("PORT"); port != "" {
		fmt.Sscanf(port, "%d", &cfg.Port)
	}

	// 数据库配置
	cfg.Database.Host = os.Getenv("DATABASE_HOST")
	if v := os.Getenv("DATABASE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Database.Port = p
		}
	}
	cfg.Database.User = os.Getenv("DATABASE_USER")
	cfg.Database.Password = os.Getenv("DATABASE_PASSWORD")
	cfg.Database.DBName = os.Getenv("DATABASE_NAME")
	if v := os.Getenv("DATABASE_SSLMODE"); v != "" {
		cfg.Database.SSLMode = v
	}

	// Redis配置
	cfg.Redis.Addr = os.Getenv("REDIS_ADDR")
	cfg.Redis.Password = os.Getenv("REDIS_PASSWORD")
	if v := os.Getenv("REDIS_DB"); v != "" {
		if db, err := strconv.Atoi(v); err == nil {
			cfg.Redis.DB = db
		}
	}

	// 校验必填物理层配置
	if cfg.Database.Host == "" {
		return nil, fmt.Errorf("必须通过 .env 或环境变量配置 PostgreSQL (DATABASE_HOST)")
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Redis.Addr == "" {
		return nil, fmt.Errorf("必须通过 .env 或环境变量配置 Redis (REDIS_ADDR)")
	}

	return cfg, nil
}
