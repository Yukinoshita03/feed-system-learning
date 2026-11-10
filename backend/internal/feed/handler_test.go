package feed

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFeedHandlerRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{`, `[]`, `{"limit":"ten"}`, `{"latest_id":-1}`,
		`{"limit":-1}`, `{"limit":51}`, `{"latest_time":1}`,
		`{"latest_id":1}`, `{"latest_time":-1,"latest_id":1}`,
	} {
		t.Run(body, func(t *testing.T) {
			handler := NewFeedHandler(NewFeedService(nil))
			router := gin.New()
			router.POST("/feed/listLatest", handler.ListLatest)
			request := httptest.NewRequest(http.MethodPost, "/feed/listLatest", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, body=%s", response.Code, response.Body.String())
			}
			var result map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["error"] == "" {
				t.Fatalf("invalid error response: %s", response.Body.String())
			}
		})
	}
}

func TestFeedHandlerClassifiesErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"invalid_parameter", fmt.Errorf("invalid: %w", ErrInvalidFeed), http.StatusBadRequest},
		{"internal_error", errors.New("internal database details"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			writeFeedError(c, test.err)
			if response.Code != test.status {
				t.Fatalf("status=%d, want %d", response.Code, test.status)
			}
			if test.status == http.StatusInternalServerError {
				if strings.Contains(response.Body.String(), test.err.Error()) || len(c.Errors) != 1 {
					t.Fatal("internal error must be recorded without exposing database details")
				}
			}
		})
	}
}
