package domain

import (
	"strings"
	"testing"
)

func TestKeyAccessPolicyValidation(t *testing.T) {
	for _, test := range []struct {
		name      string
		providers []uint64
		models    []string
		valid     bool
	}{
		{name: "inherit", valid: true},
		{name: "explicit", providers: []uint64{2, 3}, models: []string{"model-one", "models/gemini-one"}, valid: true},
		{name: "zero channel", providers: []uint64{0}},
		{name: "duplicate channel", providers: []uint64{2, 2}},
		{name: "duplicate model", models: []string{"one", "one"}},
		{name: "empty model", models: []string{""}},
		{name: "whitespace", models: []string{" one"}},
		{name: "wildcard", models: []string{"*"}},
		{name: "long model", models: []string{strings.Repeat("a", 192)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateKeyAccessPolicy(test.providers, test.models); (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}
