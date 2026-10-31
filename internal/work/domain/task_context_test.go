package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/work/domain"
)

func TestRichTaskContextOwnsRedactedCountryScopedChildEvidence(t *testing.T) {
	now := time.Now().UTC()
	input := domain.Task{CountryID: country, ID: taskID, Number: 12, Title: "Task", Status: "TESTING", Progress: 80, UpdatedAt: now, URL: "https://tasks.internal/task/12", Checklist: []domain.ChecklistItem{{ID: country, TaskID: taskID, Title: "DB_PASSWORD=checklist-secret", Position: 0}}, Comments: []domain.Comment{{ID: country, TaskID: taskID, Body: "Bearer private-comment", CreatedAt: now}}, Defects: []domain.Defect{{ID: country, TaskID: taskID, Title: "Expiry", Status: "OPEN", ExpectedResult: "token=defect-secret", UpdatedAt: now}}, Dependencies: []domain.Dependency{{ID: country, Number: 13, Title: "Prior work", Status: "COMPLETED"}}}
	got, err := domain.NormalizeTask(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"checklist-secret", "private-comment", "defect-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("retained secret")
		}
	}
	if input.Checklist[0].Title != "DB_PASSWORD=checklist-secret" {
		t.Fatal("mutated caller")
	}
	input.Comments[0].TaskID = country
	if _, err := domain.NormalizeTask(input); err == nil {
		t.Fatal("accepted comment from another task")
	}
}

func TestAbsentRichFieldsPreserveLegacyTaskDigest(t *testing.T) {
	legacy := `{"country_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","taskNumber":12,"title":"Task","description":"","acceptanceCriteria":"","status":"TESTING","progress":80,"updatedAt":"2026-10-30T00:00:00Z","url":"https://tasks.internal/task/12","documents":null,"mergeRequests":null}`
	var task domain.Task
	if err := json.Unmarshal([]byte(legacy), &task); err != nil {
		t.Fatal(err)
	}
	task, err := domain.NormalizeTask(task)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(task)
	if string(raw) != legacy {
		t.Fatal("legacy observation changed", string(raw))
	}
}
