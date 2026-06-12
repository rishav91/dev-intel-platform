package config

import (
	"testing"
	"time"
)

func TestString(t *testing.T) {
	t.Setenv("DI_TEST_STR", "hello")
	if got := String("DI_TEST_STR", "def"); got != "hello" {
		t.Errorf("set: got %q", got)
	}
	if got := String("DI_TEST_MISSING", "def"); got != "def" {
		t.Errorf("default: got %q", got)
	}
}

func TestList(t *testing.T) {
	t.Setenv("DI_TEST_LIST", "a, b ,c")
	got := List("DI_TEST_LIST", "")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len: got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestIntAndDuration(t *testing.T) {
	t.Setenv("DI_TEST_INT", "42")
	if got := Int("DI_TEST_INT", 0); got != 42 {
		t.Errorf("int: got %d", got)
	}
	t.Setenv("DI_TEST_INT_BAD", "notnum")
	if got := Int("DI_TEST_INT_BAD", 7); got != 7 {
		t.Errorf("int fallback: got %d", got)
	}
	t.Setenv("DI_TEST_DUR", "1500ms")
	if got := Duration("DI_TEST_DUR", 0); got != 1500*time.Millisecond {
		t.Errorf("duration: got %v", got)
	}
	if got := Duration("DI_TEST_DUR_MISSING", time.Second); got != time.Second {
		t.Errorf("duration default: got %v", got)
	}
}
