package config

import (
	"errors"
	"testing"
)

func TestConfigSubPathMethods(t *testing.T) {
	tests := []struct {
		name          string
		subPath       string
		wantTrimmed   string
		wantIsCustom  bool
		wantWithSlash string
	}{
		{
			name:          "root slash",
			subPath:       "/",
			wantTrimmed:   "",
			wantIsCustom:  false,
			wantWithSlash: "/",
		},
		{
			name:          "empty string",
			subPath:       "",
			wantTrimmed:   "",
			wantIsCustom:  false,
			wantWithSlash: "/",
		},
		{
			name:          "custom prefix with trailing slash",
			subPath:       "/subscription/",
			wantTrimmed:   "/subscription",
			wantIsCustom:  true,
			wantWithSlash: "/subscription/",
		},
		{
			name:          "custom prefix without trailing slash",
			subPath:       "/subscription",
			wantTrimmed:   "/subscription",
			wantIsCustom:  true,
			wantWithSlash: "/subscription/",
		},
		{
			name:          "custom prefix without leading slash",
			subPath:       "custom-sub",
			wantTrimmed:   "/custom-sub",
			wantIsCustom:  true,
			wantWithSlash: "/custom-sub/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := BackendConfig{BasePath: tt.subPath}
			if got := b.Trimmed(); got != tt.wantTrimmed {
				t.Errorf("Trimmed() = %q, want %q", got, tt.wantTrimmed)
			}
			if got := b.IsCustom(); got != tt.wantIsCustom {
				t.Errorf("IsCustom() = %v, want %v", got, tt.wantIsCustom)
			}
			if got := b.WithSlash(); got != tt.wantWithSlash {
				t.Errorf("WithSlash() = %q, want %q", got, tt.wantWithSlash)
			}
		})
	}
}

func TestValidateBasePath(t *testing.T) {
	valid := []string{
		"/",
		"",
		"/subscription",
		"/subscription/",
		"/custom-sub_123/v1",
		"sub",
	}

	for _, path := range valid {
		if err := validateBasePath(path); err != nil {
			t.Errorf("expected valid for %q, got error: %v", path, err)
		}
	}

	invalid := []string{
		"/../escape",
		"/sub?query=1",
		"/sub#hash",
		"/sub with space",
		"/sub\\backslash",
		"/sub;injection",
	}

	for _, path := range invalid {
		if err := validateBasePath(path); err == nil {
			t.Errorf("expected invalid for %q, got nil error", path)
		}
	}
}

func TestParsePort(t *testing.T) {
	port, err := parsePort("SUB_APP_PORT", "8080", 3010)
	if err != nil || port != 8080 {
		t.Fatalf("expected 8080, got %d, %v", port, err)
	}

	port, err = parsePort("SUB_APP_PORT", "", 3010)
	if err != nil || port != 3010 {
		t.Fatalf("expected fallback 3010, got %d, %v", port, err)
	}

	_, err = parsePort("SUB_APP_PORT", "invalid", 3010)
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	var envErrs EnvErrors
	if !errors.As(err, &envErrs) || len(envErrs) == 0 || envErrs[0].Key != "SUB_APP_PORT" {
		t.Fatalf("expected EnvError with key SUB_APP_PORT, got %v", err)
	}
}
