package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (db *DB) configureSQLite(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA busy_timeout=5000;`,
		`PRAGMA synchronous=NORMAL;`,
	}
	for _, pragma := range pragmas {
		if _, err := db.conn.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) migrateSQLite(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT DEFAULT '',
			platform TEXT DEFAULT 'openai',
			type TEXT DEFAULT 'oauth',
			credentials TEXT NOT NULL DEFAULT '{}',
			proxy_url TEXT DEFAULT '',
			status TEXT DEFAULT 'active',
			cooldown_reason TEXT DEFAULT '',
			cooldown_until TIMESTAMP NULL,
			error_message TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS usage_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			account_id INTEGER DEFAULT 0,
			endpoint TEXT DEFAULT '',
			model TEXT DEFAULT '',
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			status_code INTEGER DEFAULT 0,
			duration_ms INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
			first_token_ms INTEGER DEFAULT 0,
			reasoning_effort TEXT DEFAULT '',
			inbound_endpoint TEXT DEFAULT '',
			upstream_endpoint TEXT DEFAULT '',
			stream INTEGER DEFAULT 0,
			cached_tokens INTEGER DEFAULT 0,
			service_tier TEXT DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT DEFAULT '',
			key TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS system_settings (
			id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			max_concurrency INTEGER DEFAULT 2,
			global_rpm INTEGER DEFAULT 0,
			test_model TEXT DEFAULT 'gpt-5.4',
			test_concurrency INTEGER DEFAULT 50,
			proxy_url TEXT DEFAULT '',
			pg_max_conns INTEGER DEFAULT 50,
			redis_pool_size INTEGER DEFAULT 30,
			auto_clean_unauthorized INTEGER DEFAULT 0,
			auto_clean_rate_limited INTEGER DEFAULT 0,
			admin_secret TEXT DEFAULT '',
			auto_clean_full_usage INTEGER DEFAULT 0,
			proxy_pool_enabled INTEGER DEFAULT 0,
			fast_scheduler_enabled INTEGER DEFAULT 0,
			max_retries INTEGER DEFAULT 2,
			allow_remote_migration INTEGER DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS usage_stats_baseline (
			id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			total_requests INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			label TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			test_ip TEXT DEFAULT '',
			test_location TEXT DEFAULT '',
			test_latency_ms INTEGER DEFAULT 0
		);`,
	}
	for _, stmt := range statements {
		if _, err := db.conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if _, err := db.conn.ExecContext(ctx, `INSERT OR IGNORE INTO usage_stats_baseline (id) VALUES (1)`); err != nil {
		return err
	}

	columns := []struct {
		table string
		name  string
		def   string
	}{
		{"accounts", "cooldown_reason", "TEXT DEFAULT ''"},
		{"accounts", "cooldown_until", "TIMESTAMP NULL"},
		{"usage_logs", "input_tokens", "INTEGER DEFAULT 0"},
		{"usage_logs", "output_tokens", "INTEGER DEFAULT 0"},
		{"usage_logs", "reasoning_tokens", "INTEGER DEFAULT 0"},
		{"usage_logs", "first_token_ms", "INTEGER DEFAULT 0"},
		{"usage_logs", "reasoning_effort", "TEXT DEFAULT ''"},
		{"usage_logs", "inbound_endpoint", "TEXT DEFAULT ''"},
		{"usage_logs", "upstream_endpoint", "TEXT DEFAULT ''"},
		{"usage_logs", "stream", "INTEGER DEFAULT 0"},
		{"usage_logs", "cached_tokens", "INTEGER DEFAULT 0"},
		{"usage_logs", "service_tier", "TEXT DEFAULT ''"},
		{"system_settings", "pg_max_conns", "INTEGER DEFAULT 50"},
		{"system_settings", "redis_pool_size", "INTEGER DEFAULT 30"},
		{"system_settings", "auto_clean_unauthorized", "INTEGER DEFAULT 0"},
		{"system_settings", "auto_clean_rate_limited", "INTEGER DEFAULT 0"},
		{"system_settings", "admin_secret", "TEXT DEFAULT ''"},
		{"system_settings", "auto_clean_full_usage", "INTEGER DEFAULT 0"},
		{"system_settings", "proxy_pool_enabled", "INTEGER DEFAULT 0"},
		{"system_settings", "fast_scheduler_enabled", "INTEGER DEFAULT 0"},
		{"system_settings", "max_retries", "INTEGER DEFAULT 2"},
		{"system_settings", "allow_remote_migration", "INTEGER DEFAULT 0"},
		{"proxies", "test_ip", "TEXT DEFAULT ''"},
		{"proxies", "test_location", "TEXT DEFAULT ''"},
		{"proxies", "test_latency_ms", "INTEGER DEFAULT 0"},
	}
	for _, column := range columns {
		if err := db.ensureSQLiteColumn(ctx, column.table, column.name, column.def); err != nil {
			return err
		}
	}

	indexStatements := []string{
		`CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts(status);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_platform ON accounts(platform);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_cooldown_until ON accounts(cooldown_until);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_logs_created_at ON usage_logs(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_logs_account_id ON usage_logs(account_id);`,
		`CREATE INDEX IF NOT EXISTS idx_usage_logs_created_status ON usage_logs(created_at, status_code);`,
	}
	for _, stmt := range indexStatements {
		if _, err := db.conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

func (db *DB) ensureSQLiteColumn(ctx context.Context, table string, name string, columnDef string) error {
	columns, err := db.sqliteTableColumns(ctx, table)
	if err != nil {
		return err
	}
	if _, ok := columns[name]; ok {
		return nil
	}
	_, err = db.conn.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, columnDef))
	return err
}

