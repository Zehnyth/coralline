package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"coralline/app/domain"
)

// adminCredential resolves the admin token the operator captured when the
// server printed it. It is never read from disk — the operator supplies it
// via env var or flag each time a CLI command runs, the same way any other
// client of the admin API would authenticate.
func adminCredential(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if v := os.Getenv("CORALLINE_ADMIN_CREDENTIAL"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("admin token required: set CORALLINE_ADMIN_CREDENTIAL or pass --admin-cred")
}

type adminClient struct {
	addr string
	cred string
}

func (c *adminClient) do(method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+c.addr+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cred)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// call performs a request, checks the status, and decodes a JSON response
// into out when out is non-nil.
func (c *adminClient) call(method, path string, body any, okStatus int, out any) error {
	resp, err := c.do(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, okStatus); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func checkStatus(resp *http.Response, okStatus int) error {
	if resp.StatusCode != okStatus {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// connFlags are the admin API connection flags every admin subcommand takes.
type connFlags struct {
	addr *string
	cred *string
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		addr: fs.String("admin-addr", "127.0.0.1:8444", "admin API address"),
		cred: fs.String("admin-cred", "", "admin token (or set CORALLINE_ADMIN_CREDENTIAL)"),
	}
}

func (c connFlags) client() (*adminClient, error) {
	cred, err := adminCredential(*c.cred)
	if err != nil {
		return nil, err
	}
	return &adminClient{addr: *c.addr, cred: cred}, nil
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// parseTagFlags turns repeated key=value flags into the API's tag map. The
// server does the full validation; duplicates are caught here because a
// JSON object can't carry them.
func parseTagFlags(pairs []string) (map[string]string, error) {
	tags := map[string]string{}
	for _, pair := range pairs {
		t, err := domain.ParseTag(pair)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(t.Key)
		if _, dup := tags[key]; dup {
			return nil, fmt.Errorf("tag %q given more than once", key)
		}
		tags[key] = t.Value
	}
	return tags, nil
}

// tokenJSON mirrors the admin API's token view.
type tokenJSON struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	CreatedBy     string `json:"created_by,omitempty"`
	CreatedByName string `json:"created_by_name,omitempty"`
	Grants        []struct {
		Namespace  string `json:"namespace"`
		Permission string `json:"permission"`
	} `json:"grants"`
	Tags       map[string]string `json:"tags"`
	CreatedAt  time.Time         `json:"created_at"`
	ExpiresAt  *time.Time        `json:"expires_at,omitempty"`
	LastUsedAt *time.Time        `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time        `json:"revoked_at,omitempty"`
}

func (t tokenJSON) status(now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && now.After(*t.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}

type mintedJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Token string `json:"token"`
}

func formatWhen(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04")
}
