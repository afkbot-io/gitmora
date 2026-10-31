package domain_test

import (
	"strings"
	"testing"

	"workgraph.local/platform/internal/directory/domain"
)

func TestCommandsSeparateProviderIdentityFromMutableNames(t *testing.T) {
	name := "  App TOKEN=must-not-persist  "
	c := domain.Command{RequestID: "11111111-1111-4111-8111-111111111111", Action: domain.CreateRepository, ProjectID: "22222222-2222-4222-8222-222222222222", EntityID: "33333333-3333-4333-8333-333333333333", Name: &name, GitLabURL: "https://gitlab.internal/", GitLabProjectID: 42}
	normalized, e := domain.Normalize(c)
	if e != nil || normalized.GitLabURL != "https://gitlab.internal" || strings.Contains(*normalized.Name, "must-not-persist") {
		t.Fatal(normalized, e)
	}
	renamed := c
	renamed.Action = domain.RenameRepository
	renamed.ExpectedVersion = 10
	if _, e = domain.Normalize(renamed); e == nil {
		t.Fatal("repository identity retargeting accepted")
	}
	renamed.GitLabURL = ""
	renamed.GitLabProjectID = 0
	if _, e = domain.Normalize(renamed); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*domain.Command){func(c *domain.Command) { c.ProjectID = "../escape" }, func(c *domain.Command) { c.ExpectedVersion = 1 }, func(c *domain.Command) { c.GitLabURL = "https://user:secret@gitlab.internal" }, func(c *domain.Command) { c.Role = "admin" }} {
		bad := c
		change(&bad)
		if _, e = domain.Normalize(bad); e == nil {
			t.Fatal("malformed/mixed command accepted", bad)
		}
	}
}

func TestLastEnabledAdministratorAndDelegationBounds(t *testing.T) {
	if domain.CheckAdminRemoval("admin", true, "writer", 1) == nil {
		t.Fatal("last enabled project administrator removed")
	}
	if e := domain.CheckAdminRemoval("admin", true, "reader", 2); e != nil {
		t.Fatal(e)
	}
	if e := domain.CheckAdminRemoval("writer", true, "", 1); e != nil {
		t.Fatal(e)
	}
	if domain.CheckOperatorRemoval(true, true, false, 1) == nil {
		t.Fatal("last enabled operator removed")
	}
	if e := domain.CheckOperatorRemoval(true, true, false, 2); e != nil {
		t.Fatal(e)
	}
	if domain.MatchVersion(1, 2) == nil || domain.MatchVersion(0, 0) == nil {
		t.Fatal("stale/zero version accepted")
	}
}
