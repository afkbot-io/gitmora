// Package access defines capabilities shared by application authorization boundaries.
package access

import "errors"

// ErrDenied identifies missing or insufficient authority without revealing resource existence.
var ErrDenied = errors.New("access denied")

// Permission names an explicitly supported operation; unknown capabilities fail closed.
type Permission string

const (
	// ReadCapture permits retrieving capture evidence within a repository.
	ReadCapture Permission = "capture:read"
	// WriteCapture permits appending evidence within a repository.
	WriteCapture Permission = "capture:write"
	// ReadIntegration permits reading provider connection health within a project.
	ReadIntegration Permission = "integration:read"
	// ManageIntegration permits administrator-only provider credential/configuration changes.
	ManageIntegration Permission = "integration:manage"
	// ReadDirectory permits discovering authorized local catalog metadata.
	ReadDirectory Permission = "directory:read"
	// ManageDirectory permits a project administrator to change its catalog/membership.
	ManageDirectory Permission = "directory:manage"
	// ManagePlatform requires a separate enabled installation-operator identity, never a project role.
	ManagePlatform Permission = "platform:manage"
	// ManageCredentials permits limited, revocable self-delegation and credential metadata reads.
	ManageCredentials Permission = "credentials:manage"
)

// Allows evaluates the local membership role. Provider authorization is an additional boundary.
func Allows(role string, permission Permission) bool {
	switch permission {
	case ReadCapture, ReadIntegration, ReadDirectory:
		return role == "reader" || role == "writer" || role == "admin"
	case WriteCapture:
		return role == "writer" || role == "admin"
	case ManageIntegration, ManageDirectory:
		return role == "admin"
	default:
		return false
	}
}

// SupportedPermission rejects invented capabilities at both issuance and use boundaries.
func SupportedPermission(permission Permission) bool {
	switch permission {
	case ReadCapture, WriteCapture, ReadIntegration, ManageIntegration, ReadDirectory, ManageDirectory, ManagePlatform, ManageCredentials:
		return true
	default:
		return false
	}
}
