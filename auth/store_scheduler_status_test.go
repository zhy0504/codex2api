package auth

import (
	"testing"
	"time"
)

func TestConcurrencyLimitForTier(t *testing.T) {
	tests := []struct {
		name      string
		baseLimit int64
		tier      AccountHealthTier
		want      int64
	}{
		{
			name:      "healthy uses base limit",
			baseLimit: 8,
			tier:      HealthTierHealthy,
			want:      8,
		},
		{
			name:      "warm halves base limit",
			baseLimit: 8,
			tier:      HealthTierWarm,
			want:      4,
		},
		{
			name:      "warm has minimum one",
			baseLimit: 1,
			tier:      HealthTierWarm,
			want:      1,
		},
		{
			name:      "risky always one",
			baseLimit: 8,
			tier:      HealthTierRisky,
			want:      1,
		},
		{
			name:      "banned always zero",
			baseLimit: 8,
			tier:      HealthTierBanned,
			want:      0,
		},
		{
			name:      "non positive base safe for healthy",
			baseLimit: 0,
			tier:      HealthTierHealthy,
			want:      1,
		},
		{
			name:      "non positive base safe for warm",
			baseLimit: -5,
			tier:      HealthTierWarm,
			want:      1,
		},
		{
			name:      "non positive base safe for risky",
			baseLimit: -3,
			tier:      HealthTierRisky,
			want:      1,
		},
		{
			name:      "non positive base safe for banned",
			baseLimit: -1,
			tier:      HealthTierBanned,
			want:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := concurrencyLimitForTier(tc.baseLimit, tc.tier); got != tc.want {
				t.Fatalf("concurrencyLimitForTier(%d, %q) = %d, want %d", tc.baseLimit, tc.tier, got, tc.want)
			}
		})
	}
}

func TestAccountRuntimeStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		buildAcct func() *Account
		want      string
	}{
		{
			name: "banned tier returns unauthorized",
			buildAcct: func() *Account {
				return &Account{
					HealthTier:     HealthTierBanned,
					Status:         StatusCooldown,
					CooldownUtil:   now.Add(5 * time.Minute),
					CooldownReason: "rate_limited",
					AccessToken:    "at",
				}
			},
			want: "unauthorized",
		},
		{
			name: "cooldown active with reason returns reason",
			buildAcct: func() *Account {
				return &Account{
					Status:         StatusCooldown,
					CooldownUtil:   now.Add(5 * time.Minute),
					CooldownReason: "rate_limited",
				}
			},
			want: "rate_limited",
		},
		{
			name: "cooldown active without reason returns cooldown",
			buildAcct: func() *Account {
				return &Account{
					Status:       StatusCooldown,
					CooldownUtil: now.Add(5 * time.Minute),
				}
			},
			want: "cooldown",
		},
		{
			name: "cooldown expired returns active",
			buildAcct: func() *Account {
				return &Account{
					Status:       StatusCooldown,
					CooldownUtil: now.Add(-5 * time.Minute),
				}
			},
			want: "active",
		},
		{
			name: "ready with access token returns active",
			buildAcct: func() *Account {
				return &Account{
					Status:      StatusReady,
					AccessToken: "at",
				}
			},
			want: "active",
		},
		{
			name: "ready without access token returns error",
			buildAcct: func() *Account {
				return &Account{
					Status: StatusReady,
				}
			},
			want: "error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acct := tc.buildAcct()
			if got := acct.RuntimeStatus(); got != tc.want {
				t.Fatalf("RuntimeStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}
