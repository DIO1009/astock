package main

import (
	"os"
	"testing"

	"astock_trade/rotation"
)

func withEnv(t *testing.T, key, value string, fn func()) {
	t.Helper()
	oldValue, existed := os.LookupEnv(key)
	if value == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, value)
	}
	defer func() {
		if existed {
			os.Setenv(key, oldValue)
		} else {
			os.Unsetenv(key)
		}
	}()
	fn()
}

func TestRotationEnabledFromEnv(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset or empty", value: "", want: false},
		{name: "zero", value: "0", want: false},
		{name: "false", value: "false", want: false},
		{name: "off", value: "off", want: false},
		{name: "one", value: "1", want: true},
		{name: "true", value: "true", want: true},
		{name: "on", value: "on", want: true},
		{name: "mixed whitespace yes", value: " YES ", want: true},
		{name: "invalid default false", value: "maybe", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, "ASTOCK_ROTATION_ENABLED", tc.value, func() {
				if got := rotationEnabledFromEnv(); got != tc.want {
					t.Fatalf("rotationEnabledFromEnv() = %v, want %v", got, tc.want)
				}
			})
		})
	}
}

func TestRotationPolicyForStartup(t *testing.T) {
	cfg := rotation.DefaultConfig()

	if policy := rotationPolicyForStartup(false, cfg); policy != nil {
		t.Fatalf("rotationPolicyForStartup(false, cfg) = %v, want nil", policy)
	}
	if policy := rotationPolicyForStartup(true, cfg); policy == nil {
		t.Fatal("rotationPolicyForStartup(true, cfg) = nil, want policy")
	}
}
