package application

import (
	"context"
	"errors"
	"testing"

	"bosapi/backend/internal/analytics/domain"
)

func TestFreshInstallationDefaultsToEnabledOnceAcrossRestarts(t *testing.T) {
	repo := &memoryRepo{}
	ctx := context.Background()
	first := New(repo, Config{Endpoint: "https://receiver.invalid"}, nil, "v1.0.0")
	if err := first.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	status := first.Status(ctx)
	if !status.Enabled || !status.EffectiveEnabled || !repo.snap.Configured || len(repo.reports) != 1 {
		t.Fatalf("initial status=%+v reports=%d", status, len(repo.reports))
	}
	installation, epoch := repo.snap.InstallationID, repo.snap.CollectionEpoch
	if installation == "" || epoch == "" || repo.snap.EnabledSince == nil || repo.snap.ScanCursor == nil {
		t.Fatal("missing persistent initialization")
	}
	if err := first.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second := New(repo, Config{Endpoint: "https://receiver.invalid"}, nil, "v1.0.0")
	defer second.Close()
	if err := second.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if repo.snap.InstallationID != installation || repo.snap.CollectionEpoch != epoch || len(repo.reports) != 1 {
		t.Fatal("restart recreated identity, epoch, or initial report")
	}
}

func TestExplicitDisableBeforeFirstRefreshSurvivesRestart(t *testing.T) {
	repo := &memoryRepo{}
	ctx := context.Background()
	service := New(repo, Config{Endpoint: "https://receiver.invalid"}, nil, "")
	if err := service.Toggle(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	service.Close()
	restarted := New(repo, Config{Endpoint: "https://receiver.invalid"}, nil, "")
	defer restarted.Close()
	if err := restarted.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if !repo.snap.Configured || repo.snap.Enabled || len(repo.reports) != 0 || repo.snap.InstallationID != "" {
		t.Fatal("default overrode an explicit opt-out")
	}
}

func TestDefaultInitializationIsBlockedByDeploymentAndConfigurationFailures(t *testing.T) {
	for name, config := range map[string]Config{
		"forced":           {Endpoint: "https://receiver.invalid", ForceDisabled: true},
		"missing_receiver": {},
		"invalid_receiver": {Endpoint: "http://receiver.invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &memoryRepo{}
			service := New(repo, config, nil, "")
			defer service.Close()
			if err := service.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if repo.snap.Configured || len(repo.reports) != 0 || service.Status(context.Background()).EffectiveEnabled {
				t.Fatal("unavailable deployment initialized telemetry")
			}
		})
	}
}

func TestDefaultInitializationRetriesAfterStorageRecoveryAndStopsAfterClose(t *testing.T) {
	repo := &memoryRepo{readErr: domain.ErrUnavailable}
	service := New(repo, Config{Endpoint: "https://receiver.invalid"}, nil, "")
	if err := service.Refresh(context.Background()); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal(err)
	}
	if repo.snap.Configured || len(repo.reports) != 0 {
		t.Fatal("storage failure initialized telemetry")
	}
	repo.readErr = nil
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.Close()
	repo.snap.Configured, repo.snap.Enabled = false, false
	before := len(repo.reports)
	if err := service.Refresh(context.Background()); err != nil || len(repo.reports) != before {
		t.Fatal("closed service initialized telemetry", err)
	}
}