func (db *DB) sqliteTableColumns(ctx context.Context, table string) (map[string]struct{}, error) {
	rows, err := db.conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		result[name] = struct{}{}
	}
	return result, rows.Err()
}

func (db *DB) getTrafficSnapshotSQLite(ctx context.Context) (*TrafficSnapshot, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT created_at, total_tokens
		FROM usage_logs
		WHERE created_at >= $1
	`, time.Now().Add(-5*time.Minute))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	perSecondRequests := make(map[int64]float64)
	perSecondTokens := make(map[int64]float64)
	now := time.Now()
	windowStart := now.Add(-10 * time.Second).Unix()

	for rows.Next() {
		var createdRaw interface{}
		var totalTokens int64
		if err := rows.Scan(&createdRaw, &totalTokens); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil || createdAt.IsZero() {
			continue
		}
		sec := createdAt.Unix()
		perSecondRequests[sec]++
		perSecondTokens[sec] += float64(totalTokens)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := &TrafficSnapshot{}
	var qpsPeak float64
	var tpsPeak float64
	var qpsWindow float64
	var tpsWindow float64
	for sec, reqCount := range perSecondRequests {
		if reqCount > qpsPeak {
			qpsPeak = reqCount
		}
		tokenCount := perSecondTokens[sec]
		if tokenCount > tpsPeak {
			tpsPeak = tokenCount
		}
		if sec >= windowStart {
			qpsWindow += reqCount
			tpsWindow += tokenCount
		}
	}
	result.QPS = qpsWindow / 10.0
	result.TPS = tpsWindow / 10.0
	result.QPSPeak = qpsPeak
	result.TPSPeak = tpsPeak
	return result, nil
}

func (db *DB) getChartAggregationSQLite(ctx context.Context, start, end time.Time, bucketMinutes int) (*ChartAggregation, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT created_at, duration_ms, input_tokens, output_tokens, reasoning_tokens, cached_tokens, model
		FROM usage_logs
		WHERE created_at >= $1 AND created_at <= $2
		  AND status_code <> 499
	`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type bucketAgg struct {
		requests        int64
		totalLatency    float64
		inputTokens     int64
		outputTokens    int64
		reasoningTokens int64
		cachedTokens    int64
	}

	result := &ChartAggregation{}
	timelineMap := make(map[string]*bucketAgg)
	modelMap := make(map[string]int64)

	for rows.Next() {
		var createdRaw interface{}
		var durationMs int
		var inputTokens int64
		var outputTokens int64
		var reasoningTokens int64
		var cachedTokens int64
		var model sql.NullString
		if err := rows.Scan(&createdRaw, &durationMs, &inputTokens, &outputTokens, &reasoningTokens, &cachedTokens, &model); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil || createdAt.IsZero() {
			continue
		}

		bucket := createdAt.Truncate(time.Duration(bucketMinutes) * time.Minute).Format("2006-01-02T15:04:05")
		agg, ok := timelineMap[bucket]
		if !ok {
			agg = &bucketAgg{}
			timelineMap[bucket] = agg
		}
		agg.requests++
		agg.totalLatency += float64(durationMs)
		agg.inputTokens += inputTokens
		agg.outputTokens += outputTokens
		agg.reasoningTokens += reasoningTokens
		agg.cachedTokens += cachedTokens

		modelName := "unknown"
		if model.Valid && model.String != "" {
			modelName = model.String
		}
		modelMap[modelName]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(timelineMap))
	for key := range timelineMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		agg := timelineMap[key]
		avgLatency := 0.0
		if agg.requests > 0 {
			avgLatency = agg.totalLatency / float64(agg.requests)
		}
		result.Timeline = append(result.Timeline, ChartTimelinePoint{
			Bucket:          key,
			Requests:        agg.requests,
			AvgLatency:      avgLatency,
			InputTokens:     agg.inputTokens,
			OutputTokens:    agg.outputTokens,
			ReasoningTokens: agg.reasoningTokens,
			CachedTokens:    agg.cachedTokens,
		})
	}
	if result.Timeline == nil {
		result.Timeline = []ChartTimelinePoint{}
	}

	type modelAgg struct {
		model    string
		requests int64
	}
	models := make([]modelAgg, 0, len(modelMap))
	for model, requests := range modelMap {
		models = append(models, modelAgg{model: model, requests: requests})
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].requests == models[j].requests {
			return models[i].model < models[j].model
		}
		return models[i].requests > models[j].requests
	})
	if len(models) > 10 {
		models = models[:10]
	}
	for _, model := range models {
		result.Models = append(result.Models, ChartModelPoint{
			Model:    model.model,
			Requests: model.requests,
		})
	}
	if result.Models == nil {
		result.Models = []ChartModelPoint{}
	}

	return result, nil
}

