package auth

import (
	"context"
	"fmt"

	"coralline/app/domain"
)

// AdminBootstrapStore is what first-boot bootstrap needs.
type AdminBootstrapStore interface {
	TokenStore
	CountActiveAdminTokens(ctx context.Context) (int, error)
}

const BootstrapAdminName = "bootstrap-admin"

// EnsureAdminToken mints an admin token whenever the server starts with no
// active one (PRD FR-4.1). That single rule covers first boot, the upgrade
// from the legacy standalone admin credential, and lockout recovery: an
// operator with host access restarts the server to get a fresh admin token.
//
// The admin credential is an ordinary token of kind admin, so it goes
// through the same id lookup, argon2id semaphore, rate limiting, expiry,
// and revocation as every other token, and audit events can name it.
// It returns the plaintext only when a token was minted.
func EnsureAdminToken(ctx context.Context, store AdminBootstrapStore) (string, error) {
	n, err := store.CountActiveAdminTokens(ctx)
	if err != nil {
		return "", fmt.Errorf("counting admin tokens: %w", err)
	}
	if n > 0 {
		return "", nil
	}
	_, plaintext, err := MintToken(ctx, store, MintRequest{Kind: domain.TokenKindAdmin, Name: BootstrapAdminName})
	if err != nil {
		return "", fmt.Errorf("minting bootstrap admin token: %w", err)
	}
	return plaintext, nil
}

// PrintAdminBootstrapNotice is the one place a bootstrap admin token is
// ever shown — stdout, once, never the structured log stream.
// legacyCredentialPath is non-empty when a pre-token admin credential file
// was found, so the operator knows it no longer works.
func PrintAdminBootstrapNotice(plaintext, legacyCredentialPath string) {
	fmt.Println("=================================================================")
	fmt.Println(" Coralline: admin token generated")
	fmt.Println("=================================================================")
	fmt.Printf(" Admin token (shown once): %s\n", plaintext)
	fmt.Println(" Sign in to the web UI with it, and export it as")
	fmt.Println(" CORALLINE_ADMIN_CREDENTIAL for CLI commands.")
	fmt.Println(" This will not be shown again. Store it somewhere safe.")
	fmt.Println(" If every admin token is revoked or lost, restart the server")
	fmt.Println(" to mint a new one.")
	if legacyCredentialPath != "" {
		fmt.Printf(" The legacy admin credential in %s no longer works\n", legacyCredentialPath)
		fmt.Println(" and can be deleted.")
	}
	fmt.Println("=================================================================")
}
