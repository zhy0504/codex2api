package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func newAuthMiddlewareTestRouter(staticKeys []string) *gin.Engine {
	gin.SetMode(gin.TestMode)

	h := NewHandler(nil, nil, staticKeys)
	// Keep DB key cache valid in tests so invalid-key checks stay deterministic
	// and do not hit a nil DB.
	h.dbKeys = map[string]bool{}
	h.dbKeysUntil = time.Now().Add(time.Hour)

	r := gin.New()
	r.GET("/protected", h.authMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	return r
}

func TestAuthMiddleware_StaticAPIKeys(t *testing.T) {
	router := newAuthMiddlewareTestRouter([]string{"sk-test-valid"})

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
		wantErrorCode string
	}{
		{
			name:          "missing authorization header",
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "missing_api_key",
		},
		{
			name:          "non bearer authorization",
			authorization: "Basic dXNlcjpwYXNz",
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "missing_api_key",
		},
		{
			name:          "bearer with invalid key",
			authorization: "Bearer sk-test-invalid",
			wantStatus:    http.StatusUnauthorized,
			wantErrorCode: "invalid_api_key",
		},
		{
			name:          "bearer with valid static key",
			authorization: "Bearer sk-test-valid",
			wantStatus:    http.StatusOK,
		},
		{
			name:          "bearer with extra spaces around key",
			authorization: "Bearer   sk-test-valid   ",
			wantStatus:    http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status mismatch: got %d want %d", w.Code, tc.wantStatus)
			}

			if tc.wantErrorCode == "" {
				return
			}

			if got := gjson.GetBytes(w.Body.Bytes(), "error.code").String(); got != tc.wantErrorCode {
				t.Fatalf("error.code mismatch: got %q want %q", got, tc.wantErrorCode)
			}
		})
	}
}
