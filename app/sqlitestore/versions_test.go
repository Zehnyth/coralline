package sqlitestore

import (
	"context"
	"sort"
	"testing"
)

func TestListPublishedVersionsReturnsOnlyActiveRows(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, ns := range []string{"acme", "beta"} {
		if err := db.CreateNamespace(ctx, ns); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(ns, name, system, version string) {
		t.Helper()
		if err := db.ReserveVersion(ctx, ns, name, system, version, ""); err != nil {
			t.Fatalf("reserve %s/%s/%s %s: %v", ns, name, system, version, err)
		}
		if err := db.ActivateVersion(ctx, ns, name, system, version, "sha", 1); err != nil {
			t.Fatalf("activate %s/%s/%s %s: %v", ns, name, system, version, err)
		}
	}
	publish("acme", "vpc", "aws", "1.0.0")
	publish("acme", "vpc", "aws", "1.1.0")
	publish("beta", "dash", "newrelic", "0.1.0")
	// Still uploading, and failed: neither is visible.
	if err := db.ReserveVersion(ctx, "acme", "vpc", "aws", "2.0.0", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ReserveVersion(ctx, "acme", "vpc", "aws", "3.0.0", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.FailVersion(ctx, "acme", "vpc", "aws", "3.0.0"); err != nil {
		t.Fatal(err)
	}

	all, err := db.ListPublishedVersions(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range all {
		if v.PublishedAt.IsZero() {
			t.Errorf("%s/%s %s has no publish time", v.Namespace, v.Name, v.Version)
		}
		got = append(got, v.Namespace+"/"+v.Name+"/"+v.System+"@"+v.Version)
	}
	sort.Strings(got)
	want := []string{"acme/vpc/aws@1.0.0", "acme/vpc/aws@1.1.0", "beta/dash/newrelic@0.1.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	beta, err := db.ListPublishedVersions(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(beta) != 1 || beta[0].Name != "dash" {
		t.Errorf("namespace-scoped listing = %+v", beta)
	}
}