func (db *DB) sqliteBoolExpr(column string) string {
	return fmt.Sprintf("CASE WHEN %s <> 0 THEN 1 ELSE 0 END", column)
}

func (db *DB) sqliteJSONExtract(column string, key string) string {
	return fmt.Sprintf("json_extract(%s, '$.%s')", column, key)
}

// ListAPIKeys 获取所有 API 密钥（SQLite）
func (db *DB) listAPIKeysSQLite(ctx context.Context) ([]*APIKeyRow, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT id, name, key, created_at FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*APIKeyRow
	for rows.Next() {
		k := &APIKeyRow{}
		var createdRaw interface{}
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &createdRaw); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, err
		}
		k.CreatedAt = createdAt
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (db *DB) insertAPIKeySQLite(ctx context.Context, name, key string) (int64, error) {
	res, err := db.conn.ExecContext(ctx, `INSERT INTO api_keys (name, key) VALUES (?, ?)`, name, key)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) deleteAPIKeySQLite(ctx context.Context, id int64) error {
	_, err := db.conn.ExecContext(ctx, `DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

func (db *DB) getAllAPIKeyValuesSQLite(ctx context.Context) ([]string, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT key FROM api_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (db *DB) getSystemSettingsSQLite(ctx context.Context) (*SystemSettings, error) {
	s := &SystemSettings{}

	var (
		autoCleanUnauthorized int
		autoCleanRateLimited  int
		autoCleanFullUsage    int
		proxyPoolEnabled      int
		fastSchedulerEnabled  int
		allowRemoteMigration  int
	)

	err := db.conn.QueryRowContext(ctx, `
		SELECT max_concurrency, global_rpm, test_model, test_concurrency, proxy_url, pg_max_conns, redis_pool_size,
		       COALESCE(auto_clean_unauthorized, 0), COALESCE(auto_clean_rate_limited, 0), COALESCE(admin_secret, ''),
		       COALESCE(auto_clean_full_usage, 0), COALESCE(proxy_pool_enabled, 0),
		       COALESCE(fast_scheduler_enabled, 0), COALESCE(max_retries, 2), COALESCE(allow_remote_migration, 0)
		FROM system_settings WHERE id = 1
	`).Scan(
		&s.MaxConcurrency, &s.GlobalRPM, &s.TestModel, &s.TestConcurrency, &s.ProxyURL, &s.PgMaxConns, &s.RedisPoolSize,
		&autoCleanUnauthorized, &autoCleanRateLimited, &s.AdminSecret,
		&autoCleanFullUsage, &proxyPoolEnabled,
		&fastSchedulerEnabled, &s.MaxRetries, &allowRemoteMigration,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	s.AutoCleanUnauthorized = autoCleanUnauthorized != 0
	s.AutoCleanRateLimited = autoCleanRateLimited != 0
	s.AutoCleanFullUsage = autoCleanFullUsage != 0
	s.ProxyPoolEnabled = proxyPoolEnabled != 0
	s.FastSchedulerEnabled = fastSchedulerEnabled != 0
	s.AllowRemoteMigration = allowRemoteMigration != 0

	return s, nil
}

func boolToSQLiteInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (db *DB) updateSystemSettingsSQLite(ctx context.Context, s *SystemSettings) error {
	_, err := db.conn.ExecContext(ctx, `
		INSERT INTO system_settings (
			id, max_concurrency, global_rpm, test_model, test_concurrency, proxy_url, pg_max_conns, redis_pool_size,
			auto_clean_unauthorized, auto_clean_rate_limited, admin_secret, auto_clean_full_usage, proxy_pool_enabled,
			fast_scheduler_enabled, max_retries, allow_remote_migration
		)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			max_concurrency = excluded.max_concurrency,
			global_rpm = excluded.global_rpm,
			test_model = excluded.test_model,
			test_concurrency = excluded.test_concurrency,
			proxy_url = excluded.proxy_url,
			pg_max_conns = excluded.pg_max_conns,
			redis_pool_size = excluded.redis_pool_size,
			auto_clean_unauthorized = excluded.auto_clean_unauthorized,
			auto_clean_rate_limited = excluded.auto_clean_rate_limited,
			admin_secret = excluded.admin_secret,
			auto_clean_full_usage = excluded.auto_clean_full_usage,
			proxy_pool_enabled = excluded.proxy_pool_enabled,
			fast_scheduler_enabled = excluded.fast_scheduler_enabled,
			max_retries = excluded.max_retries,
			allow_remote_migration = excluded.allow_remote_migration
	`,
		s.MaxConcurrency,
		s.GlobalRPM,
		s.TestModel,
		s.TestConcurrency,
		s.ProxyURL,
		s.PgMaxConns,
		s.RedisPoolSize,
		boolToSQLiteInt(s.AutoCleanUnauthorized),
		boolToSQLiteInt(s.AutoCleanRateLimited),
		s.AdminSecret,
		boolToSQLiteInt(s.AutoCleanFullUsage),
		boolToSQLiteInt(s.ProxyPoolEnabled),
		boolToSQLiteInt(s.FastSchedulerEnabled),
		s.MaxRetries,
		boolToSQLiteInt(s.AllowRemoteMigration),
	)
	return err
}

func (db *DB) listProxiesSQLite(ctx context.Context) ([]*ProxyRow, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT id, url, label, enabled, created_at, COALESCE(test_ip,''), COALESCE(test_location,''), COALESCE(test_latency_ms,0) FROM proxies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proxies []*ProxyRow
	for rows.Next() {
		p := &ProxyRow{}
		var enabledInt int
		var createdRaw interface{}
		if err := rows.Scan(&p.ID, &p.URL, &p.Label, &enabledInt, &createdRaw, &p.TestIP, &p.TestLocation, &p.TestLatencyMs); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, err
		}
		p.CreatedAt = createdAt
		p.Enabled = enabledInt != 0
		proxies = append(proxies, p)
	}
	return proxies, rows.Err()
}

func (db *DB) listEnabledProxiesSQLite(ctx context.Context) ([]*ProxyRow, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT id, url, label, enabled, created_at, COALESCE(test_ip,''), COALESCE(test_location,''), COALESCE(test_latency_ms,0) FROM proxies WHERE enabled <> 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proxies []*ProxyRow
	for rows.Next() {
		p := &ProxyRow{}
		var enabledInt int
		var createdRaw interface{}
		if err := rows.Scan(&p.ID, &p.URL, &p.Label, &enabledInt, &createdRaw, &p.TestIP, &p.TestLocation, &p.TestLatencyMs); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, err
		}
		p.CreatedAt = createdAt
		p.Enabled = enabledInt != 0
		proxies = append(proxies, p)
	}
	return proxies, rows.Err()
}

func (db *DB) insertProxySQLite(ctx context.Context, url, label string) (int64, error) {
	res, err := db.conn.ExecContext(ctx, `INSERT OR IGNORE INTO proxies (url, label) VALUES (?, ?)`, url, label)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return 0, sql.ErrNoRows
	}
	return res.LastInsertId()
}

func (db *DB) insertProxiesSQLite(ctx context.Context, urls []string, label string) (int, error) {
	inserted := 0
	for _, u := range urls {
		res, err := db.conn.ExecContext(ctx, `INSERT OR IGNORE INTO proxies (url, label) VALUES (?, ?)`, u, label)
		if err != nil {
			continue
		}
		affected, err := res.RowsAffected()
		if err == nil && affected > 0 {
			inserted += int(affected)
		}
	}
	return inserted, nil
}

func (db *DB) deleteProxySQLite(ctx context.Context, id int64) error {
	_, err := db.conn.ExecContext(ctx, `DELETE FROM proxies WHERE id = ?`, id)
	return err
}

func (db *DB) deleteProxiesSQLite(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM proxies WHERE id IN (%s)", strings.Join(placeholders, ","))
	res, err := db.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

func (db *DB) updateProxySQLite(ctx context.Context, id int64, label *string, enabled *bool) error {
	if label != nil {
		if _, err := db.conn.ExecContext(ctx, `UPDATE proxies SET label = ? WHERE id = ?`, *label, id); err != nil {
			return err
		}
	}
	if enabled != nil {
		if _, err := db.conn.ExecContext(ctx, `UPDATE proxies SET enabled = ? WHERE id = ?`, boolToSQLiteInt(*enabled), id); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) updateProxyTestResultSQLite(ctx context.Context, id int64, ip, location string, latencyMs int) error {
	_, err := db.conn.ExecContext(ctx,
		`UPDATE proxies SET test_ip = ?, test_location = ?, test_latency_ms = ? WHERE id = ?`,
		ip, location, latencyMs, id)
	return err
}

func (db *DB) insertUsageLogSQLite(ctx context.Context, logInput *UsageLogInput) error {
	if logInput == nil {
		return nil
	}
	_, err := db.conn.ExecContext(ctx,
		`INSERT INTO usage_logs (account_id, endpoint, model, prompt_tokens, completion_tokens, total_tokens, status_code, duration_ms,
		  input_tokens, output_tokens, reasoning_tokens, first_token_ms, reasoning_effort, inbound_endpoint, upstream_endpoint, stream, cached_tokens, service_tier)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		logInput.AccountID,
		logInput.Endpoint,
		logInput.Model,
		logInput.PromptTokens,
		logInput.CompletionTokens,
		logInput.TotalTokens,
		logInput.StatusCode,
		logInput.DurationMs,
		logInput.InputTokens,
		logInput.OutputTokens,
		logInput.ReasoningTokens,
		logInput.FirstTokenMs,
		logInput.ReasoningEffort,
		logInput.InboundEndpoint,
		logInput.UpstreamEndpoint,
		boolToSQLiteInt(logInput.Stream),
		logInput.CachedTokens,
		logInput.ServiceTier,
	)
	return err
}

