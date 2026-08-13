// Package config loads the server configuration from the environment.
//
// Everything the server needs is an environment variable; there is no config
// file. Values that must not have a default (secrets) are reported as errors
// instead of being silently invented.
package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Addr        string
	DatabaseURL string

	// DataDir holds one working tree per project: <DataDir>/<project-id>/…
	DataDir string
	// GitDir holds one bare repository per group: <GitDir>/<group-slug>.git
	GitDir string

	// PublicURL is the address the server is reachable at from outside. It is
	// used for clone hints, ICS subscription URLs and webhook URLs.
	PublicURL string

	JWTSecret     []byte
	SecretKey     []byte // AES-256 key for account credentials
	CookieSecure  bool
	CookieDomain  string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	StepUpTTL     time.Duration
	MaxUploadSize int64

	// Bootstrap owner, only used when no user exists yet.
	OwnerUsername string
	OwnerPassword string

	// Git over SSH. Empty means it is not set up on this machine; the API then
	// says so instead of handing out an address that does not work.
	//
	// GitSSHHost is what the clone address starts with (git@offlinebot.xyz),
	// GitSSHWrapper the forced command the keys carry, and GitSSHSecret the
	// shared secret sshd and the wrapper authenticate to this server with.
	GitSSHHost    string
	GitSSHWrapper string
	GitSSHSecret  string

	GitBinary string
	// GitHTTPBackend is the path to git-http-backend. Alpine's git package does
	// not ship it — that lives in git-daemon (see README).
	GitHTTPBackend string

	Env string
}

func Load() (*Config, error) {
	// .env is a convenience for local runs; in Docker the variables come from
	// the environment and this is a no-op.
	_ = godotenv.Load(".env", "../.env")

	c := &Config{
		Addr:           envOr("ADDR", ":5000"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		DataDir:        envOr("DATA_DIR", "/srv/data"),
		GitDir:         envOr("GIT_DIR", "/srv/git"),
		PublicURL:      strings.TrimRight(envOr("PUBLIC_URL", "http://localhost:5173"), "/"),
		CookieSecure:   envBool("COOKIE_SECURE", true),
		CookieDomain:   os.Getenv("COOKIE_DOMAIN"),
		AccessTTL:      envDuration("ACCESS_TTL", 15*time.Minute),
		RefreshTTL:     envDuration("REFRESH_TTL", 30*24*time.Hour),
		StepUpTTL:      envDuration("STEPUP_TTL", 10*time.Minute),
		MaxUploadSize:  int64(envInt("MAX_UPLOAD_MB", 512)) << 20,
		OwnerUsername:  os.Getenv("OWNER_USERNAME"),
		OwnerPassword:  os.Getenv("OWNER_PASSWORD"),
		GitSSHHost:     os.Getenv("GIT_SSH_HOST"),
		GitSSHWrapper:  envOr("GIT_SSH_WRAPPER", "/usr/local/bin/hp-git-shell"),
		GitSSHSecret:   os.Getenv("GIT_SSH_SECRET"),
		GitBinary:      envOr("GIT_BINARY", "git"),
		GitHTTPBackend: envOr("GIT_HTTP_BACKEND", ""),
		Env:            envOr("ENV", "production"),
	}

	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}

	jwt := os.Getenv("JWT_SECRET")
	if len(jwt) < 16 {
		return nil, errors.New("JWT_SECRET is not set or shorter than 16 characters")
	}
	c.JWTSecret = []byte(jwt)

	secret := os.Getenv("SECRET_KEY")
	if len(secret) < 16 {
		return nil, errors.New("SECRET_KEY is not set or shorter than 16 characters (it encrypts account credentials)")
	}
	// A fixed-size key, whatever length the passphrase has.
	sum := sha256.Sum256([]byte(secret))
	c.SecretKey = sum[:]

	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, fmt.Errorf("DATA_DIR: %w", err)
	}
	c.DataDir = abs
	abs, err = filepath.Abs(c.GitDir)
	if err != nil {
		return nil, fmt.Errorf("GIT_DIR: %w", err)
	}
	c.GitDir = abs

	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create DATA_DIR: %w", err)
	}
	if err := os.MkdirAll(c.GitDir, 0o755); err != nil {
		return nil, fmt.Errorf("create GIT_DIR: %w", err)
	}
	return c, nil
}

func (c *Config) IsDev() bool { return c.Env == "development" }

// SSHEnabled reports whether git over SSH is set up. Without the secret the
// wrapper on the host could not authenticate, so half a configuration counts
// as off rather than as a clone address that does not work.
func (c *Config) SSHEnabled() bool {
	return c.GitSSHHost != "" && c.GitSSHSecret != ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
