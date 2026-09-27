package modules

import (
	"context"

	"coralline/app/domain"
)

// ReadRepo is the narrow read-side interface. Per the CQRS-lite design
// (PRD §6.2), these calls have no business logic to coordinate — the
// Terraform protocol handlers call the repository through this
// interface directly, with no service-layer indirection.
type ReadRepo interface {
	ListActiveVersions(ctx context.Context, namespace, name, system string) ([]string, error)
	GetActiveVersion(ctx context.Context, namespace, name, system, version string) (*domain.Version, error)
}
