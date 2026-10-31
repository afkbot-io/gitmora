package main

import (
	"bytes"
	"context"
	"testing"
)

func TestCoreCLIRejectsRetiredInferenceCommands(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if err := run(context.Background(), []string{"inference", "--help"}, nil, &out, &diagnostic); err == nil || out.Len() != 0 {
		t.Fatal("retired inference command remains available")
	}
}
