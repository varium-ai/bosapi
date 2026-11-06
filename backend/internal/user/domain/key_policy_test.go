package domain

import (
	"math"
	"strings"
	"testing"
)

func TestValidateKeyRateLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		value *int64
		valid bool
	}{
		{name: "unlimited", valid: true},
		{name: "minimum", value: keyRateLimitPointer(1), valid: true},
		{name: "maximum", value: keyRateLimitPointer(math.MaxUint32), valid: true},
		{name: "zero", value: keyRateLimitPointer(0)},
		{name: "negative", value: keyRateLimitPointer(-1)},
		{name: "overflow", value: keyRateLimitPointer(math.MaxUint32 + 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			for index := range 3 {
				limits := make([]*int64, 3)
				limits[index] = test.value
				if err := ValidateKeyRateLimits(limits...); (err == nil) != test.valid {
					t.Fatalf("limit at index %d: err = %v, want valid = %v", index, err, test.valid)
				}
			}
		})
	}
}

func keyRateLimitPointer(value int64) *int64 { return &value }

func TestValidateKeyName(t *testing.T) {
	for _, test := range []struct {
		name  string
		valid bool
	}{
		{name: "production", valid: true},
		{name: strings.Repeat("a", 100), valid: true},
		{name: strings.Repeat("密", 100), valid: true},
		{name: "  " + strings.Repeat("密", 100) + "  ", valid: true},
		{name: strings.Repeat("a", 101)},
		{name: strings.Repeat("密", 101)},
		{name: "  \t\n"},
		{name: ""},
		{name: string([]byte{0xff})},
	} {
		if err := ValidateKeyName(test.name); (err == nil) != test.valid {
			t.Fatalf("name length %d: err=%v want valid=%v", len(test.name), err, test.valid)
		}
	}
}

func TestValidateKeyQuotas(t *testing.T) {
	for _, test := range []struct {
		name  string
		value float64
		valid bool
	}{
		{name: "zero", value: 0, valid: true},
		{name: "positive", value: 1.25, valid: true},
		{name: "negative", value: -1},
		{name: "nan", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateKeyQuotaAmounts(nil, &test.value); (err == nil) != test.valid {
				t.Fatalf("amount: err=%v want valid=%v", err, test.valid)
			}
		})
	}
	for _, value := range []int64{0, 1, math.MaxInt64, -1} {
		if err := ValidateKeyQuotaTokens(nil, &value); (err == nil) != (value >= 0) {
			t.Fatalf("tokens %d: err=%v", value, err)
		}
	}
	if err := ValidateKeyQuotaAmounts(nil); err != nil {
		t.Fatalf("unlimited amount: %v", err)
	}
	if err := ValidateKeyQuotaTokens(nil); err != nil {
		t.Fatalf("unlimited tokens: %v", err)
	}
}
