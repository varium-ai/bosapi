package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestAnnouncementValidation(t *testing.T) {
	for _, test := range []struct {
		name, title, body string
		valid             bool
	}{
		{"markdown", "Release", "# Heading\n\n**Bold**\n\n- One\n- Two", true},
		{"unicode_title_limit", strings.Repeat("界", 200), "body", true},
		{"body_byte_limit", "Title", strings.Repeat("x", 65536), true},
		{"empty_title", "", "body", false},
		{"blank_title", " \n\t", "body", false},
		{"long_title", strings.Repeat("界", 201), "body", false},
		{"empty_body", "Title", "", false},
		{"blank_body", "Title", " \n\t", false},
		{"long_body", "Title", strings.Repeat("x", 65537), false},
		{"unicode_body_bytes", "Title", strings.Repeat("界", 21846), false},
		{"invalid_title_encoding", string([]byte{0xff}), "body", false},
		{"invalid_body_encoding", "Title", string([]byte{0xff}), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (Announcement{Title: test.title, Body: test.body}).Validate()
			if test.valid && err != nil || !test.valid && !errors.Is(err, ErrInvalidAnnouncement) {
				t.Fatalf("validation returned %v, valid=%v", err, test.valid)
			}
		})
	}
}
