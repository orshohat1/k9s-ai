// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"1.0.65", [3]int{1, 0, 65}, true},
		{"GitHub Copilot CLI 1.0.65.", [3]int{1, 0, 65}, true},
		{"v0.0.420", [3]int{0, 0, 420}, true},
		{"garbage", [3]int{}, false},
		{"1.0", [3]int{}, false},
	}
	for _, tt := range tests {
		got, ok := parseVersion(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseVersion(%q) = %v,%v; want %v,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		have, want string
		expect     bool
	}{
		{"1.0.65", "1.0.65", true},  // equal
		{"1.0.66", "1.0.65", true},  // newer patch
		{"1.1.0", "1.0.65", true},   // newer minor
		{"2.0.0", "1.0.65", true},   // newer major
		{"1.0.60", "1.0.65", false}, // older patch (the failing CLI)
		{"0.0.420", "1.0.65", false},// old version scheme
		{"garbage", "1.0.65", false},// unparseable -> cannot confirm
	}
	for _, tt := range tests {
		if got := versionAtLeast(tt.have, tt.want); got != tt.expect {
			t.Errorf("versionAtLeast(%q, %q) = %v; want %v", tt.have, tt.want, got, tt.expect)
		}
	}
}
