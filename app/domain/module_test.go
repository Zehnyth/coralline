package domain

import (
	"errors"
	"testing"
)

func TestValidateModuleCoordinates(t *testing.T) {
	tests := []struct {
		name                            string
		namespace, module, system, vers string
		ok                              bool
	}{
		{"plain", "acme", "vpc", "aws", "1.0.0", true},
		{"hyphens and underscores", "acme", "my-vpc_mod", "aws", "10.2.3", true},
		{"prerelease and build", "acme", "vpc", "aws", "1.0.0-rc.1+build.5", true},
		{"escaped traversal in name", "acme", "../../other/mod", "aws", "1.0.0", false},
		{"slash in name", "acme", "a/b", "aws", "1.0.0", false},
		{"dot-dot version", "acme", "vpc", "aws", "..", false},
		{"slash in version", "acme", "vpc", "aws", "1.0.0/../x", false},
		{"uppercase system", "acme", "vpc", "AWS", "1.0.0", false},
		{"bad namespace", "Acme", "vpc", "aws", "1.0.0", false},
		{"empty name", "acme", "", "aws", "1.0.0", false},
		{"leading v", "acme", "vpc", "aws", "v1.0.0", false},
		{"leading zero", "acme", "vpc", "aws", "01.0.0", false},
		{"two components", "acme", "vpc", "aws", "1.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateModuleCoordinates(tt.namespace, tt.module, tt.system, tt.vers)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}
