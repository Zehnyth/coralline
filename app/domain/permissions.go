package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Permission is an IAM-style "<resource>:<action>" string. The grantable
// set is closed: anything not in GrantablePermissions is rejected when a
// grant is written, so a typo fails loudly instead of silently never
// matching.
type Permission string

const (
	PermModuleRead    Permission = "module:read"
	PermModulePublish Permission = "module:publish"
	PermModuleAll     Permission = "module:*"
)

// AllNamespaces is the Grant.Namespace wildcard.
const AllNamespaces = "*"

var grantablePermissions = []Permission{PermModuleRead, PermModulePublish, PermModuleAll}

// GrantablePermissions lists every permission a standard token may hold.
func GrantablePermissions() []Permission {
	return append([]Permission(nil), grantablePermissions...)
}

// ParsePermission accepts only grantable permissions.
func ParsePermission(s string) (Permission, error) {
	p := Permission(strings.TrimSpace(s))
	for _, g := range grantablePermissions {
		if p == g {
			return p, nil
		}
	}
	names := make([]string, len(grantablePermissions))
	for i, g := range grantablePermissions {
		names[i] = string(g)
	}
	return "", fmt.Errorf("%w: unknown permission %q (grantable: %s)", ErrInvalid, s, strings.Join(names, ", "))
}

// covers matches exactly, or via a "<resource>:*" wildcard on the same
// resource. There is deliberately no other wildcard form.
func (p Permission) covers(want Permission) bool {
	if p == want {
		return true
	}
	resource, action, ok := strings.Cut(string(p), ":")
	if !ok || action != "*" {
		return false
	}
	wantResource, _, _ := strings.Cut(string(want), ":")
	return resource == wantResource
}

var namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-_]{0,63}$`)

// ValidNamespaceName is the one definition of a legal namespace name.
func ValidNamespaceName(name string) bool {
	return namespacePattern.MatchString(name)
}

// Grant binds one permission to one namespace (or AllNamespaces).
type Grant struct {
	Namespace  string
	Permission Permission
}

// GrantInput is an unvalidated grant as received from a transport.
type GrantInput struct {
	Namespace  string
	Permission string
}

// NormalizeGrants validates, de-duplicates, and sorts grants. It does not
// check that namespaces exist — callers with repository access do that.
func NormalizeGrants(in []GrantInput) ([]Grant, error) {
	seen := make(map[Grant]bool, len(in))
	out := make([]Grant, 0, len(in))
	for _, g := range in {
		ns := strings.TrimSpace(g.Namespace)
		if ns != AllNamespaces && !ValidNamespaceName(ns) {
			return nil, fmt.Errorf("%w: invalid namespace %q in grant", ErrInvalid, g.Namespace)
		}
		p, err := ParsePermission(g.Permission)
		if err != nil {
			return nil, err
		}
		grant := Grant{Namespace: ns, Permission: p}
		if seen[grant] {
			continue
		}
		seen[grant] = true
		out = append(out, grant)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Permission < out[j].Permission
	})
	return out, nil
}

// Role is form and CLI sugar: it expands to explicit permissions at mint
// time and is stored expanded, so a role's definition changing later never
// silently changes what existing tokens can do.
type Role struct {
	Name        string
	Description string
	Permissions []Permission
}

var roles = []Role{
	{Name: "reader", Description: "download modules", Permissions: []Permission{PermModuleRead}},
	{Name: "publisher", Description: "download and publish modules", Permissions: []Permission{PermModuleRead, PermModulePublish}},
}

func Roles() []Role {
	out := make([]Role, len(roles))
	for i, r := range roles {
		out[i] = Role{Name: r.Name, Description: r.Description, Permissions: append([]Permission(nil), r.Permissions...)}
	}
	return out
}

func ExpandRole(name string) ([]Permission, error) {
	for _, r := range roles {
		if r.Name == name {
			return append([]Permission(nil), r.Permissions...), nil
		}
	}
	return nil, fmt.Errorf("%w: unknown role %q (roles: reader, publisher)", ErrInvalid, name)
}
