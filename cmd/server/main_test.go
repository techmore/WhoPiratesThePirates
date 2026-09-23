package main

import "testing"

func TestIsLoopbackAddress(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "localhost", value: "localhost", want: true},
		{name: "ipv4", value: "127.0.0.1", want: true},
		{name: "ipv6", value: "[::1]", want: true},
		{name: "wildcard", value: "0.0.0.0", want: false},
		{name: "public", value: "192.0.2.10", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLoopbackAddress(tc.value); got != tc.want {
				t.Fatalf("isLoopbackAddress(%q) = %t, want %t", tc.value, got, tc.want)
			}
		})
	}
}

func TestEnvEnabled(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE"} {
		if !envEnabled(value) {
			t.Fatalf("envEnabled(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "0", "false"} {
		if envEnabled(value) {
			t.Fatalf("envEnabled(%q) = true, want false", value)
		}
	}
}
