package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/work/domain"
)

const country = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const taskID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func TestTaskProjectionOwnsSanitizedBoundedEvidence(t *testing.T) {
	input := domain.Task{CountryID: country, ID: taskID, Number: 12, Title: "Test", Description: "token=must-never-persist", Status: "IN_PROGRESS", Progress: 30, UpdatedAt: time.Now(), URL: "https://tasks.internal/task/12?countryId=" + country + "&taskId=" + taskID, Documents: []domain.Document{{ID: taskID, FileName: "architecture.md", Title: "Architecture", Content: "Bearer a-long-private-token"}}}
	got, err := domain.NormalizeTask(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "must-never-persist") || strings.Contains(string(raw), "a-long-private-token") {
		t.Fatal("unredacted task")
	}
	if input.Documents[0].Content != "Bearer a-long-private-token" {
		t.Fatal("caller mutated")
	}
	bad := input
	bad.Progress = 101
	if _, err := domain.NormalizeTask(bad); err == nil {
		t.Fatal("invalid progress accepted")
	}
	bad = input
	bad.CountryID = "12"
	if _, err := domain.NormalizeTask(bad); err == nil {
		t.Fatal("numeric country accepted")
	}
	bad = input
	bad.Documents = append(bad.Documents, bad.Documents[0])
	if _, err := domain.NormalizeTask(bad); err == nil {
		t.Fatal("duplicate document accepted")
	}
}

func TestEvidenceUsesOnlyConfirmedClaimsAndStablePublicationIdentity(t *testing.T) {
	snapshot := domain.CaptureEvidence{RepositoryID: country, SessionID: taskID, CountryID: country, TaskID: taskID, Revision: 3, Digest: strings.Repeat("c", 64), Commits: []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}}
	input := domain.Evidence{ID: country, Capture: snapshot, Confirmed: []string{strings.Repeat("a", 40)}}
	write, err := domain.NewEvidenceDocument(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(write.Content, strings.Repeat("b", 40)) || !strings.Contains(write.Content, strings.Repeat("a", 40)) {
		t.Fatal("unconfirmed claim exported")
	}
	if write.FileName != "workgraph-evidence-"+country+".md" {
		t.Fatal(write.FileName)
	}
	retry, _ := domain.NewEvidenceDocument(input)
	if write != retry {
		t.Fatal("unstable retry")
	}
	input.Confirmed = []string{strings.Repeat("d", 40)}
	if _, err := domain.NewEvidenceDocument(input); err == nil {
		t.Fatal("foreign commit accepted")
	}
	input.Confirmed = nil
	if _, err := domain.NewEvidenceDocument(input); err == nil {
		t.Fatal("unverified evidence accepted")
	}
}

func TestTaskProjectionRemovesCredentialsFromSourceURLs(t *testing.T) {
	input := domain.Task{CountryID: country, ID: taskID, Number: 1, Title: "Task", Status: "PLANNING", UpdatedAt: time.Now(), URL: "https://tasks.internal/task/1?countryId=" + country + "&token=private-query-token", Links: []domain.Link{{URL: "https://gitlab.internal/group/app?access_token=another-private-token#private-fragment", Title: "MR"}}}
	got, err := domain.NormalizeTask(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"private-query-token", "another-private-token", "private-fragment"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("URL credential retained", secret)
		}
	}
}
