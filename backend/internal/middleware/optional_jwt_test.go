package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"feed-system-learning/internal/auth"

	"github.com/gin-gonic/gin"
)

func TestOptionalJWTChecksProvidedCredentials(t *testing.T) {
	manager, _, err := auth.NewTokenManager("optional-test-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateToken(42, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		header string
		store  AccessTokenStore
		status int
	}{
		{"guest", "", nil, 200},
		{"valid", "Bearer " + token, testStore{active: true}, 200},
		{"revoked", "Bearer " + token, testStore{active: false}, 401},
		{"malformed", "Bearer invalid", testStore{active: true}, 401},
		{"wrong_scheme", "Basic " + token, testStore{active: true}, 401},
		{"store_failure", "Bearer " + token, testStore{err: errors.New("store unavailable")}, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/feed", OptionalJWT(manager, test.store), func(c *gin.Context) {
				value, _ := c.Get(AccountIDKey)
				id, _ := value.(uint)
				if test.name == "guest" && id != 0 || test.name == "valid" && id != 42 {
					t.Fatalf("unexpected viewer %d", id)
				}
				c.Status(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodGet, "/feed", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d, want %d", response.Code, test.status)
			}
		})
	}
}
