package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codex2api/admin"
	"github.com/codex2api/auth"
	"github.com/codex2api/cache"
	"github.com/codex2api/config"
	"github.com/codex2api/database"
	"github.com/codex2api/proxy"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

//go:embed frontend/dist/*
var frontendFS embed.FS

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Codex2API v2 启动中...")

	// 1. 加载配置 (.env)
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("加载核心环境配置失败 (请检查 .env 文件): %v", err)
	}
	log.Printf("物理层配置加载成功: port=%d", cfg.Port)

	// 2. 初始化 PostgreSQL
	db, err := database.New(cfg.Database.DSN())
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer db.Close()
	log.Printf("PostgreSQL 连接成功: %s:%d/%s", cfg.Database.Host, cfg.Database.Port, cfg.Database.DBName)

	if err := db.SetCredentialsEncryptionKey(cfg.CredentialsEncryptionKey); err != nil {
		log.Fatalf("初始化凭据加密失败: %v", err)
	}
	if db.CredentialsEncryptionEnabled() {
		encCtx, encCancel := context.WithTimeout(context.Background(), 30*time.Second)
		migratedCount, migrateErr := db.EncryptExistingCredentialSecrets(encCtx)
		encCancel()
		if migrateErr != nil {
			log.Fatalf("凭据加密迁移失败: %v", migrateErr)
		}
		if migratedCount > 0 {
			log.Printf("凭据加密迁移完成：已处理 %d 个账号", migratedCount)
		} else {
			log.Printf("凭据加密已启用")
		}
	} else {
		log.Printf("警告: 未配置 CREDENTIALS_ENCRYPTION_KEY，凭据将以明文保存（仅建议开发环境）")
	}

	// 3. 读取运行时的系统逻辑设置（需在 Redis 初始化之前，以获取 pool size）
	sysCtx, sysCancel := context.WithTimeout(context.Background(), 5*time.Second)
	settings, err := db.GetSystemSettings(sysCtx)
	sysCancel()

	if err == nil && settings == nil {
		// 初次运行，保存初始安全设置到数据库
		log.Printf("初次运行，初始化系统默认设置...")
		settings = &database.SystemSettings{
			MaxConcurrency:        2,
			GlobalRPM:             0,
			TestModel:             "gpt-5.4",
			TestConcurrency:       50,
			ProxyURL:              "",
			PgMaxConns:            50,
			RedisPoolSize:         30,
			AutoCleanUnauthorized: false,
			AutoCleanRateLimited:  false,
		}
		_ = db.UpdateSystemSettings(context.Background(), settings)
	} else if err != nil {
		log.Printf("警告: 读取系统设置失败: %v，将采用安全后备策略", err)
		settings = &database.SystemSettings{MaxConcurrency: 2, GlobalRPM: 0, TestModel: "gpt-5.4", TestConcurrency: 50, PgMaxConns: 50, RedisPoolSize: 30}
	} else {
		log.Printf("已加载持久化业务设置: ProxyURL=%s, MaxConcurrency=%d, GlobalRPM=%d, PgMaxConns=%d, RedisPoolSize=%d",
			settings.ProxyURL, settings.MaxConcurrency, settings.GlobalRPM, settings.PgMaxConns, settings.RedisPoolSize)
	}

	if settings.AdminSecret == "" && cfg.BootstrapAdminSecret != "" {
		settings.AdminSecret = cfg.BootstrapAdminSecret
		if err := db.UpdateSystemSettings(context.Background(), settings); err != nil {
			log.Printf("警告: 从环境变量初始化 AdminSecret 失败: %v", err)
		} else {
			log.Printf("已从环境变量 ADMIN_SECRET 初始化管理密钥")
		}
	}

	if err := ensureProductionSecurityBaseline(cfg, db, settings); err != nil {
		log.Fatalf("生产环境安全基线检查失败: %v", err)
	}

	// 4. 初始化 Redis（使用数据库中保存的 pool size）
	redisPoolSize := 30
	if settings.RedisPoolSize > 0 {
		redisPoolSize = settings.RedisPoolSize
	}
	tc, err := cache.New(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, redisPoolSize)
	if err != nil {
		log.Fatalf("Redis 初始化失败: %v", err)
	}
	defer tc.Close()
	log.Printf("Redis 连接成功: %s, pool_size=%d", cfg.Redis.Addr, redisPoolSize)

	// 4b. 应用 PostgreSQL 连接池设置
	if settings.PgMaxConns > 0 {
		db.SetMaxOpenConns(settings.PgMaxConns)
		log.Printf("PostgreSQL 连接池: max_conns=%d", settings.PgMaxConns)
	}

	// 5. 初始化账号管理器
	store := auth.NewStore(db, tc, settings)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	if err := store.Init(ctx); err != nil {
		cancel()
		log.Fatalf("账号初始化失败: %v", err)
	}
	cancel()

	// 全局 RPM 限流器
	rateLimiter := proxy.NewRateLimiter(settings.GlobalRPM)
	adminHandler := admin.NewHandler(store, db, tc, rateLimiter, cfg.BootstrapAdminSecret)
	// 初始化 admin handler 的连接池设置跟踪
	adminHandler.SetPoolSizes(settings.PgMaxConns, settings.RedisPoolSize)
	store.SetUsageProbeFunc(adminHandler.ProbeUsageSnapshot)

	// 启动后台刷新
	store.StartBackgroundRefresh()
	store.TriggerUsageProbeAsync()
	store.TriggerRecoveryProbeAsync()
	store.TriggerAutoCleanupAsync()
	defer store.Stop()

	log.Printf("账号就绪: %d/%d 可用", store.AvailableCount(), store.AccountCount())

	// 6. 启动 HTTP 服务
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(loggerMiddleware())
	r.Use(securityHeadersMiddleware())
	r.Use(corsMiddleware(cfg.CORSAllowedOrigins))

	// handler 不再接收 cfg.APIKeys
	handler := proxy.NewHandler(store, db, cfg.StaticAPIKeys)

	r.Use(rateLimiter.Middleware())
	if settings.GlobalRPM > 0 {
		log.Printf("全局限流已生效: %d RPM", settings.GlobalRPM)
	}
	log.Printf("单账号并发上限: %d", settings.MaxConcurrency)

	handler.RegisterRoutes(r)
	adminHandler.RegisterRoutes(r)

	// 管理后台前端静态文件
	subFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		log.Printf("前端静态文件加载失败（开发模式可忽略）: %v", err)
	} else {
		httpFS := http.FS(subFS)
		// 预读 index.html（SPA 回退时直接返回，避免 FileServer 重定向）
		indexHTML, _ := fs.ReadFile(subFS, "index.html")

		serveAdmin := func(c *gin.Context) {
			fp := c.Param("filepath")
			// 尝试打开请求的文件（排除目录和根路径）
			if fp != "/" && len(fp) > 1 {
				trimmed := fp[1:] // 去掉开头的 /
				if f, err := subFS.Open(trimmed); err == nil {
					fi, statErr := f.Stat()
					f.Close()
					if statErr == nil && !fi.IsDir() {
						c.FileFromFS(fp, httpFS)
						return
					}
				}
			}
			// 文件不存在或者是目录 → 直接返回 index.html 字节（让 React Router 处理）
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
		}

		// 同时处理 /admin 和 /admin/*，避免依赖自动补斜杠重定向。
		r.GET("/admin", serveAdmin)
		r.GET("/admin/*filepath", serveAdmin)
	}

	// 根路径重定向到管理后台（使用 302 避免浏览器永久缓存）
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin/")
	})

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":    "ok",
			"available": store.AvailableCount(),
			"total":     store.AccountCount(),
		})
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Println("==========================================")
	log.Printf("  Codex2API v2 已启动")
	log.Printf("  HTTP:   http://0.0.0.0%s", addr)
	log.Printf("  管理台: http://0.0.0.0%s/admin/", addr)
	log.Printf("  API:    POST /v1/chat/completions")
	log.Printf("  API:    POST /v1/responses")
	log.Printf("  API:    GET  /v1/models")
	log.Println("==========================================")

	// 优雅关闭
	go func() {
		if err := r.Run(addr); err != nil {
			log.Fatalf("HTTP 服务启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在关闭...")
	store.Stop()
	log.Println("已关闭")
}

func ensureProductionSecurityBaseline(cfg *config.Config, db *database.DB, settings *database.SystemSettings) error {
	if cfg == nil || !cfg.IsProduction() {
		return nil
	}

	adminSecret := strings.TrimSpace(cfg.BootstrapAdminSecret)
	if settings != nil && strings.TrimSpace(settings.AdminSecret) != "" {
		adminSecret = strings.TrimSpace(settings.AdminSecret)
	}
	if adminSecret == "" {
		return fmt.Errorf("APP_ENV=production 时必须配置管理密钥（system_settings.admin_secret 或 ADMIN_SECRET）")
	}

	if len(strings.TrimSpace(cfg.CredentialsEncryptionKey)) < 32 {
		return fmt.Errorf("APP_ENV=production 时必须配置 CREDENTIALS_ENCRYPTION_KEY，且长度至少为 32")
	}

	hasAPIKeys := len(cfg.StaticAPIKeys) > 0
	if !hasAPIKeys {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		keys, err := db.GetAllAPIKeyValues(ctx)
		if err != nil {
			return fmt.Errorf("检查 API Key 配置失败: %w", err)
		}
		hasAPIKeys = len(keys) > 0
	}
	if !hasAPIKeys {
		return fmt.Errorf("APP_ENV=production 时必须至少配置一个 API Key（数据库 api_keys 或 CODEX_API_KEYS）")
	}

	return nil
}

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.Writer.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		c.Next()
	}
}

