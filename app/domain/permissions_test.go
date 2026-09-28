package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAllows(t *testing.T) {
	standard := func(grants ...Grant) Token { return Token{Kind: TokenKindStandard, Grants: grants} }
	for _, tc := range []struct {
		name  string
		token Token
		ns    string
		want  Permission
		allow bool
	}{
		{"exact read", standard(Grant{"acme", PermModuleRead}), "acme", PermModuleRead, true},
		{"read does not imply publish", standard(Grant{"acme", PermModuleRead}), "acme", PermModulePublish, false},
		{"publish does not imply read", standard(Grant{"acme", PermModulePublish}), "acme", PermModuleRead, false},
		{"resource wildcard covers read", standard(Grant{"acme", PermModuleAll}), "acme", PermModuleRead, true},
		{"resource wildcard covers publish", standard(Grant{"acme", PermModuleAll}), "acme", PermModulePublish, true},
		{"namespace wildcard", standard(Grant{AllNamespaces, PermModuleRead}), "anything", PermModuleRead, true},
		{"wrong namespace", standard(Grant{"acme", PermModuleAll}), "other", PermModuleRead, false},
		{"no grants", standard(), "acme", PermModuleRead, false},
		{"resource wildcard does not cross resources", standard(Grant{"acme", PermModuleAll}), "acme", Permission("token:create"), false},
		{"admin allows everything", Token{Kind: TokenKindAdmin}, "acme", PermModulePublish, true},
		{"empty kind is not admin", Token{}, "acme", PermModuleRead, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.token.Allows(tc.ns, tc.want); got != tc.allow {
				t.Fatalf("Allows(%q, %q) = %v, want %v", tc.ns, tc.want, got, tc.allow)
			}
		})
	}
}

func TestTagsNeverGrantAccess(t *testing.T) {
	tok := Token{
		Kind: TokenKindStandard,
		Tags: map[string]string{"module:publish": "true", "role": "admin", "kind": "admin"},
	}
	if tok.Allows("acme", PermModulePublish) || tok.Allows("acme", PermModuleRead) {
		t.Fatal("a tag granted access")
	}
}

func TestParsePermissionRejectsUngrantable(t *testing.T) {
	for _, s := range []string{"module:pubish", "token:create", "token:*", "*:*", "namespace:create", "module", "", "MODULE:READ"} {
		if _, err := ParsePermission(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParsePermission(%q) err = %v, want ErrInvalid", s, err)
		}
	}
	for _, p := range GrantablePermissions() {
		if _, err := ParsePermission(" " + string(p) + " "); err != nil {
			t.Errorf("ParsePermission(%q) = %v", p, err)
		}
	}
}

func TestNormalizeGrants(t *testing.T) {
	got, err := NormalizeGrants([]GrantInput{
		{"acme", "module:publish"},
		{"*", "module:read"},
		{"acme", "module:publish"},
		{" acme ", "module:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Grant{{"*", PermModuleRead}, {"acme", PermModulePublish}, {"acme", PermModuleRead}}
	if len(got) != len(want) {
		t.Fatalf("grants = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("grants = %v, want %v", got, want)
		}
	}

	for _, bad := range [][]GrantInput{
		{{"Acme", "module:read"}},
		{{"", "module:read"}},
		{{"acme", "module:delete"}},
		{{"**", "module:read"}},
	} {
		if _, err := NormalizeGrants(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeGrants(%v) err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestExpandRole(t *testing.T) {
	perms, err := ExpandRole("publisher")
	if err != nil || len(perms) != 2 || perms[0] != PermModuleRead || perms[1] != PermModulePublish {
		t.Fatalf("ExpandRole(publisher) = %v, %v", perms, err)
	}
	perms[0] = PermModuleAll // must not mutate the role table
	if again, _ := ExpandRole("publisher"); again[0] != PermModuleRead {
		t.Fatal("ExpandRole returned a shared slice")
	}
	if _, err := ExpandRole("admin"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ExpandRole(admin) err = %v, want ErrInvalid", err)
	}
}

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]Tag{{" Owner ", " alice "}, {"team", "platform"}, {"note", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if got["owner"] != "alice" || got["team"] != "platform" || len(got) != 3 {
		t.Fatalf("tags = %v", got)
	}

	tooMany := make([]Tag, MaxTagsPerToken+1)
	for i := range tooMany {
		tooMany[i] = Tag{Key: "k" + strings.Repeat("x", i), Value: "v"}
	}
	for name, in := range map[string][]Tag{
		"too many":            tooMany,
		"empty key":           {{"", "v"}},
		"bad key chars":       {{"owner name", "v"}},
		"key leading dash":    {{"-owner", "v"}},
		"key too long":        {{strings.Repeat("k", MaxTagKeyLen+1), "v"}},
		"value too long":      {{"owner", strings.Repeat("v", MaxTagValueLen+1)}},
		"control character":   {{"owner", "alice\nadmin"}},
		"case-folded dupe":    {{"owner", "a"}, {"OWNER", "b"}},
		"exact duplicate key": {{"owner", "a"}, {"owner", "b"}},
	} {
		if _, err := NormalizeTags(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestParseTagLines(t *testing.T) {
	tags, err := ParseTagLines("owner=alice\r\n\n  team = platform \nurl=https://x.example/?a=b\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 || tags[1] != (Tag{"team", "platform"}) || tags[2].Value != "https://x.example/?a=b" {
		t.Fatalf("tags = %v", tags)
	}
	if _, err := ParseTagLines("owner=alice\nnot-a-tag"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestNormalizeTokenName(t *testing.T) {
	if n, err := NormalizeTokenName("  alice laptop "); err != nil || n != "alice laptop" {
		t.Fatalf("NormalizeTokenName = %q, %v", n, err)
	}
	for _, bad := range []string{"", " ", "-lead", strings.Repeat("a", 65), "tab\tname", "<script>"} {
		if _, err := NormalizeTokenName(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("NormalizeTokenName(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestTokenActive(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	if !(Token{ExpiresAt: &future}).Active(now) {
		t.Error("unexpired token inactive")
	}
	if (Token{ExpiresAt: &past}).Active(now) {
		t.Error("expired token active")
	}
	if (Token{RevokedAt: &past}).Active(now) {
		t.Error("revoked token active")
	}
}
