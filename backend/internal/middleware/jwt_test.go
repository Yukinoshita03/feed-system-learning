package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"feed-system-learning/internal/auth"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type testStore struct {
	active bool
	err    error
}

func (s testStore) IsAccessTokenActive(_ context.Context, _ uint, _ string) (bool, error) {
	return s.active, s.err
}

func TestRequireJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager, _, err := auth.NewTokenManager("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := manager.GenerateToken(42, "test-user")
	if err != nil {
		t.Fatal(err)
	}
	sign := func(method jwt.SigningMethod, key string, claims jwt.MapClaims) string {
		t.Helper()
		token, err := jwt.NewWithClaims(method, claims).SignedString([]byte(key))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	future := time.Now().Add(time.Hour).Unix()
	expired := sign(jwt.SigningMethodHS256, "test-secret", jwt.MapClaims{"account_id": 42, "exp": time.Now().Add(-time.Hour).Unix()})
	wrongKey := sign(jwt.SigningMethodHS256, "another-secret", jwt.MapClaims{"account_id": 42, "exp": future})
	wrongMethod := sign(jwt.SigningMethodHS384, "test-secret", jwt.MapClaims{"account_id": 42, "exp": future})
	noExpiration := sign(jwt.SigningMethodHS256, "test-secret", jwt.MapClaims{"account_id": 42})
	noAccountID := sign(jwt.SigningMethodHS256, "test-secret", jwt.MapClaims{"exp": future})

	tests := []struct {
		name, header string
		status       int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + valid, http.StatusUnauthorized},
		{"malformed token", "Bearer not-a-token", http.StatusUnauthorized},
		{"expired", "Bearer " + expired, http.StatusUnauthorized},
		{"wrong signature", "Bearer " + wrongKey, http.StatusUnauthorized},
		{"wrong algorithm", "Bearer " + wrongMethod, http.StatusUnauthorized},
		{"missing expiration", "Bearer " + noExpiration, http.StatusUnauthorized},
		{"missing account ID", "Bearer " + noAccountID, http.StatusUnauthorized},
		{"revoked", "Bearer " + valid, http.StatusUnauthorized},
		{"store error", "Bearer " + valid, http.StatusInternalServerError},
		{"valid", "Bearer " + valid, http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := testStore{active: test.name != "revoked"}
			if test.name == "store error" {
				store.err = errors.New("database unavailable")
			}
			r := gin.New()
			r.GET("/protected", RequireJWT(manager, store), func(c *gin.Context) {
				id, _ := c.Get(AccountIDKey)
				c.JSON(http.StatusOK, gin.H{"account_id": id})
			})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if test.name == "valid" && response.Body.String() != `{"account_id":42}` {
				t.Fatalf("account ID not forwarded: %s", response.Body.String())
			}
		})
	}
}