func normalizeOrigin(origin string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(origin)), "/")
}

func requestScheme(c *gin.Context) string {
	if c != nil && c.Request != nil && c.Request.TLS != nil {
		return "https"
	}
	if c != nil && strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https") {
		return "https"
	}
	return "http"
}

func isSameOriginRequest(c *gin.Context, origin string) bool {
	if c == nil || c.Request == nil {
		return false
	}
	normOrigin := normalizeOrigin(origin)
	if normOrigin == "" {
		return false
	}
	expected := normalizeOrigin(fmt.Sprintf("%s://%s", requestScheme(c), c.Request.Host))
	return expected != "" && normOrigin == expected
}

func corsMiddleware(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		normalized := normalizeOrigin(origin)
		if normalized == "" {
			continue
		}
		allowed[normalized] = true
	}

	const allowMethods = "GET,POST,PUT,PATCH,DELETE,OPTIONS"
	const allowHeaders = "Authorization,Content-Type,X-Admin-Key,X-Request-ID"
	const exposeHeaders = "X-Request-ID"

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}

		normalized := normalizeOrigin(origin)
		allowedByConfig := allowed["*"] || allowed[normalized]
		allowedBySameOrigin := isSameOriginRequest(c, origin)
		if !allowedByConfig && !allowedBySameOrigin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "CORS origin not allowed",
			})
			return
		}

		header := c.Writer.Header()
		header.Set("Vary", "Origin")
		header.Set("Access-Control-Allow-Origin", origin)
		header.Set("Access-Control-Allow-Credentials", "true")
		header.Set("Access-Control-Allow-Methods", allowMethods)
		header.Set("Access-Control-Allow-Headers", allowHeaders)
		header.Set("Access-Control-Expose-Headers", exposeHeaders)

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

