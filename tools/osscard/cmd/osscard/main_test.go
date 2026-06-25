package main

import (
	"reflect"
	"testing"
)

// TestSplitScopes checks trimming, blank-skipping, and that an empty input yields nil.
func TestSplitScopes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"repo:a/b, org:c ,, repo:d/e", []string{"repo:a/b", "org:c", "repo:d/e"}},
		{"  ", nil},
		{"", nil},
		{"single", []string{"single"}},
	}
	for _, c := range cases {
		if got := splitScopes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitScopes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