func (db *DB) getUsageStatsSQLite(ctx context.Context) (*UsageStats, error) {
	stats := &UsageStats{}

	todayQuery := `
	SELECT
		COUNT(*) AS today_requests,
		COALESCE(SUM(total_tokens), 0) AS today_tokens,
		COALESCE(SUM(prompt_tokens), 0) AS today_prompt,
		COALESCE(SUM(completion_tokens), 0) AS today_completion,
		COALESCE(SUM(cached_tokens), 0) AS today_cached,
		SUM(CASE WHEN created_at >= datetime('now', '-1 minute') THEN 1 ELSE 0 END) AS rpm,
		COALESCE(SUM(CASE WHEN created_at >= datetime('now', '-1 minute') THEN total_tokens ELSE 0 END), 0) AS tpm,
		COALESCE(AVG(duration_ms), 0) AS avg_duration_ms,
		SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) AS today_errors
	FROM usage_logs
	WHERE created_at >= date('now')
	  AND status_code <> 499
	`

	var todayErrors int64
	err := db.conn.QueryRowContext(ctx, todayQuery).Scan(
		&stats.TodayRequests, &stats.TodayTokens, &stats.TotalPrompt, &stats.TotalCompletion, &stats.TotalCachedTokens,
		&stats.RPM, &stats.TPM,
		&stats.AvgDurationMs,
		&todayErrors,
	)
	if err != nil {
		return nil, err
	}

	var visibleTotal int64
	_ = db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE status_code <> 499`).Scan(&visibleTotal)

	var bReq, bTok, bPrompt, bComp, bCached int64
	_ = db.conn.QueryRowContext(ctx, `
		SELECT total_requests, total_tokens, prompt_tokens, completion_tokens, cached_tokens
		FROM usage_stats_baseline WHERE id = 1
	`).Scan(&bReq, &bTok, &bPrompt, &bComp, &bCached)

	stats.TotalRequests = visibleTotal + bReq
	stats.TotalTokens = stats.TodayTokens + bTok
	stats.TotalPrompt += bPrompt
	stats.TotalCompletion += bComp
	stats.TotalCachedTokens += bCached

	if stats.TodayRequests > 0 {
		stats.ErrorRate = float64(todayErrors) / float64(stats.TodayRequests) * 100
	}

	return stats, nil
}

func (db *DB) listRecentUsageLogsSQLite(ctx context.Context, limit int) ([]*UsageLog, error) {
	if limit <= 0 || limit > 5000 {
		limit = 50
	}

	query := `SELECT u.id, u.account_id, u.endpoint, u.model, u.prompt_tokens, u.completion_tokens, u.total_tokens, u.status_code, u.duration_ms,
	            COALESCE(u.input_tokens, 0), COALESCE(u.output_tokens, 0), COALESCE(u.reasoning_tokens, 0),
	            COALESCE(u.first_token_ms, 0), COALESCE(u.reasoning_effort, ''), COALESCE(u.inbound_endpoint, ''),
	            COALESCE(u.upstream_endpoint, ''), COALESCE(u.stream, 0), COALESCE(u.cached_tokens, 0), COALESCE(u.service_tier, ''),
	            COALESCE(` + db.sqliteJSONExtract("a.credentials", "email") + `, ''), u.created_at
	           FROM usage_logs u
	           LEFT JOIN accounts a ON u.account_id = a.id
	           WHERE u.status_code <> 499
	           ORDER BY u.id DESC LIMIT ?`

	rows, err := db.conn.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*UsageLog
	for rows.Next() {
		l := &UsageLog{}
		var streamInt int
		var createdRaw interface{}
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Endpoint, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.StatusCode, &l.DurationMs,
			&l.InputTokens, &l.OutputTokens, &l.ReasoningTokens, &l.FirstTokenMs, &l.ReasoningEffort, &l.InboundEndpoint, &l.UpstreamEndpoint, &streamInt, &l.CachedTokens, &l.ServiceTier,
			&l.AccountEmail, &createdRaw); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, err
		}
		l.Stream = streamInt != 0
		l.CreatedAt = createdAt
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func (db *DB) listUsageLogsByTimeRangeSQLite(ctx context.Context, start, end time.Time) ([]*UsageLog, error) {
	query := `SELECT u.id, u.account_id, u.endpoint, u.model, u.prompt_tokens, u.completion_tokens, u.total_tokens, u.status_code, u.duration_ms,
	            COALESCE(u.input_tokens, 0), COALESCE(u.output_tokens, 0), COALESCE(u.reasoning_tokens, 0),
	            COALESCE(u.first_token_ms, 0), COALESCE(u.reasoning_effort, ''), COALESCE(u.inbound_endpoint, ''),
	            COALESCE(u.upstream_endpoint, ''), COALESCE(u.stream, 0), COALESCE(u.cached_tokens, 0), COALESCE(u.service_tier, ''),
	            COALESCE(` + db.sqliteJSONExtract("a.credentials", "email") + `, ''), u.created_at
	           FROM usage_logs u
	           LEFT JOIN accounts a ON u.account_id = a.id
	           WHERE u.created_at >= ? AND u.created_at <= ?
	             AND u.status_code <> 499
	           ORDER BY u.created_at ASC`

	rows, err := db.conn.QueryContext(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*UsageLog
	for rows.Next() {
		l := &UsageLog{}
		var streamInt int
		var createdRaw interface{}
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Endpoint, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.StatusCode, &l.DurationMs,
			&l.InputTokens, &l.OutputTokens, &l.ReasoningTokens, &l.FirstTokenMs, &l.ReasoningEffort, &l.InboundEndpoint, &l.UpstreamEndpoint, &streamInt, &l.CachedTokens, &l.ServiceTier,
			&l.AccountEmail, &createdRaw); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, err
		}
		l.Stream = streamInt != 0
		l.CreatedAt = createdAt
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func (db *DB) listUsageLogsByTimeRangePagedSQLite(ctx context.Context, f UsageLogFilter) (*UsageLogPage, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 20
	}

	where := `u.created_at >= ? AND u.created_at <= ? AND u.status_code <> 499`
	args := []interface{}{f.Start, f.End}

	if f.Email != "" {
		where += ` AND LOWER(COALESCE(` + db.sqliteJSONExtract("a.credentials", "email") + `, '')) LIKE ?`
		args = append(args, "%"+strings.ToLower(f.Email)+"%")
	}
	if f.Model != "" {
		where += ` AND u.model = ?`
		args = append(args, f.Model)
	}
	if f.Endpoint != "" {
		where += ` AND u.inbound_endpoint = ?`
		args = append(args, f.Endpoint)
	}
	if f.FastOnly != nil {
		if *f.FastOnly {
			where += ` AND COALESCE(u.service_tier, '') = 'fast'`
		} else {
			where += ` AND COALESCE(u.service_tier, '') <> 'fast'`
		}
	}
	if f.StreamOnly != nil {
		where += ` AND ` + db.sqliteBoolExpr("COALESCE(u.stream, 0)") + ` = ?`
		args = append(args, boolToSQLiteInt(*f.StreamOnly))
	}

	countQuery := `SELECT COUNT(*) FROM usage_logs u LEFT JOIN accounts a ON u.account_id = a.id WHERE ` + where
	var total int64
	if err := db.conn.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (f.Page - 1) * f.PageSize
	query := `SELECT u.id, u.account_id, u.endpoint, u.model, u.prompt_tokens, u.completion_tokens, u.total_tokens, u.status_code, u.duration_ms,
	            COALESCE(u.input_tokens, 0), COALESCE(u.output_tokens, 0), COALESCE(u.reasoning_tokens, 0),
	            COALESCE(u.first_token_ms, 0), COALESCE(u.reasoning_effort, ''), COALESCE(u.inbound_endpoint, ''),
	            COALESCE(u.upstream_endpoint, ''), COALESCE(u.stream, 0), COALESCE(u.cached_tokens, 0), COALESCE(u.service_tier, ''),
	            COALESCE(` + db.sqliteJSONExtract("a.credentials", "email") + `, ''), u.created_at
	           FROM usage_logs u
	           LEFT JOIN accounts a ON u.account_id = a.id
	           WHERE ` + where + `
	           ORDER BY u.created_at DESC
	           LIMIT ? OFFSET ?`

	argsWithPage := append(append([]interface{}{}, args...), f.PageSize, offset)
	rows, err := db.conn.QueryContext(ctx, query, argsWithPage...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &UsageLogPage{Total: total}
	for rows.Next() {
		l := &UsageLog{}
		var streamInt int
		var createdRaw interface{}
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Endpoint, &l.Model, &l.PromptTokens, &l.CompletionTokens, &l.TotalTokens, &l.StatusCode, &l.DurationMs,
			&l.InputTokens, &l.OutputTokens, &l.ReasoningTokens, &l.FirstTokenMs, &l.ReasoningEffort, &l.InboundEndpoint, &l.UpstreamEndpoint, &streamInt, &l.CachedTokens,
			&l.ServiceTier, &l.AccountEmail, &createdRaw); err != nil {
			return nil, err
		}
		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, err
		}
		l.Stream = streamInt != 0
		l.CreatedAt = createdAt
		result.Logs = append(result.Logs, l)
	}
	if result.Logs == nil {
		result.Logs = []*UsageLog{}
	}
	return result, rows.Err()
}

func (db *DB) clearUsageLogsSQLite(ctx context.Context) error {
	_, err := db.conn.ExecContext(ctx, `
		UPDATE usage_stats_baseline SET
			total_requests = total_requests + COALESCE((SELECT COUNT(*) FROM usage_logs WHERE status_code <> 499), 0),
			total_tokens = total_tokens + COALESCE((SELECT SUM(total_tokens) FROM usage_logs WHERE status_code <> 499), 0),
			prompt_tokens = prompt_tokens + COALESCE((SELECT SUM(prompt_tokens) FROM usage_logs WHERE status_code <> 499), 0),
			completion_tokens = completion_tokens + COALESCE((SELECT SUM(completion_tokens) FROM usage_logs WHERE status_code <> 499), 0),
			cached_tokens = cached_tokens + COALESCE((SELECT SUM(cached_tokens) FROM usage_logs WHERE status_code <> 499), 0)
		WHERE id = 1
	`)
	if err != nil {
		return fmt.Errorf("快照统计基线失败: %w", err)
	}

	_, err = db.conn.ExecContext(ctx, `DELETE FROM usage_logs`)
	if err != nil {
		return err
	}
	_, _ = db.conn.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name = 'usage_logs'`)
	return nil
}

