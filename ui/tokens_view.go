package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"coralline/app/domain"
)

// maxExpiryHours guards the int→Duration conversion; auth.MintToken
// re-validates the real bound.
const maxExpiryHours = 10 * 365 * 24

const customRole = "custom"

// Token statuses as shown in the UI.
const (
	statusActive  = "active"
	statusExpired = "expired"
	statusRevoked = "revoked"
)

// tokenRow is a token pre-formatted for display.
type tokenRow struct {
	ID        string
	Name      string
	IsAdmin   bool
	Status    string // active | expired | revoked
	CreatedBy string
	CreatedAt string
	LastUsed  string
	Expires   string
	RevokedAt string
	Grants    []domain.Grant
	Tags      []domain.Tag
}

func newTokenRow(t domain.Token, now time.Time) tokenRow {
	row := tokenRow{
		ID: t.ID, Name: t.Name, IsAdmin: t.IsAdmin(), Status: statusActive,
		CreatedBy: "system", CreatedAt: formatTimestamp(t.CreatedAt),
		LastUsed: "Never", Expires: "Never",
		Grants: t.Grants, Tags: domain.SortedTags(t.Tags),
	}
	switch {
	case t.Revoked():
		row.Status = statusRevoked
		row.RevokedAt = formatTimestamp(*t.RevokedAt)
	case t.Expired(now):
		row.Status = statusExpired
	}
	if t.CreatedBy != "" {
		row.CreatedBy = t.CreatedByName
		if row.CreatedBy == "" {
			row.CreatedBy = "token " + shortID(t.CreatedBy)
		}
	}
	if t.LastUsedAt != nil {
		row.LastUsed = formatTimestamp(*t.LastUsedAt)
	}
	if t.ExpiresAt != nil {
		row.Expires = formatTimestamp(*t.ExpiresAt)
	}
	return row
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

type tokensPage struct {
	basePage
	Tokens    []tokenRow
	TagFilter string
	Notice    string
	Error     string
}

type tokenForm struct {
	Name           string
	Namespace      string
	Role           string
	Permissions    []string
	TagsText       string
	ExpiresInHours string
	// Submission is a single-use key that makes a resubmitted or
	// double-clicked form return the first result instead of minting again.
	Submission string
}

type roleOption struct {
	Value       string
	Label       string
	Description string
}

type permissionOption struct {
	Value   string
	Checked bool
}

type tokenFormPage struct {
	basePage
	Form              tokenForm
	Namespaces        []string
	Roles             []roleOption
	PermissionOptions []permissionOption
	MaxExpiryHours    int
	Error             string
}

type tokenCreatedPage struct {
	basePage
	Token tokenRow
	// Plaintext is empty once the secret has been shown.
	Plaintext string
}

type tokenDetailPage struct {
	basePage
	Token          tokenRow
	Matrix         grantMatrix
	TagsText       string
	Notice         string
	Error          string
	GrantsEditable bool
	TagsEditable   bool
	Revocable      bool
}

func roleOptions() []roleOption {
	var out []roleOption
	for _, role := range domain.Roles() {
		out = append(out, roleOption{Value: role.Name, Label: capitalize(role.Name), Description: role.Description})
	}
	return append(out, roleOption{Value: customRole, Label: "Custom", Description: "choose permissions below"})
}

func permissionOptions(selected []string) []permissionOption {
	chosen := map[string]bool{}
	for _, s := range selected {
		chosen[s] = true
	}
	var out []permissionOption
	for _, p := range domain.GrantablePermissions() {
		out = append(out, permissionOption{Value: string(p), Checked: chosen[string(p)]})
	}
	return out
}

func (f tokenForm) newToken() (NewToken, error) {
	var perms []string
	if f.Role == customRole {
		if len(f.Permissions) == 0 {
			return NewToken{}, fmt.Errorf("%w: choose at least one custom permission", domain.ErrInvalid)
		}
		perms = f.Permissions
	} else {
		// The form disables the custom checkboxes unless Access is Custom;
		// without JavaScript they can still arrive, and silently dropping
		// them would mint a token other than the one the admin asked for.
		if len(f.Permissions) > 0 {
			return NewToken{}, fmt.Errorf("%w: custom permissions only apply when Access is set to Custom", domain.ErrInvalid)
		}
		expanded, err := domain.ExpandRole(f.Role)
		if err != nil {
			return NewToken{}, err
		}
		for _, p := range expanded {
			perms = append(perms, string(p))
		}
	}
	grants := make([]domain.GrantInput, 0, len(perms))
	for _, p := range perms {
		grants = append(grants, domain.GrantInput{Namespace: f.Namespace, Permission: p})
	}

	tags, err := domain.ParseTagLines(f.TagsText)
	if err != nil {
		return NewToken{}, err
	}

	hours := 0
	if f.ExpiresInHours != "" {
		n, err := strconv.Atoi(f.ExpiresInHours)
		if err != nil || n < 0 || n > maxExpiryHours {
			return NewToken{}, fmt.Errorf("%w: expiry must be a whole number of hours between 0 and %d", domain.ErrInvalid, maxExpiryHours)
		}
		hours = n
	}
	return NewToken{Name: f.Name, Grants: grants, Tags: tags, ExpiresIn: time.Duration(hours) * time.Hour}, nil
}

type grantMatrix struct {
	Columns []string
	Rows    []matrixRow
}

type matrixRow struct {
	Label string
	Cells []matrixCell
}

type matrixCell struct {
	Value      string // "<namespace>|<permission>"
	Permission string
	Checked    bool
}

func grantValue(namespace, permission string) string {
	return namespace + "|" + permission
}

// buildGrantMatrix lays out every namespace (plus "all namespaces") against
// every grantable permission. checked overrides the token's current grants
// when re-rendering a rejected submission.
func buildGrantMatrix(namespaces []domain.Namespace, grants []domain.Grant, checked map[string]bool) grantMatrix {
	if checked == nil {
		checked = map[string]bool{}
		for _, g := range grants {
			checked[grantValue(g.Namespace, string(g.Permission))] = true
		}
	}
	perms := domain.GrantablePermissions()
	m := grantMatrix{}
	for _, p := range perms {
		m.Columns = append(m.Columns, string(p))
	}
	names := []string{domain.AllNamespaces}
	seen := map[string]bool{domain.AllNamespaces: true}
	for _, n := range namespaces {
		if !seen[n.Name] {
			seen[n.Name] = true
			names = append(names, n.Name)
		}
	}
	for _, g := range grants {
		if !seen[g.Namespace] {
			seen[g.Namespace] = true
			names = append(names, g.Namespace)
		}
	}
	for _, ns := range names {
		row := matrixRow{Label: ns}
		if ns == domain.AllNamespaces {
			row.Label = "All namespaces"
		}
		for _, p := range perms {
			v := grantValue(ns, string(p))
			row.Cells = append(row.Cells, matrixCell{Value: v, Permission: string(p), Checked: checked[v]})
		}
		m.Rows = append(m.Rows, row)
	}
	return m
}

func tagLines(tags map[string]string) string {
	var b strings.Builder
	for _, t := range domain.SortedTags(tags) {
		b.WriteString(t.Key)
		b.WriteByte('=')
		b.WriteString(t.Value)
		b.WriteByte('\n')
	}
	return b.String()
}

// userMessage turns an invalid-input or conflict error into display text.
// Those messages are written for operators; the sentinel prefix is not.
func userMessage(err error) string {
	msg := err.Error()
	for _, sentinel := range []error{domain.ErrInvalid, domain.ErrConflict} {
		msg = strings.TrimPrefix(msg, sentinel.Error()+": ")
	}
	return capitalize(msg)
}
