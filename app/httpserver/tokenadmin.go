package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"coralline/app/auth"
	"coralline/app/domain"
	"coralline/app/logging"
	"coralline/app/sqlitestore"
	"coralline/ui"
)

// tokenAdmin implements ui.TokenAdmin for the web UI. The acting admin
// always comes from the authenticated session in the request context,
// never from form input, and every token is minted through auth.MintToken
// — the same validation the admin JSON API uses. It can only ever mint
// standard tokens.
type tokenAdmin struct {
	db  *sqlitestore.DB
	log *slog.Logger
}

var _ ui.TokenAdmin = (*tokenAdmin)(nil)

func actorFrom(ctx context.Context) (*domain.Token, error) {
	rv := viewerFrom(ctx)
	if rv.token == nil || !rv.token.IsAdmin() {
		return nil, domain.ErrForbidden
	}
	return rv.token, nil
}

func (a *tokenAdmin) ListTokens(ctx context.Context, filter domain.TokenFilter) ([]domain.Token, error) {
	if _, err := actorFrom(ctx); err != nil {
		return nil, err
	}
	return a.db.ListTokens(ctx, filter)
}

func (a *tokenAdmin) GetToken(ctx context.Context, id string) (*domain.Token, error) {
	if _, err := actorFrom(ctx); err != nil {
		return nil, err
	}
	return a.db.GetTokenDetail(ctx, id)
}

func (a *tokenAdmin) CreateStandardToken(ctx context.Context, in ui.NewToken) (*domain.Token, string, error) {
	actor, err := actorFrom(ctx)
	if err != nil {
		return nil, "", err
	}
	tok, plaintext, err := auth.MintToken(ctx, a.db, auth.MintRequest{
		Kind:      domain.TokenKindStandard,
		Name:      in.Name,
		CreatedBy: actor.ID,
		Grants:    in.Grants,
		Tags:      in.Tags,
		ExpiresIn: in.ExpiresIn,
	})
	if err != nil {
		return nil, "", err
	}
	logging.Audit(a.log, "token_created",
		"token_id", tok.ID, "token_name", tok.Name, "kind", string(tok.Kind),
		"grants", grantSummary(tok.Grants), "actor", actor.Name, "actor_id", actor.ID, "via", "ui")
	return tok, plaintext, nil
}

// requireStandard refuses UI changes to admin tokens: they are read-only in
// the web UI and managed from the CLI on the registry host, so a hijacked
// browser session can't retag or revoke another operator's admin token.
func (a *tokenAdmin) requireStandard(ctx context.Context, id string) (*domain.Token, error) {
	tok, err := a.db.GetToken(ctx, id)
	if err != nil {
		return nil, err
	}
	if tok.IsAdmin() {
		return nil, fmt.Errorf("%w: admin tokens are managed from the CLI on the registry host (coralline admin rotate)", domain.ErrInvalid)
	}
	return tok, nil
}

func (a *tokenAdmin) ReplaceTags(ctx context.Context, id string, tags []domain.Tag) error {
	actor, err := actorFrom(ctx)
	if err != nil {
		return err
	}
	if _, err := a.requireStandard(ctx, id); err != nil {
		return err
	}
	normalized, err := domain.NormalizeTags(tags)
	if err != nil {
		return err
	}
	if err := a.db.ReplaceTokenTags(ctx, id, normalized); err != nil {
		return err
	}
	logging.Audit(a.log, "token_tags_updated", "token_id", id, "actor", actor.Name, "actor_id", actor.ID, "via", "ui")
	return nil
}

func (a *tokenAdmin) ReplaceGrants(ctx context.Context, id string, grants []domain.GrantInput) error {
	actor, err := actorFrom(ctx)
	if err != nil {
		return err
	}
	current, err := a.requireStandard(ctx, id)
	if err != nil {
		return err
	}
	normalized, err := auth.ValidateGrants(ctx, a.db, current.Kind, grants)
	if err != nil {
		return err
	}
	if err := a.db.ReplaceTokenGrants(ctx, id, normalized); err != nil {
		return err
	}
	// Grant changes alter a live token's authority, so record exactly what changed.
	logging.Audit(a.log, "token_grants_updated", "token_id", id,
		"from", grantSummary(current.Grants), "to", grantSummary(normalized),
		"actor", actor.Name, "actor_id", actor.ID, "via", "ui")
	return nil
}

func (a *tokenAdmin) RevokeToken(ctx context.Context, id string) error {
	actor, err := actorFrom(ctx)
	if err != nil {
		return err
	}
	if _, err := a.requireStandard(ctx, id); err != nil {
		return err
	}
	if err := a.db.RevokeToken(ctx, id); err != nil {
		return err
	}
	logging.Audit(a.log, "token_revoked", "token_id", id, "actor", actor.Name, "actor_id", actor.ID, "via", "ui")
	return nil
}

func grantSummary(grants []domain.Grant) string {
	parts := make([]string, 0, len(grants))
	for _, g := range grants {
		parts = append(parts, g.Namespace+"="+string(g.Permission))
	}
	return strings.Join(parts, ",")
}
