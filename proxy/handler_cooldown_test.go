package proxy

import (
	"net/http"
	"testing"
	"time"

	"github.com/codex2api/auth"
)

func TestParseRetryAfter(t *testing.T) {
	t.Run("resets_in_seconds", func(t *testing.T) {
		body := []byte(`{"error":{"resets_in_seconds":30}}`)
		d, ok := parseRetryAfter(body)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if d != 30*time.Second {
			t.Fatalf("duration mismatch: got %v want %v", d, 30*time.Second)
		}
	})

	t.Run("invalid body", func(t *testing.T) {
		d, ok := parseRetryAfter([]byte(`{}`))
		if ok {
			t.Fatal("expected ok=false")
		}
		if d != 0 {
			t.Fatalf("duration mismatch: got %v want 0", d)
		}
	})

	t.Run("resets_at future", func(t *testing.T) {
		future := time.Now().Add(45 * time.Second).Unix()
		body := []byte(`{"error":{"resets_at":` + itoa(future) + `}}`)
		d, ok := parseRetryAfter(body)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if d < 40*time.Second || d > 50*time.Second {
			t.Fatalf("duration out of range: got %v want around 45s", d)
		}
	})
}

func TestCompute429Cooldown_PrefersShortParsedWindow(t *testing.T) {
	h := &Handler{}
	account := &auth.Account{PlanType: "free"}

	body := []byte(`{"error":{"resets_in_seconds":30}}`)
	got := h.compute429Cooldown(account, body, nil)

	if got != 30*time.Second {
		t.Fatalf("cooldown mismatch: got %v want %v", got, 30*time.Second)
	}
}

func TestCompute429Cooldown_UsesWindowFallbackWhenNoParse(t *testing.T) {
	h := &Handler{}
	account := &auth.Account{PlanType: "team"}

	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("x-codex-primary-window-minutes", "300")
	resp.Header.Set("x-codex-primary-used-percent", "101")

	got := h.compute429Cooldown(account, []byte(`{}`), resp)
	if got != 5*time.Hour {
		t.Fatalf("cooldown mismatch: got %v want %v", got, 5*time.Hour)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
