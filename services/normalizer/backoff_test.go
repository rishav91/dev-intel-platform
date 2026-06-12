package main

import (
	"errors"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, baseBackoff},     // clamped to attempt 1
		{1, baseBackoff},     // 250ms
		{2, 2 * baseBackoff}, // 500ms
		{3, 4 * baseBackoff}, // 1s
		{100, maxBackoff},    // capped (no overflow to <=0)
	}
	for _, tc := range cases {
		if got := backoff(tc.attempt); got != tc.want {
			t.Errorf("backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestIsPermanent(t *testing.T) {
	if !isPermanent(permanentError{errors.New("bad payload")}) {
		t.Error("permanentError should be permanent")
	}
	if isPermanent(errors.New("transient")) {
		t.Error("plain error should not be permanent")
	}
	if isPermanent(nil) {
		t.Error("nil should not be permanent")
	}
}
