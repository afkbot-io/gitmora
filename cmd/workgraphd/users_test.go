package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUserCreationHelpAndValidationDoNotRequireDatabaseOrPassword(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "invalid-private-database")
	for _, args := range [][]string{{"users", "--help"}, {"users", "create", "--help"}} {
		var out bytes.Buffer
		if e := run(context.Background(), args, &out); e != nil || !strings.Contains(out.String(), "--username") {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"users", "create", "--password", "secret-argument"}, {"users", "create", "--username", "../unsafe", "--name", "Test"}} {
		var out bytes.Buffer
		e := run(context.Background(), args, &out)
		if e == nil || out.Len() != 0 || strings.Contains(e.Error(), "secret-argument") || strings.Contains(e.Error(), "private-database") {
			t.Fatal("unsafe account input", e)
		}
	}
}
