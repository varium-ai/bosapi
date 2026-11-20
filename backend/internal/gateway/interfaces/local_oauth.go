package interfaces

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"time"

	"bosapi/backend/internal/platform/logging"
)

func (a *app) refreshLocalOAuth(ctx context.Context, selected provider) (provider, error) {
	if time.Until(selected.ExpiresAt) > time.Minute {
		a.retryLocalOAuthSave(ctx)
		return selected, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return provider{}, err
		}
		// Re-read under the same lock used to claim the exchange: another request
		// may already have rotated the token in the selected snapshot.
		a.mu.Lock()
		index := slices.IndexFunc(a.data.Providers, func(p provider) bool {
			return p.ID == selected.ID && p.Type == "oauth" && p.AccountID == selected.AccountID
		})
		if index < 0 {
			a.mu.Unlock()
			return provider{}, errors.New("local OAuth provider removed or replaced")
		}
		current := a.data.Providers[index]
		if time.Until(current.ExpiresAt) > time.Minute {
			a.mu.Unlock()
			a.retryLocalOAuthSave(ctx)
			return current, nil
		}
		if done := a.localOAuthRefreshes[current.ID]; done != nil {
			a.mu.Unlock()
			select {
			case <-ctx.Done():
				return provider{}, ctx.Err()
			case <-done:
				continue
			}
		}
		if a.localOAuthRefreshes == nil {
			a.localOAuthRefreshes = make(map[int]chan struct{})
		}
		done := make(chan struct{})
		a.localOAuthRefreshes[current.ID] = done
		a.mu.Unlock()
		return a.rotateLocalOAuth(ctx, current, done)
	}
}

func (a *app) rotateLocalOAuth(ctx context.Context, original provider, done chan struct{}) (provider, error) {
	defer func() {
		a.mu.Lock()
		delete(a.localOAuthRefreshes, original.ID)
		close(done)
		a.mu.Unlock()
	}()
	credentials, err := a.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {original.RefreshToken}, "client_id": {clientID}, "scope": {"openid profile email"}})
	if err != nil {
		return provider{}, err
	}
	expiresAt := time.Now().Add(time.Duration(credentials.ExpiresIn) * time.Second)

	a.persistenceMu.Lock()
	defer a.persistenceMu.Unlock()
	next := a.localDataSnapshot()
	index := slices.IndexFunc(next.Providers, func(p provider) bool { return p.ID == original.ID })
	if index < 0 {
		return provider{}, errors.New("local OAuth provider removed or replaced")
	}
	current := next.Providers[index]
	// A deleted provider's ID can be reused while the upstream exchange is in
	// flight. Never publish the old account's tokens into its replacement.
	if current.Type != "oauth" || current.AccountID != original.AccountID || current.AccessToken != original.AccessToken ||
		current.RefreshToken != original.RefreshToken || !current.ExpiresAt.Equal(original.ExpiresAt) {
		return provider{}, errors.New("local OAuth credentials changed during refresh")
	}
	current.AccessToken = credentials.AccessToken
	if credentials.RefreshToken != "" {
		current.RefreshToken = credentials.RefreshToken
	}
	current.ExpiresAt = expiresAt
	next.Providers = slices.Clone(next.Providers)
	next.Providers[index] = current

	// The upstream may have invalidated the old refresh token. Unlike ordinary
	// configuration updates, this rotation cannot be rolled back on disk error.
	a.mu.Lock()
	a.data = next
	a.localOAuthSavePending.Store(true)
	a.mu.Unlock()
	a.persistLocalOAuth(ctx, next)
	return current, nil
}

func (a *app) retryLocalOAuthSave(ctx context.Context) {
	// Another writer already holds the lock; don't queue OAuth requests behind it
	// just to save the same credentials again.
	if !a.localOAuthSavePending.Load() || !a.persistenceMu.TryLock() {
		return
	}
	defer a.persistenceMu.Unlock()
	if a.localOAuthSavePending.Load() {
		a.persistLocalOAuth(ctx, a.localDataSnapshot())
	}
}

func (a *app) persistLocalOAuth(ctx context.Context, snapshot data) {
	if err := a.persistLocalData(snapshot); err != nil {
		logging.Event(ctx, "WARN", "gateway.oauth_token.persist_failed", "rotated local OAuth credentials retained in memory for save retry")
	}
}
