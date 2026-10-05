package java

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestProbesRespectCallerCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ProbeContext(ctx, executable); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProbeContext error = %v, want context.Canceled", err)
	}
	if _, err := ProbeModulesContext(ctx, executable); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProbeModulesContext error = %v, want context.Canceled", err)
	}
}
