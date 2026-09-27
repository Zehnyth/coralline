package ui

import (
	"fmt"
	"net"
	"sort"
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

// Status filter values on the token list. "expiring" is an active token
// that expires within expiringWithin.
const (
	filterActive   = "active"
	filterExpiring = "expiring"
	filterExpired  = "expired"
	filterRevoked  = "revoked"
)

const (
	expiringWithin = 7 * 24 * time.Hour
	// A token unused for this long (or never used, once this old) is
	// flagged as a candidate for revocation.
	staleAfter = 90 * 24 * time.Hour
)

// accessChip is one compact "what can this token do" label.
type accessChip struct {
	Label  string
	Accent bool
}

// tokenRow is a token pre-formatted for display.
type tokenRow struct {
	ID        string
	Name      string
	IsAdmin   bool
	Status    string // active | expired | revoked
	Filter    string // active | expiring | expired | revoked
	CreatedBy string
	CreatedAt string
	LastUsed  string
	// LastUsedAgo is the relative form; Stale flags a token worth
	// reviewing for offboarding.
	LastUsedAgo string
	Stale       bool
	Expires     string
	// ExpiresLabel is the compact form ("Never", "in 3 days", a date once
	// expired); ExpiresSoon flags an active token about to expire.
	ExpiresLabel string
	ExpiresSoon  bool
	RevokedAt    string
	Grants       []domain.Grant
	Access       []accessChip
	Tags         []domain.Tag
}

func newTokenRow(t domain.Token, now time.Time) tokenRow {
	row := tokenRow{
		ID: t.ID, Name: t.Name, IsAdmin: t.IsAdmin(), Status: statusActive, Filter: filterActive,
		CreatedBy: "system", CreatedAt: formatTimestamp(t.CreatedAt),
		LastUsed: "Never", LastUsedAgo: "Never", Expires: "Never", ExpiresLabel: "Never",
		Grants: t.Grants, Access: accessChips(t), Tags: domain.SortedTags(t.Tags),
	}
	switch {
	case t.Revoked():
		row.Status, row.Filter = statusRevoked, filterRevoked
		row.RevokedAt = formatTimestamp(*t.RevokedAt)
	case t.Expired(now):
		row.Status, row.Filter = statusExpired, filterExpired
	}
	if t.CreatedBy != "" {
		row.CreatedBy = t.CreatedByName
		if row.CreatedBy == "" {
			row.CreatedBy = "token " + shortID(t.CreatedBy)
		}
	}
	if t.LastUsedAt != nil {
		row.LastUsed = formatTimestamp(*t.LastUsedAt)
		row.LastUsedAgo = ago(*t.LastUsedAt, now)
		row.Stale = now.Sub(*t.LastUsedAt) > staleAfter
	} else {
		row.Stale = !t.CreatedAt.IsZero() && now.Sub(t.CreatedAt) > staleAfter
	}
	if t.ExpiresAt != nil {
		row.Expires = formatTimestamp(*t.ExpiresAt)
		if in := until(*t.ExpiresAt, now); in != "" {
			row.ExpiresLabel = in
		} else {
			row.ExpiresLabel = formatDate(*t.ExpiresAt)
		}
		if row.Status == statusActive && t.ExpiresAt.Sub(now) <= expiringWithin {
			row.Filter, row.ExpiresSoon = filterExpiring, true
		}
	}
	if row.Status != statusActive {
		row.Stale = false
	}
	return row
}

// SearchText is what the token list's search box matches: name and tags.
func (r tokenRow) SearchText() string {
	var b strings.Builder
	b.WriteString(r.Name)
	for _, t := range r.Tags {
		b.WriteString(" " + t.Key + "=" + t.Value)
	}
	return b.String()
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// accessChips summarizes a token's grants per namespace: "read · acme",
// "publish · all namespaces", or the raw permissions when they don't form
// a level.
func accessChips(t domain.Token) []accessChip {
	if t.IsAdmin() {
		return []accessChip{{Label: "admin", Accent: true}}
	}
	var out []accessChip
	for _, ns := range grantNamespaces(t.Grants) {
		where := namespaceLabel(ns)
		level, perms := accessLevel(t.Grants, ns)
		if level == levelCustom {
			for _, p := range perms {
				out = append(out, accessChip{Label: p + " · " + where})
			}
			continue
		}
		out = append(out, accessChip{Label: level + " · " + where})
	}
	return out
}

func namespaceLabel(ns string) string {
	if ns == domain.AllNamespaces {
		return "all namespaces"
	}
	return ns
}

// grantNamespaces lists the namespaces grants mention, "*" first.
func grantNamespaces(grants []domain.Grant) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range grants {
		if !seen[g.Namespace] {
			seen[g.Namespace] = true
			out = append(out, g.Namespace)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i] == domain.AllNamespaces) != (out[j] == domain.AllNamespaces) {
			return out[i] == domain.AllNamespaces
		}
		return out[i] < out[j]
	})
	return out
}

