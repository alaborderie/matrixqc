package main

import (
	"context"
	"strings"
	"testing"
)

func TestCheckRequiresInputsAndValidConfidence(t *testing.T) {
	for _, args := range [][]string{{"check"}, {"check", "--matrix", "missing.csv", "--files", "missing", "--confidence", "NaN"}, {"bench", "--labels", "missing.json", "--results", "missing.json", "--out", "/tmp/benchmark.md"}} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("invalid CLI input returned success: %v", args)
		}
	}
	if err := run(context.Background(), []string{"check"}); err == nil || strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("input contract not implemented: %v", err)
	}
}

func TestStaticServeNeedsNoHomeOrCache(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	err := run(context.Background(), []string{"serve", "--unknown-flag"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("static server unexpectedly requires filesystem/cache: %v", err)
	}
}
