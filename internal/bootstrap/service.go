// Package bootstrap initializes a fresh installation through a privileged offline use case.
package bootstrap

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/endpoint"
	"workgraph.local/platform/internal/identifier"
)

var (
	// ErrInvalid identifies invalid initial setup without echoing sensitive input.
	ErrInvalid = errors.New("invalid bootstrap parameters")
	// ErrAlreadyInitialized prevents a second initial administrator from claiming an installation.
	ErrAlreadyInitialized = errors.New("installation already initialized")
)

// Input defines the initial local project/repository mapping, not a verified provider connection.
type Input struct {
	DisplayName     string
	ProjectName     string
	RepositoryName  string
	GitLabURL       string
	GitLabProjectID int64
}

// Setup is the atomic initial state supplied to the storage adapter; it contains no raw secret.
type Setup struct {
	Input        Input
	UserID       string
	ProjectID    string
	RepositoryID string
	Credential   access.CredentialRecord
}

// Store initializes only an empty installation and commits all records together.
type Store interface {
	Initialize(context.Context, Setup) error
}

// Result exposes the initial credential once after durable initialization succeeds.
type Result struct {
	UserID       string
	ProjectID    string
	RepositoryID string
	Credential   access.IssuedCredential
}

// Service implements initial setup; normal user management must use authenticated use cases.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService supplies the atomic storage port and clock used for credential expiration.
func NewService(store Store, now func() time.Time) *Service { return &Service{store: store, now: now} }

// Initialize creates a first administrator, scoped mapping and 90-day client credential.
func (s *Service) Initialize(ctx context.Context, input Input) (Result, error) {
	if !validName(input.DisplayName) || !validName(input.ProjectName) || !validName(input.RepositoryName) || input.GitLabProjectID < 1 {
		return Result{}, ErrInvalid
	}
	base, ok := endpoint.GitLabBase(input.GitLabURL)
	if !ok {
		return Result{}, ErrInvalid
	}
	input.GitLabURL = base
	userID, err := identifier.New()
	if err != nil {
		return Result{}, err
	}
	projectID, err := identifier.New()
	if err != nil {
		return Result{}, err
	}
	repositoryID, err := identifier.New()
	if err != nil {
		return Result{}, err
	}
	credential, err := access.IssueCredential(userID, "Initial client", []access.Permission{access.ReadCapture, access.WriteCapture, access.ReadIntegration, access.ManageIntegration}, s.now(), 90*24*time.Hour)
	if err != nil {
		return Result{}, err
	}
	setup := Setup{Input: input, UserID: userID, ProjectID: projectID, RepositoryID: repositoryID, Credential: credential.Record}
	if err := s.store.Initialize(ctx, setup); err != nil {
		return Result{}, err
	}
	return Result{UserID: userID, ProjectID: projectID, RepositoryID: repositoryID, Credential: credential}, nil
}

func validName(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && len(value) <= 200 && !strings.ContainsAny(value, "\x00\r\n")
}