// Access levels in the per-namespace editor. Publish means read and
// publish — what the "publisher" role grants. Any other combination (a
// lone module:publish, module:*) is shown as custom and kept as it is.
const (
	levelNone    = "none"
	levelRead    = "read"
	levelPublish = "publish"
	levelCustom  = "custom"
)

// accessLevel reads the level of one namespace's grants, and the raw
// permissions behind it.
func accessLevel(grants []domain.Grant, ns string) (string, []string) {
	var perms []string
	has := map[domain.Permission]bool{}
	for _, g := range grants {
		if g.Namespace == ns {
			has[g.Permission] = true
			perms = append(perms, string(g.Permission))
		}
	}
	sort.Strings(perms)
	switch {
	case len(has) == 0:
		return levelNone, nil
	case len(has) == 1 && has[domain.PermModuleRead]:
		return levelRead, perms
	case len(has) == 2 && has[domain.PermModuleRead] && has[domain.PermModulePublish]:
		return levelPublish, perms
	default:
		return levelCustom, perms
	}
}

// accessRow is one namespace in the token detail's access editor.
type accessRow struct {
	// Namespace is the grant value ("*" for all namespaces); Label is
	// what's shown.
	Namespace string
	Label     string
	Level     string
	// Custom lists the raw permissions of a custom level, which the editor
	// can keep but not build.
	Custom []string
}

// buildAccessRows lists "all namespaces", every namespace, and any namespace
// only the grants still mention. levels overrides the token's current
// levels when re-rendering a rejected submission.
func buildAccessRows(namespaces []domain.Namespace, grants []domain.Grant, levels map[string]string) []accessRow {
	names := []string{domain.AllNamespaces}
	seen := map[string]bool{domain.AllNamespaces: true}
	for _, n := range namespaces {
		if !seen[n.Name] {
			seen[n.Name] = true
			names = append(names, n.Name)
		}
	}
	for _, ns := range grantNamespaces(grants) {
		if !seen[ns] {
			seen[ns] = true
			names = append(names, ns)
		}
	}
	rows := make([]accessRow, 0, len(names))
	for _, ns := range names {
		level, perms := accessLevel(grants, ns)
		row := accessRow{Namespace: ns, Label: capitalize(namespaceLabel(ns)), Level: level}
		if level == levelCustom {
			row.Custom = perms
		}
		if submitted, ok := levels[ns]; ok {
			row.Level = submitted
		}
		rows = append(rows, row)
	}
	return rows
}

// accessFormPrefix prefixes each namespace's level field: access:<ns>.
const accessFormPrefix = "access:"

// grantsFromLevels turns submitted per-namespace levels back into grants.
// A custom level keeps that namespace's current grants unchanged.
func grantsFromLevels(levels map[string]string, current []domain.Grant) ([]domain.GrantInput, error) {
	var out []domain.GrantInput
	add := func(ns string, perms ...domain.Permission) {
		for _, p := range perms {
			out = append(out, domain.GrantInput{Namespace: ns, Permission: string(p)})
		}
	}
	names := make([]string, 0, len(levels))
	for ns := range levels {
		names = append(names, ns)
	}
	sort.Strings(names)
	for _, ns := range names {
		switch levels[ns] {
		case levelNone:
		case levelRead:
			add(ns, domain.PermModuleRead)
		case levelPublish:
			add(ns, domain.PermModuleRead, domain.PermModulePublish)
		case levelCustom:
			for _, g := range current {
				if g.Namespace == ns {
					add(ns, g.Permission)
				}
			}
		default:
			return nil, fmt.Errorf("%w: choose None, Read, or Publish for %s", domain.ErrInvalid, namespaceLabel(ns))
		}
	}
	return out, nil
}

// statusTab is one option of the token list's status filter.
type statusTab struct {
	Value string
	Label string
	Count int
}

func statusTabs(rows []tokenRow) []statusTab {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Filter]++
	}
	return []statusTab{
		{Value: "", Label: "All", Count: len(rows)},
		{Value: filterActive, Label: "Active", Count: counts[filterActive]},
		{Value: filterExpiring, Label: "Expiring soon", Count: counts[filterExpiring]},
		{Value: filterExpired, Label: "Expired", Count: counts[filterExpired]},
		{Value: filterRevoked, Label: "Revoked", Count: counts[filterRevoked]},
	}
}

