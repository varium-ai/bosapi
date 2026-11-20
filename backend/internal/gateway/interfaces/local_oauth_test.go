package interfaces

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gatewayinfra "bosapi/backend/internal/gateway/infrastructure"
)

func localOAuthTestProvider() provider {
	return provider{ID: 1, Type: "oauth", Status: "active", AccountID: "account", AccessToken: "initial-access", RefreshToken: "initial-refresh", ExpiresAt: time.Now().Add(-time.Minute)}
}

func rotatedOAuthResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`))}
}

func TestLocalOAuthRotationSurvivesTemporarySaveFailure(t *testing.T) {
	a := testApp(t)
	initial := localOAuthTestProvider()
	a.data.Providers = []provider{initial}
	backing := gatewayinfra.NewEncryptedStore(a.path, a.encryptionKey)
	if err := backing.Save(a.data); err != nil {
		t.Fatal(err)
	}
	saves := 0
	a.store = controlledGatewayStore{gatewayDataStore: backing, save: func(value any) error {
		saves++
		if saves == 1 {
			return errors.New("temporary disk failure")
		}
		return backing.Save(value)
	}}
	exchanges := 0
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		exchanges++
		if err := r.ParseForm(); err != nil || r.Form.Get("refresh_token") != initial.RefreshToken || exchanges != 1 {
			return nil, errors.New("unexpected or repeated token exchange")
		}
		return rotatedOAuthResponse(), nil
	})}
	refreshed, err := a.refresh(t.Context(), initial)
	if err != nil || refreshed.AccessToken != "rotated-access" || refreshed.RefreshToken != "rotated-refresh" {
		t.Fatalf("first refresh discarded the rotated credentials: %v", err)
	}
	if reloadLocalPersistence(t, a).Providers[0].RefreshToken != initial.RefreshToken {
		t.Fatal("first save should have failed")
	}
	// A request that selected the old snapshot must reuse the rotation and retry
	// only the write, not the one-time upstream exchange.
	refreshed, err = a.refresh(t.Context(), initial)
	if err != nil || refreshed.RefreshToken != "rotated-refresh" || exchanges != 1 || saves != 2 {
		t.Fatalf("did not retry only persistence: exchanges=%d saves=%d err=%v", exchanges, saves, err)
	}
	stored := reloadLocalPersistence(t, a).Providers[0]
	if stored.RefreshToken != refreshed.RefreshToken || stored.AccessToken != refreshed.AccessToken || !stored.ExpiresAt.Equal(refreshed.ExpiresAt) {
		t.Fatal("recovered encrypted store is missing the rotated credentials")
	}
	if _, err := a.refresh(t.Context(), refreshed); err != nil || saves != 2 {
		t.Fatalf("clean credentials triggered another write: saves=%d err=%v", saves, err)
	}
}

func TestLocalOAuthConcurrentRequestsShareRotation(t *testing.T) {
	a := testApp(t)
	initial := localOAuthTestProvider()
	a.data.Providers = []provider{initial}
	started, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	var exchanges atomic.Int32
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if exchanges.Add(1) != 1 {
			return nil, errors.New("duplicate token exchange")
		}
		close(started)
		<-proceed
		return rotatedOAuthResponse(), nil
	})}
	type result struct {
		p   provider
		err error
	}
	results := make(chan result, 2)
	request := func() {
		p, err := a.refresh(t.Context(), initial)
		results <- result{p, err}
	}
	go request()
	awaitLocalPersistence(t, started)
	go request()
	unblock()
	for range 2 {
		if result := awaitLocalPersistence(t, results); result.err != nil || result.p.RefreshToken != "rotated-refresh" {
			t.Fatalf("refresh could not share the rotation: %v", result.err)
		}
	}
	if exchanges.Load() != 1 {
		t.Fatal("concurrent requests replayed the old refresh token")
	}
}

func TestLocalOAuthRotationDoesNotOverwriteReplacement(t *testing.T) {
	a := testApp(t)
	initial := localOAuthTestProvider()
	a.data.Providers = []provider{initial}
	started, proceed := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(proceed) })
	defer unblock()
	a.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-proceed
		return rotatedOAuthResponse(), nil
	})}
	done := make(chan error, 1)
	go func() {
		_, err := a.refresh(t.Context(), initial)
		done <- err
	}()
	awaitLocalPersistence(t, started)
	replacement := provider{ID: initial.ID, Type: "oauth", Status: "active", AccountID: "other-account", AccessToken: "other-access", RefreshToken: "other-refresh"}
	if err := a.updateLocalData(func(next *data) bool {
		next.Providers[0] = replacement
		return true
	}); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := awaitLocalPersistence(t, done); err == nil {
		t.Fatal("refresh for a removed account should fail")
	}
	if stored := reloadLocalPersistence(t, a).Providers[0]; stored.AccountID != replacement.AccountID || stored.RefreshToken != replacement.RefreshToken || stored.AccessToken != replacement.AccessToken {
		t.Fatal("the late refresh overwrote the replacement provider")
	}
}