const requestIDContextKey = "x-request-id"

func resolveRequestID(c *gin.Context) string {
	if c != nil {
		incoming := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if incoming != "" {
			if len(incoming) > 128 {
				return incoming[:128]
			}
			return incoming
		}
	}
	return "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func contextString(c *gin.Context, key string) string {
	if c == nil || strings.TrimSpace(key) == "" {
		return ""
	}
	v, ok := c.Get(key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func contextInt64(c *gin.Context, key string) (int64, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return 0, false
	}
	v, ok := c.Get(key)
	if !ok || v == nil {
		return 0, false
	}

	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		parsed := strings.TrimSpace(n)
		if parsed == "" {
			return 0, false
		}
		var out int64
		if _, err := fmt.Sscan(parsed, &out); err == nil {
			return out, true
		}
	}

	return 0, false
}

// loggerMiddleware 结构化请求日志中间件
func loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := resolveRequestID(c)
		c.Set(requestIDContextKey, requestID)
		c.Writer.Header().Set("X-Request-ID", requestID)

		start := time.Now()
		c.Next()
		latency := time.Since(start)

		entry := map[string]interface{}{
			"type":       "request",
			"request_id": requestID,
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"status":     c.Writer.Status(),
			"latency_ms": latency.Milliseconds(),
			"client_ip":  c.ClientIP(),
		}
		if route := c.FullPath(); route != "" {
			entry["route"] = route
		}
		if ua := strings.TrimSpace(c.Request.UserAgent()); ua != "" {
			entry["user_agent"] = ua
		}
		if accountID, ok := contextInt64(c, "x-account-id"); ok {
			entry["account_id"] = accountID
		}
		if email := contextString(c, "x-account-email"); email != "" {
			entry["account_email"] = email
		}
		if proxyURL := contextString(c, "x-account-proxy"); proxyURL != "" {
			entry["proxy_url"] = proxyURL
		}
		if model := contextString(c, "x-model"); model != "" {
			entry["model"] = model
		}
		if effort := contextString(c, "x-reasoning-effort"); effort != "" {
			entry["reasoning_effort"] = effort
		}
		if tier := contextString(c, "x-service-tier"); tier != "" {
			entry["service_tier"] = tier
		}
		if len(c.Errors) > 0 {
			entry["error"] = c.Errors.String()
		}

		payload, err := json.Marshal(entry)
		if err != nil {
			log.Printf("request log serialize failed: request_id=%s err=%v", requestID, err)
			return
		}
		log.Printf("%s", string(payload))
	}
}
