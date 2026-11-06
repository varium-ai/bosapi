package application

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"bosapi/backend/internal/auth/domain"
)

type countingLoginLockStore struct {
	mu             sync.Mutex
	threshold      int
	now            time.Time
	failures       map[string]int
	lockedUntil    map[string]time.Time
	unlocked       map[string]int
	recordFailures []string
}

func TestLoginLockExpiryAndManualUnlock(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := newCountingLoginLockStore(5, now)
	repo := &mockAuthRepository{
		users:     map[string]domain.User{"user@example.com": loginLockUser()},
		usersByID: map[uint64]domain.User{42: loginLockUser()},
	}
	svc := NewService(repo, []byte(strings.Repeat("x", 32)), time.Minute, time.Hour).WithLoginLock(store)
	svc.now = func() time.Time { return now }

	store.mu.Lock()
	store.lockedUntil["user@example.com"] = now.Add(15 * time.Minute)
	store.mu.Unlock()
	if _, err := svc.Login(context.Background(), "user@example.com", "correct-horse-battery"); err != ErrLoginLocked {
		t.Fatalf("locked login error = %v, want %v", err, ErrLoginLocked)
	}
	svc.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := svc.Login(context.Background(), "user@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("login after lock expiry: %v", err)
	}

	svc.now = func() time.Time { return now }
	store.mu.Lock()
	store.lockedUntil["user@example.com"] = now.Add(15 * time.Minute)
	store.mu.Unlock()
	if err := store.Unlock(context.Background(), "user@example.com"); err != nil {
		t.Fatalf("manual unlock: %v", err)
	}
	if _, err := svc.Login(context.Background(), "user@example.com", "correct-horse-battery"); err != nil {
		t.Fatalf("login after manual unlock: %v", err)
	}
}

func TestLoginLockSharesFailuresAcrossInstances(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := newCountingLoginLockStore(5, now)
	repo := &mockAuthRepository{
		users:     map[string]domain.User{"user@example.com": loginLockUser()},
		usersByID: map[uint64]domain.User{42: loginLockUser()},
	}
	first := NewService(repo, []byte(strings.Repeat("x", 32)), time.Minute, time.Hour).WithLoginLock(store)
	second := NewService(repo, []byte(strings.Repeat("x", 32)), time.Minute, time.Hour).WithLoginLock(store)
	first.now = func() time.Time { return now }
	second.now = func() time.Time { return now }

	var wg sync.WaitGroup
	for attempt := 0; attempt < 5; attempt++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = first.Login(context.Background(), "user@example.com", "wrong-secret-1234")
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = second.Login(context.Background(), "user@example.com", "wrong-secret-1234")
		}()
	}
	wg.Wait()
	decision, err := store.Check(context.Background(), "user@example.com", now)
	if err != nil || !decision.Locked {
		t.Fatalf("shared lock decision=%+v err=%v", decision, err)
	}
}

func newCountingLoginLockStore(threshold int, now time.Time) *countingLoginLockStore {
	return &countingLoginLockStore{
		threshold:   threshold,
		now:         now,
		failures:    make(map[string]int),
		lockedUntil: make(map[string]time.Time),
		unlocked:    make(map[string]int),
	}
}

func (store *countingLoginLockStore) Check(_ context.Context, key string, at time.Time) (domain.LoginLockDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	until, ok := store.lockedUntil[key]
	if ok && at.Before(until) {
		return domain.LoginLockDecision{Locked: true, RetryAfter: until.Sub(at)}, nil
	}
	if ok {
		delete(store.lockedUntil, key)
		store.failures[key] = 0
	}
	return domain.LoginLockDecision{}, nil
}

func (store *countingLoginLockStore) RecordFailure(_ context.Context, key string, at time.Time) (domain.LoginLockDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.recordFailures = append(store.recordFailures, key)
	if until, ok := store.lockedUntil[key]; ok && at.Before(until) {
		return domain.LoginLockDecision{Locked: true, RetryAfter: until.Sub(at)}, nil
	}
	store.failures[key]++
	if store.failures[key] < store.threshold {
		return domain.LoginLockDecision{}, nil
	}
	store.lockedUntil[key] = at.Add(15 * time.Minute)
	store.failures[key] = 0
	return domain.LoginLockDecision{Locked: true, RetryAfter: 15 * time.Minute}, nil
}

func (store *countingLoginLockStore) Unlock(_ context.Context, key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.unlocked[key]++
	delete(store.lockedUntil, key)
	store.failures[key] = 0
	return nil
}

func (store *countingLoginLockStore) failureCount(key string) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.failures[key]
}

func TestLoginLockAllowsFourFailuresAndLocksOnFifth(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := newCountingLoginLockStore(5, now)
	repo := &mockAuthRepository{
		users:     map[string]domain.User{"user@example.com": loginLockUser()},
		usersByID: map[uint64]domain.User{42: loginLockUser()},
	}
	svc := NewService(repo, []byte(strings.Repeat("x", 32)), time.Minute, time.Hour).WithLoginLock(store)
	svc.now = func() time.Time { return now }

	for attempt := 1; attempt <= 4; attempt++ {
		if _, err := svc.Login(context.Background(), "user@example.com", "wrong-secret-1234"); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
	}
	if got := store.failureCount("user@example.com"); got != 4 {
		t.Fatalf("failure count = %d, want 4", got)
	}
	if _, err := svc.Login(context.Background(), "USER@example.com ", "correct-horse-battery"); err != nil {
		t.Fatalf("correct credentials after four failures: %v", err)
	}
	if got := store.failureCount("user@example.com"); got != 0 {
		t.Fatalf("success reset failure count = %d, want 0", got)
	}
}

func TestLoginLockRejectsCorrectCredentialsAfterFifthFailure(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := newCountingLoginLockStore(5, now)
	repo := &mockAuthRepository{
		users:     map[string]domain.User{"user@example.com": loginLockUser()},
		usersByID: map[uint64]domain.User{42: loginLockUser()},
	}
	svc := NewService(repo, []byte(strings.Repeat("x", 32)), time.Minute, time.Hour).WithLoginLock(store)
	svc.now = func() time.Time { return now }

	for attempt := 1; attempt <= 5; attempt++ {
		if _, err := svc.Login(context.Background(), "user@example.com", "wrong-secret-1234"); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
	}
	if _, err := svc.Login(context.Background(), "user@example.com", "correct-horse-battery"); err != ErrLoginLocked {
		t.Fatalf("locked login error = %v, want %v", err, ErrLoginLocked)
	}
}

func loginLockUser() domain.User {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		panic(err)
	}
	return domain.User{ID: 42, Email: "user@example.com", PasswordHash: hash, Role: "org_admin", Status: "active"}
}