func (db *DB) getAccountRequestCountsSQLite(ctx context.Context) (map[int64]*AccountRequestCount, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT account_id,
		       SUM(CASE WHEN status_code < 400 THEN 1 ELSE 0 END) AS success_count,
		       SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END) AS error_count
		FROM usage_logs
		GROUP BY account_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]*AccountRequestCount)
	for rows.Next() {
		rc := &AccountRequestCount{}
		if err := rows.Scan(&rc.AccountID, &rc.SuccessCount, &rc.ErrorCount); err != nil {
			return nil, err
		}
		result[rc.AccountID] = rc
	}
	return result, rows.Err()
}

func (db *DB) listActiveSQLite(ctx context.Context) ([]*AccountRow, error) {
	rows, err := db.conn.QueryContext(ctx, `
		SELECT id, name, platform, type, credentials, proxy_url, status, cooldown_reason, cooldown_until, error_message, created_at, updated_at
		FROM accounts
		WHERE status = 'active'
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("查询账号失败: %w", err)
	}
	defer rows.Close()

	var accounts []*AccountRow
	for rows.Next() {
		a := &AccountRow{}
		var (
			credRaw          interface{}
			cooldownUntilRaw interface{}
			createdRaw       interface{}
			updatedRaw       interface{}
		)
		if err := rows.Scan(
			&a.ID,
			&a.Name,
			&a.Platform,
			&a.Type,
			&credRaw,
			&a.ProxyURL,
			&a.Status,
			&a.CooldownReason,
			&cooldownUntilRaw,
			&a.ErrorMessage,
			&createdRaw,
			&updatedRaw,
		); err != nil {
			return nil, fmt.Errorf("扫描账号行失败: %w", err)
		}

		a.Credentials = decodeCredentials(credRaw)
		db.decryptCredentialMapInPlace(a.ID, a.Credentials)

		cooldownUntil, err := parseDBNullTimeValue(cooldownUntilRaw)
		if err != nil {
			return nil, fmt.Errorf("解析 cooldown_until 失败: %w", err)
		}
		a.CooldownUntil = cooldownUntil

		createdAt, err := parseDBTimeValue(createdRaw)
		if err != nil {
			return nil, fmt.Errorf("解析 created_at 失败: %w", err)
		}
		a.CreatedAt = createdAt

		updatedAt, err := parseDBTimeValue(updatedRaw)
		if err != nil {
			return nil, fmt.Errorf("解析 updated_at 失败: %w", err)
		}
		a.UpdatedAt = updatedAt

		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (db *DB) updateCredentialsSQLite(ctx context.Context, id int64, credentials map[string]interface{}) error {
	var currentRaw interface{}
	if err := db.conn.QueryRowContext(ctx, `SELECT credentials FROM accounts WHERE id = ?`, id).Scan(&currentRaw); err != nil {
		return err
	}
	current := decodeCredentials(currentRaw)

	encCredentials, _, err := db.encryptCredentialMap(credentials)
	if err != nil {
		return fmt.Errorf("加密 credentials 失败: %w", err)
	}

	merged := mergeCredentialMaps(current, encCredentials)
	credJSON, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("序列化 credentials 失败: %w", err)
	}

	_, err = db.conn.ExecContext(ctx, `UPDATE accounts SET credentials = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, string(credJSON), id)
	return err
}

