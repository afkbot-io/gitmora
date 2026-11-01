package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
)

// ResolveMember permits exact lookup only after current project administration is proven.
func (s *DirectoryStore) ResolveMember(ctx context.Context, p access.Principal, project, input string) (domain.MemberCandidate, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	isID := identifier.Valid(input)
	if !isID {
		var e error
		input, e = domain.Username(input)
		if e != nil {
			return domain.MemberCandidate{}, e
		}
	}
	return directoryRead(ctx, s.pool, p, false, func(tx pgx.Tx, a directoryActor) (domain.MemberCandidate, error) {
		if e := directoryProjectAuthority(ctx, tx, a, project, true); e != nil {
			return domain.MemberCandidate{}, e
		}
		var out domain.MemberCandidate
		e := tx.QueryRow(ctx, `SELECT u.id::text,u.display_name,COALESCE(h.username,'') FROM users u LEFT JOIN user_handles h ON h.user_id=u.id WHERE u.enabled AND u.deleted_at IS NULL AND (($1 AND u.id::text=$2)OR(NOT $1 AND h.username=$2))`, isID, input).Scan(&out.ID, &out.DisplayName, &out.Username)
		return out, directoryNoRows(e)
	})
}
