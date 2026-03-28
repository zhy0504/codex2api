package database

import (
	"path/filepath"
	"testing"
)

func TestNewSQLiteInitializesFreshDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "codex2api.db")

	db, err := New("sqlite", dbPath)
	if err != nil {
		t.Fatalf("New(sqlite) 返回错误: %v", err)
	}
	defer db.Close()

	if got := db.Driver(); got != "sqlite" {
		t.Fatalf("Driver() = %q, want %q", got, "sqlite")
	}

	settings, err := db.GetSystemSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSystemSettings() 返回错误: %v", err)
	}
	if settings != nil {
		t.Fatalf("fresh sqlite db should not have settings row yet")
	}

	if err := db.UpdateSystemSettings(t.Context(), &SystemSettings{
		MaxConcurrency:       3,
		GlobalRPM:            120,
		TestModel:            "gpt-5.4",
		TestConcurrency:      20,
		ProxyURL:             "",
		PgMaxConns:           50,
		RedisPoolSize:        20,
		FastSchedulerEnabled: true,
		MaxRetries:           4,
		AllowRemoteMigration: true,
	}); err != nil {
		t.Fatalf("UpdateSystemSettings() 返回错误: %v", err)
	}

	settings, err = db.GetSystemSettings(t.Context())
	if err != nil {
		t.Fatalf("GetSystemSettings() 返回错误: %v", err)
	}
	if settings == nil {
		t.Fatalf("settings should exist after upsert")
	}
	if !settings.FastSchedulerEnabled || settings.MaxRetries != 4 || !settings.AllowRemoteMigration {
		t.Fatalf("settings mismatch: %+v", settings)
	}
}
