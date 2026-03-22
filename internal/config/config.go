package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SanityProjectID  string
	SanityDataset    string
	SanityToken      string
	AnthropicAPIKey  string
	GroqAPIKey       string
	YouTubeChannelID string
	StateFile        string
	CacheDir         string
	ClaudeModel      string
	TranscriptLimit  int
	PollInterval     time.Duration
	StyleName        string
}

func Load(envFile string) (*Config, error) {
	if envFile != "" {
		if err := loadEnvFile(envFile); err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}

	home := os.Getenv("ODIN_WRITER_HOME")
	if home == "" {
		home = "/var/odin-writer"
	}

	cfg := &Config{
		SanityProjectID:  os.Getenv("SANITY_PROJECT_ID"),
		SanityDataset:    os.Getenv("SANITY_DATASET"),
		SanityToken:      os.Getenv("SANITY_TOKEN"),
		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		GroqAPIKey:       os.Getenv("GROQ_API_KEY"),
		YouTubeChannelID: os.Getenv("YOUTUBE_CHANNEL_ID"),
		StateFile:        getEnvOrDefault("STATE_FILE", filepath.Join(home, "state.json")),
		CacheDir:         getEnvOrDefault("CACHE_DIR", filepath.Join(home, "cache")),
		ClaudeModel:     getEnvOrDefault("CLAUDE_MODEL", "claude-opus-4-6"),
		TranscriptLimit: getEnvInt("TRANSCRIPT_LIMIT", 150000),
		PollInterval:    getEnvDuration("POLL_INTERVAL", 24*time.Hour),
		StyleName:       getEnvOrDefault("STYLE", "esportivo"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"SANITY_PROJECT_ID", c.SanityProjectID},
		{"SANITY_DATASET", c.SanityDataset},
		{"SANITY_TOKEN", c.SanityToken},
		{"ANTHROPIC_API_KEY", c.AnthropicAPIKey},
		{"GROQ_API_KEY", c.GroqAPIKey},
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("missing required env var: %s", r.name)
		}
	}
	return nil
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			value = value[1 : len(value)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}
