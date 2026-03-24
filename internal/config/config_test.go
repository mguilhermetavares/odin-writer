package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// requiredEnvVars lists all required variables with dummy values for test setups.
var requiredEnvVars = map[string]string{
	"SANITY_PROJECT_ID": "test-project",
	"SANITY_DATASET":    "test-dataset",
	"SANITY_TOKEN":      "test-token",
	"ANTHROPIC_API_KEY": "test-anthropic-key",
	"GROQ_API_KEY":      "test-groq-key",
}

// setRequiredEnvVars sets all required env vars via t.Setenv so they are
// automatically cleaned up after each test.
func setRequiredEnvVars(t *testing.T) {
	t.Helper()
	for k, v := range requiredEnvVars {
		t.Setenv(k, v)
	}
}

// writeTempEnvFile writes a .env file in t.TempDir() with the given content
// and returns its path.
func writeTempEnvFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing temp env file: %v", err)
	}
	return path
}

// TestLoadValidEnvFile verifies that Load succeeds when a .env file provides
// all required variables.
func TestLoadValidEnvFile(t *testing.T) {
	content := `SANITY_PROJECT_ID=proj123
SANITY_DATASET=production
SANITY_TOKEN=tok
ANTHROPIC_API_KEY=ant
GROQ_API_KEY=groq
`
	path := writeTempEnvFile(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.SanityProjectID != "proj123" {
		t.Errorf("SanityProjectID = %q, want %q", cfg.SanityProjectID, "proj123")
	}
	if cfg.SanityDataset != "production" {
		t.Errorf("SanityDataset = %q, want %q", cfg.SanityDataset, "production")
	}
}

// TestLoadMissingRequiredVars checks that Load returns an error when each
// required variable is absent, one at a time.
func TestLoadMissingRequiredVars(t *testing.T) {
	required := []string{
		"SANITY_PROJECT_ID",
		"SANITY_DATASET",
		"SANITY_TOKEN",
		"ANTHROPIC_API_KEY",
		"GROQ_API_KEY",
	}

	for _, missing := range required {
		t.Run("missing_"+missing, func(t *testing.T) {
			setRequiredEnvVars(t)
			// Unset the variable under test by overriding with empty string.
			// t.Setenv restores the original value automatically.
			t.Setenv(missing, "")

			_, err := Load("")
			if err == nil {
				t.Errorf("expected error when %s is missing, got nil", missing)
			}
		})
	}
}

// TestLoadDefaultOptionalVars checks that optional variables receive their
// documented default values when not set.
func TestLoadDefaultOptionalVars(t *testing.T) {
	setRequiredEnvVars(t)

	// Ensure optional vars are absent.
	for _, key := range []string{"CLAUDE_MODEL", "TRANSCRIPT_LIMIT", "POLL_INTERVAL", "STYLE"} {
		t.Setenv(key, "")
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ClaudeModel != "claude-opus-4-6" {
		t.Errorf("ClaudeModel default = %q, want %q", cfg.ClaudeModel, "claude-opus-4-6")
	}
	if cfg.TranscriptLimit != 150000 {
		t.Errorf("TranscriptLimit default = %d, want 150000", cfg.TranscriptLimit)
	}
	if cfg.PollInterval != 24*time.Hour {
		t.Errorf("PollInterval default = %v, want 24h", cfg.PollInterval)
	}
	if cfg.StyleName != "esportivo" {
		t.Errorf("StyleName default = %q, want %q", cfg.StyleName, "esportivo")
	}
}

// TestLoadEnvVarTakesPrecedenceOverFile verifies that an environment variable
// already set before Load is called takes precedence over the .env file value.
func TestLoadEnvVarTakesPrecedenceOverFile(t *testing.T) {
	// Start with all required vars set via environment.
	setRequiredEnvVars(t)

	// Override one variable via env (should win over .env file).
	t.Setenv("SANITY_PROJECT_ID", "from-env")

	// .env file sets a different value for the same key.
	content := `SANITY_PROJECT_ID=from-file
SANITY_DATASET=test-dataset
SANITY_TOKEN=test-token
ANTHROPIC_API_KEY=test-anthropic-key
GROQ_API_KEY=test-groq-key
`
	path := writeTempEnvFile(t, content)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SanityProjectID != "from-env" {
		t.Errorf("SanityProjectID = %q, want %q (env should take precedence)", cfg.SanityProjectID, "from-env")
	}
}

// TestLoadNonexistentEnvFileUsesEnvVars confirms that Load does not fail when
// the .env file does not exist, falling back to environment variables.
func TestLoadNonexistentEnvFileUsesEnvVars(t *testing.T) {
	setRequiredEnvVars(t)

	nonexistent := filepath.Join(t.TempDir(), "does-not-exist.env")

	cfg, err := Load(nonexistent)
	if err != nil {
		t.Fatalf("expected no error for missing .env file, got %v", err)
	}
	if cfg.SanityProjectID != "test-project" {
		t.Errorf("SanityProjectID = %q, want %q", cfg.SanityProjectID, "test-project")
	}
}

// TestGetEnvOrDefaultReturnsDefault checks the helper returns the default
// value when the variable is not set.
func TestGetEnvOrDefaultReturnsDefault(t *testing.T) {
	t.Setenv("ODIN_TEST_VAR_NOTSET", "")

	result := getEnvOrDefault("ODIN_TEST_VAR_NOTSET", "my-default")
	if result != "my-default" {
		t.Errorf("getEnvOrDefault = %q, want %q", result, "my-default")
	}
}

// TestGetEnvOrDefaultReturnsEnvValue checks the helper returns the env value
// when it is set.
func TestGetEnvOrDefaultReturnsEnvValue(t *testing.T) {
	t.Setenv("ODIN_TEST_VAR_SET", "custom-value")

	result := getEnvOrDefault("ODIN_TEST_VAR_SET", "my-default")
	if result != "custom-value" {
		t.Errorf("getEnvOrDefault = %q, want %q", result, "custom-value")
	}
}

// TestGetEnvIntConvertsValidString checks that getEnvInt parses a valid integer
// string correctly.
func TestGetEnvIntConvertsValidString(t *testing.T) {
	t.Setenv("ODIN_TEST_INT", "42")

	result := getEnvInt("ODIN_TEST_INT", 99)
	if result != 42 {
		t.Errorf("getEnvInt = %d, want 42", result)
	}
}

// TestGetEnvIntReturnsDefaultOnInvalidString verifies getEnvInt falls back to
// the default when the env var is not a valid positive integer.
func TestGetEnvIntReturnsDefaultOnInvalidString(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ODIN_TEST_INT_BAD", tc.value)

			result := getEnvInt("ODIN_TEST_INT_BAD", 7)
			if result != 7 {
				t.Errorf("getEnvInt(%q) = %d, want default 7", tc.value, result)
			}
		})
	}
}

// TestGetEnvDurationConvertsValidString checks that getEnvDuration parses a
// valid duration string correctly.
func TestGetEnvDurationConvertsValidString(t *testing.T) {
	t.Setenv("ODIN_TEST_DUR", "12h")

	result := getEnvDuration("ODIN_TEST_DUR", 24*time.Hour)
	if result != 12*time.Hour {
		t.Errorf("getEnvDuration = %v, want 12h", result)
	}
}

// TestGetEnvDurationReturnsDefaultOnInvalidString verifies getEnvDuration falls
// back to the default when the env var is not a valid positive duration.
func TestGetEnvDurationReturnsDefaultOnInvalidString(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"invalid", "notaduration"},
		{"zero", "0s"},
		{"negative", "-1h"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ODIN_TEST_DUR_BAD", tc.value)

			result := getEnvDuration("ODIN_TEST_DUR_BAD", 24*time.Hour)
			if result != 24*time.Hour {
				t.Errorf("getEnvDuration(%q) = %v, want 24h", tc.value, result)
			}
		})
	}
}