type tokensPage struct {
	basePage
	Catalog    catalogProps
	Tokens     []tokenRow
	StatusTabs []statusTab
	Notice     string
	Error      string
}

type tokenForm struct {
	Name        string
	Namespace   string
	Role        string
	Permissions []string
	TagsText    string
	// Expiry is the chosen preset: hours as a string, "0" for never, or
	// "custom" to use ExpiresInHours.
	Expiry         string
	ExpiresInHours string
	// Submission is a single-use key that makes a resubmitted or
	// double-clicked form return the first result instead of minting again.
	Submission string
}

// expiryCustom is the Expiry value that reads ExpiresInHours instead.
const expiryCustom = "custom"

// hours resolves the preset (or the custom field) to the hours string
// newToken validates.
func (f tokenForm) hours() string {
	if f.Expiry != "" && f.Expiry != expiryCustom {
		return f.Expiry
	}
	return f.ExpiresInHours
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

// expiryOption is one expiry preset on the create form.
type expiryOption struct {
	Value string
	Label string
}

// expiryOptions lists the presets that fit within max hours, then Never
// and Custom.
func expiryOptions(max int) []expiryOption {
	var out []expiryOption
	for _, o := range []struct {
		hours int
		label string
	}{{7 * 24, "7 days"}, {30 * 24, "30 days"}, {90 * 24, "90 days"}, {365 * 24, "1 year"}} {
		if o.hours <= max {
			out = append(out, expiryOption{Value: strconv.Itoa(o.hours), Label: o.label})
		}
	}
	return append(out, expiryOption{Value: "0", Label: "Never"}, expiryOption{Value: expiryCustom, Label: "Custom"})
}

type tokenFormPage struct {
	basePage
	Form              tokenForm
	Namespaces        []string
	Roles             []roleOption
	PermissionOptions []permissionOption
	ExpiryOptions     []expiryOption
	MaxExpiryHours    int
	Error             string
}

type tokenCreatedPage struct {
	basePage
	Token tokenRow
	// Plaintext is empty once the secret has been shown.
	Plaintext string
	// EnvVar is the TF_TOKEN_<host> variable Terraform and OpenTofu read
	// for this registry, or "" when the host has a port (which the
	// variable name can't carry).
	EnvVar string
	// CanPublish shows the publishing snippet.
	CanPublish bool
}

type tokenDetailPage struct {
	basePage
	Token          tokenRow
	Access         []accessRow
	TagsText       string
	Notice         string
	Error          string
	GrantsEditable bool
	TagsEditable   bool
	Revocable      bool
}

// terraformEnvVar is the credentials variable for host: TF_TOKEN_ followed
// by the host name with "." as "_" and "-" as "__". A host with a port has
// none.
func terraformEnvVar(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return ""
	}
	name := strings.NewReplacer("-", "__", ".", "_").Replace(strings.ToLower(host))
	return "TF_TOKEN_" + name
}

func canPublish(t domain.Token) bool {
	for _, g := range t.Grants {
		if g.Permission == domain.PermModulePublish || g.Permission == domain.PermModuleAll {
			return true
		}
	}
	return false
}

func roleOptions() []roleOption {
	var out []roleOption
	for _, role := range domain.Roles() {
		out = append(out, roleOption{Value: role.Name, Label: capitalize(role.Name), Description: capitalize(role.Description) + "."})
	}
	return append(out, roleOption{Value: customRole, Label: "Custom", Description: "Pick exact permissions."})
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

	if f.Expiry == expiryCustom && strings.TrimSpace(f.ExpiresInHours) == "" {
		// An empty custom expiry would otherwise read as "never".
		return NewToken{}, fmt.Errorf("%w: enter how many hours the token lasts, or pick a preset", domain.ErrInvalid)
	}
	hours := 0
	if h := strings.TrimSpace(f.hours()); h != "" {
		n, err := strconv.Atoi(h)
		if err != nil || n < 0 || n > maxExpiryHours {
			return NewToken{}, fmt.Errorf("%w: expiry must be a whole number of hours between 0 and %d", domain.ErrInvalid, maxExpiryHours)
		}
		hours = n
	}
	return NewToken{Name: f.Name, Grants: grants, Tags: tags, ExpiresIn: time.Duration(hours) * time.Hour}, nil
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
