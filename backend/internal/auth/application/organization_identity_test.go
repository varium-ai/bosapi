package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"bosapi/backend/internal/auth/domain"
)

func TestAccessTokensForGlobalAndOrganizationAdministrators(t *testing.T) {
	for _, test := range []struct {
		role  string
		org   uint64
		valid bool
	}{
		{"super_admin", 0, true}, {"org_admin", 3, true}, {"org_admin", 0, false},
	} {
		t.Run(test.role+"/"+time.Duration(test.org).String(), func(t *testing.T) {
			user := domain.User{ID: 7, Role: test.role, OrganizationID: test.org, Status: "active"}
			repository := &mockAuthRepository{usersByID: map[uint64]domain.User{7: user}}
			service := NewService(repository, []byte(strings.Repeat("x", 32)), time.Minute, time.Hour)
			pair, err := service.issue(context.Background(), user)
			if err != nil {
				t.Fatal(err)
			}
			verified, _, _, err := service.VerifyAccess(context.Background(), pair.AccessToken)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
			if err == nil && verified.OrganizationID != test.org {
				t.Fatalf("wrong organization: %+v", verified)
			}
			// Organization or role changes invalidate previously issued access tokens.
			user.OrganizationID = 9
			repository.usersByID[7] = user
			if _, _, _, err := service.VerifyAccess(context.Background(), pair.AccessToken); err == nil {
				t.Fatal("stale organization claim accepted")
			}
		})
	}
}
