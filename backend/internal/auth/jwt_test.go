package auth

import (
	"testing"
	"time"
)

func TestGenerateTokenUsesInjectedLifetime(t *testing.T) {
	for _, lifetime := range []time.Duration{time.Minute, 47 * time.Minute} {
		t.Run(lifetime.String(), func(t *testing.T) {
			manager, _, err := NewTokenManager("test-secret", lifetime)
			if err != nil {
				t.Fatal(err)
			}
			token, err := manager.GenerateToken(42, "test-user")
			if err != nil {
				t.Fatal(err)
			}
			claims, err := manager.ParseToken(token)
			if err != nil {
				t.Fatal(err)
			}
			if claims.ExpiresAt == nil || claims.IssuedAt == nil {
				t.Fatal("missing expiration or issued-at claim")
			}
			if got := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time); got != lifetime {
				t.Fatalf("JWT lifetime = %v, want %v", got, lifetime)
			}
		})
	}
}

func TestNewTokenManagerRejectsInvalidLifetime(t *testing.T) {
	for _, lifetime := range []time.Duration{0, -time.Second, 500 * time.Millisecond} {
		t.Run(lifetime.String(), func(t *testing.T) {
			if _, _, err := NewTokenManager("test-secret", lifetime); err == nil {
				t.Fatal("invalid lifetime was accepted")
			}
		})
	}
}