func (db *DB) setErrorSQLite(ctx context.Context, id int64, errorMsg string) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE accounts SET status = 'error', error_message = ?, cooldown_reason = '', cooldown_until = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, errorMsg, id)
	return err
}

func (db *DB) clearErrorSQLite(ctx context.Context, id int64) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE accounts SET status = 'active', error_message = '', cooldown_reason = '', cooldown_until = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (db *DB) setCooldownSQLite(ctx context.Context, id int64, reason string, until time.Time) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE accounts SET cooldown_reason = ?, cooldown_until = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, reason, until, id)
	return err
}

func (db *DB) clearCooldownSQLite(ctx context.Context, id int64) error {
	_, err := db.conn.ExecContext(ctx, `UPDATE accounts SET cooldown_reason = '', cooldown_until = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (db *DB) insertAccountSQLite(ctx context.Context, name string, refreshToken string, proxyURL string) (int64, error) {
	credentials := map[string]interface{}{
		"refresh_token": refreshToken,
	}
	encryptedCredentials, _, err := db.encryptCredentialMap(credentials)
	if err != nil {
		return 0, fmt.Errorf("加密 refresh_token 失败: %w", err)
	}

	credJSON, err := json.Marshal(encryptedCredentials)
	if err != nil {
		return 0, err
	}

	res, err := db.conn.ExecContext(ctx, `INSERT INTO accounts (name, credentials, proxy_url) VALUES (?, ?, ?)`, name, string(credJSON), proxyURL)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) countAllSQLite(ctx context.Context) (int, error) {
	var count int
	err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count)
	return count, err
}

func (db *DB) getAllRefreshTokensSQLite(ctx context.Context) (map[string]bool, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT id, credentials FROM accounts WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var id int64
		var credRaw interface{}
		if err := rows.Scan(&id, &credRaw); err != nil {
			return nil, err
		}

		credMap := decodeCredentials(credRaw)
		db.decryptCredentialMapInPlace(id, credMap)

		if rt, ok := credMap["refresh_token"].(string); ok && rt != "" {
			result[rt] = true
		}
	}
	return result, rows.Err()
}
