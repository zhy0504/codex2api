package cache

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type memoryTokenEntry struct {
	token     string
	expiresAt time.Time
}

type memoryOAuthSessionEntry struct {
	payload   string
	expiresAt time.Time
}

// MemoryTokenCache 为单机轻量部署提供进程内 token 缓存和刷新锁。
// 重启后缓存丢失属于预期行为。
type MemoryTokenCache struct {
	mu       sync.RWMutex
	tokens   map[int64]memoryTokenEntry
	locks    map[int64]time.Time
	oauth    map[string]memoryOAuthSessionEntry
	poolSize int
}

// NewMemory 创建内存缓存实现。
func NewMemory(poolSize int) TokenCache {
	if poolSize <= 0 {
		poolSize = 1
	}
	return &MemoryTokenCache{
		tokens:   make(map[int64]memoryTokenEntry),
		locks:    make(map[int64]time.Time),
		oauth:    make(map[string]memoryOAuthSessionEntry),
		poolSize: poolSize,
	}
}

func (tc *MemoryTokenCache) Driver() string {
	return "memory"
}

func (tc *MemoryTokenCache) Label() string {
	return "Memory"
}

func (tc *MemoryTokenCache) Close() error {
	return nil
}

func (tc *MemoryTokenCache) Ping(ctx context.Context) error {
	return nil
}

func (tc *MemoryTokenCache) Stats() PoolStats {
	return PoolStats{
		TotalConns: 1,
		IdleConns:  1,
		StaleConns: 0,
	}
}

func (tc *MemoryTokenCache) PoolSize() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.poolSize
}

func (tc *MemoryTokenCache) SetPoolSize(n int) {
	if n <= 0 {
		n = 1
	}
	tc.mu.Lock()
	tc.poolSize = n
	tc.mu.Unlock()
}

func (tc *MemoryTokenCache) GetAccessToken(ctx context.Context, accountID int64) (string, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	entry, ok := tc.tokens[accountID]
	if !ok {
		return "", nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(tc.tokens, accountID)
		return "", nil
	}
	return entry.token, nil
}

func (tc *MemoryTokenCache) SetAccessToken(ctx context.Context, accountID int64, token string, ttl time.Duration) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	expiresAt := time.Time{}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	tc.tokens[accountID] = memoryTokenEntry{
		token:     token,
		expiresAt: expiresAt,
	}
	return nil
}

func (tc *MemoryTokenCache) DeleteAccessToken(ctx context.Context, accountID int64) error {
	tc.mu.Lock()
	delete(tc.tokens, accountID)
	tc.mu.Unlock()
	return nil
}

func (tc *MemoryTokenCache) AcquireRefreshLock(ctx context.Context, accountID int64, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()

	if until, ok := tc.locks[accountID]; ok && time.Now().Before(until) {
		return false, nil
	}
	tc.locks[accountID] = time.Now().Add(ttl)
	return true, nil
}

func (tc *MemoryTokenCache) ReleaseRefreshLock(ctx context.Context, accountID int64) error {
	tc.mu.Lock()
	delete(tc.locks, accountID)
	tc.mu.Unlock()
	return nil
}

func (tc *MemoryTokenCache) WaitForRefreshComplete(ctx context.Context, accountID int64, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tc.mu.Lock()
		lockUntil, locked := tc.locks[accountID]
		if locked && time.Now().After(lockUntil) {
			delete(tc.locks, accountID)
			locked = false
		}
		entry, hasToken := tc.tokens[accountID]
		if hasToken && !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
			delete(tc.tokens, accountID)
			hasToken = false
			entry = memoryTokenEntry{}
		}
		tc.mu.Unlock()

		if !locked && hasToken && entry.token != "" {
			return entry.token, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}

	return "", fmt.Errorf("等待刷新超时")
}

func (tc *MemoryTokenCache) SetOAuthSession(ctx context.Context, sessionID, payload string, ttl time.Duration) error {
	if sessionID == "" {
		return fmt.Errorf("sessionID 不能为空")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	tc.mu.Lock()
	tc.oauth[sessionID] = memoryOAuthSessionEntry{
		payload:   payload,
		expiresAt: time.Now().Add(ttl),
	}
	tc.mu.Unlock()
	return nil
}

func (tc *MemoryTokenCache) GetOAuthSession(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}

	tc.mu.Lock()
	entry, ok := tc.oauth[sessionID]
	if !ok {
		tc.mu.Unlock()
		return "", nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(tc.oauth, sessionID)
		tc.mu.Unlock()
		return "", nil
	}
	tc.mu.Unlock()

	return entry.payload, nil
}

func (tc *MemoryTokenCache) DeleteOAuthSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	tc.mu.Lock()
	delete(tc.oauth, sessionID)
	tc.mu.Unlock()
	return nil
}
