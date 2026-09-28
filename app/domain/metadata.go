package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var tokenNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._@-]{0,63}$`)

// NormalizeTokenName trims and validates a token's display name. Names are
// labels for humans and audit logs, not identifiers — they need not be
// unique.
func NormalizeTokenName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if !tokenNamePattern.MatchString(n) {
		return "", fmt.Errorf("%w: token name must be 1-64 characters (letters, digits, spaces, . _ @ -) and start with a letter or digit", ErrInvalid)
	}
	return n, nil
}

const (
	MaxTagsPerToken = 20
	MaxTagKeyLen    = 64
	MaxTagValueLen  = 256
)

var tagKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// Tag is one admin-asserted key/value label on a token. Tags describe who a
// token belongs to; they never participate in authorization.
type Tag struct {
	Key   string
	Value string
}

// ParseTag splits "key=value" on the first '='.
func ParseTag(pair string) (Tag, error) {
	k, v, ok := strings.Cut(pair, "=")
	if !ok {
		return Tag{}, fmt.Errorf("%w: tag %q must be key=value", ErrInvalid, pair)
	}
	return Tag{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)}, nil
}

// ParseTagLines parses one key=value per line, skipping blank lines.
func ParseTagLines(text string) ([]Tag, error) {
	var out []Tag
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		t, err := ParseTag(line)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: tag must be key=value", ErrInvalid, i+1)
		}
		out = append(out, t)
	}
	return out, nil
}

// NormalizeTags validates tag bounds, lowercases keys, and rejects duplicate
// keys (including ones that only collide after lowercasing).
func NormalizeTags(in []Tag) (map[string]string, error) {
	if len(in) > MaxTagsPerToken {
		return nil, fmt.Errorf("%w: at most %d tags per token", ErrInvalid, MaxTagsPerToken)
	}
	out := make(map[string]string, len(in))
	for _, t := range in {
		key := strings.ToLower(strings.TrimSpace(t.Key))
		value := strings.TrimSpace(t.Value)
		if len(key) > MaxTagKeyLen || !tagKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("%w: tag key %q must be 1-%d characters of a-z, 0-9, '_', '.', '-', starting with a letter or digit", ErrInvalid, t.Key, MaxTagKeyLen)
		}
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxTagValueLen {
			return nil, fmt.Errorf("%w: tag %q value must be valid text of at most %d characters", ErrInvalid, key, MaxTagValueLen)
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return nil, fmt.Errorf("%w: tag %q value must not contain control characters", ErrInvalid, key)
			}
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%w: duplicate tag key %q", ErrInvalid, key)
		}
		out[key] = value
	}
	return out, nil
}

// SortedTags returns a map's tags ordered by key, for stable display and
// for feeding map-shaped input back through NormalizeTags.
func SortedTags(m map[string]string) []Tag {
	out := make([]Tag, 0, len(m))
	for k, v := range m {
		out = append(out, Tag{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// TokenFilter narrows a token listing. An empty TagKey means no filter.
type TokenFilter struct {
	TagKey   string
	TagValue string
}

// ParseTokenFilter accepts "" (no filter) or "key=value".
func ParseTokenFilter(s string) (TokenFilter, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return TokenFilter{}, nil
	}
	t, err := ParseTag(s)
	if err != nil {
		return TokenFilter{}, err
	}
	key := strings.ToLower(t.Key)
	if !tagKeyPattern.MatchString(key) {
		return TokenFilter{}, fmt.Errorf("%w: tag filter must be key=value", ErrInvalid)
	}
	return TokenFilter{TagKey: key, TagValue: t.Value}, nil
}
