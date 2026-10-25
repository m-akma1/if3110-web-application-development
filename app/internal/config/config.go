// Package config is the only place that reads the application's environment variables.
// Load reports every missing or invalid variable at once, so the app refuses to start
// instead of running on a half-configured environment.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"dbplatform/app/internal/authn"
	"dbplatform/app/internal/bootstrap"
	"dbplatform/app/internal/dbmanager"
	"dbplatform/app/internal/groups"
	"dbplatform/app/internal/migration"
	"dbplatform/app/internal/rbac"
)

// Config is the process configuration.
type Config struct {
	DatabaseURL string // platform PostgreSQL, built from POSTGRES_*
	ListenAddr  string // LISTEN_ADDR, default ":8080"

	// TargetCredentialsKey seals target database passwords (AES-256-GCM, base64 of 32 bytes).
	TargetCredentialsKey string

	Session   authn.SessionConfig   // SESSION_*
	Query     dbmanager.QueryLimits // QUERY_*
	Migration migration.Limits      // MIGRATION_*

	// BootstrapAdmins are created only when the platform has no active admin (BOOTSTRAP_ADMINS).
	BootstrapAdmins []bootstrap.Admin

	SeedDemoData bool
	DemoUsers    []bootstrap.DemoUser  // SEED_DEMO_USERS, set when SeedDemoData
	DemoGroups   []bootstrap.DemoGroup // SEED_DEMO_GROUPS, set when SeedDemoData
	DemoTarget   dbmanager.TargetInput // demo database, built from TARGET_*, set when SeedDemoData
}

// Load reads the environment. It fails when a required value is missing or any value is malformed.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	r := &reader{getenv: getenv}
	cfg := Config{
		ListenAddr:           r.optional("LISTEN_ADDR", ":8080"),
		TargetCredentialsKey: r.required("TARGET_CREDENTIALS_KEY"),
		Session:              authn.DefaultSessionConfig(),
		Query:                dbmanager.DefaultQueryLimits(),
		Migration:            migration.DefaultLimits(),
	}

	cfg.DatabaseURL = postgresURL(
		r.required("POSTGRES_USER"),
		r.requiredSecret("POSTGRES_PASSWORD"),
		r.optional("POSTGRES_HOST", "postgres"),
		r.port("POSTGRES_PORT"),
		r.required("POSTGRES_DB"),
		r.sslMode("POSTGRES_SSLMODE", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"),
	)

	r.duration("SESSION_IDLE_TIMEOUT_MINUTES", time.Minute, &cfg.Session.IdleTimeout)
	r.duration("SESSION_ABSOLUTE_EXPIRATION_HOURS", time.Hour, &cfg.Session.AbsoluteExpiration)
	r.duration("SESSION_ROTATION_INTERVAL_MINUTES", time.Minute, &cfg.Session.RotationInterval)
	r.duration("SESSION_GRACE_PERIOD_SECONDS", time.Second, &cfg.Session.GracePeriod)
	r.duration("QUERY_TIMEOUT_MS", time.Millisecond, &cfg.Query.Timeout)
	r.positiveInt("QUERY_ROW_LIMIT", &cfg.Query.MaxRows)
	r.duration("MIGRATION_LOCK_TIMEOUT_MS", time.Millisecond, &cfg.Migration.LockTimeout)
	r.duration("MIGRATION_STATEMENT_TIMEOUT_MS", time.Millisecond, &cfg.Migration.StatementTimeout)
	r.duration("MIGRATION_QUEUE_TIMEOUT_MS", time.Millisecond, &cfg.Migration.QueueTimeout)

	r.jsonList("BOOTSTRAP_ADMINS", false, &cfg.BootstrapAdmins)
	taken := r.checkAdmins(cfg.BootstrapAdmins)

	cfg.SeedDemoData = r.boolean("SEED_DEMO_DATA")
	if cfg.SeedDemoData {
		r.jsonList("SEED_DEMO_USERS", true, &cfg.DemoUsers)
		usernames := r.checkDemoUsers(cfg.DemoUsers, taken)
		r.jsonList("SEED_DEMO_GROUPS", false, &cfg.DemoGroups)
		r.checkDemoGroups(cfg.DemoGroups, usernames)

		cfg.DemoTarget = dbmanager.TargetInput{
			Name:     bootstrap.DemoTargetName,
			Host:     r.optional("TARGET_POSTGRES_HOST", "target-postgres"),
			Port:     r.port("TARGET_POSTGRES_PORT"),
			DBName:   r.required("TARGET_POSTGRES_DB"),
			Username: r.required("TARGET_APP_USER"),
			Password: r.requiredSecret("TARGET_APP_PASSWORD"),
			SSLMode:  r.sslMode("TARGET_POSTGRES_SSLMODE", "disable", "require", "verify-full"),
		}
	}
	return cfg, r.err()
}

// postgresURL builds a connection URL; url.UserPassword escapes any character in the password.
func postgresURL(user, password, host string, port int, db, sslMode string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + db,
		RawQuery: url.Values{"sslmode": {sslMode}}.Encode(),
	}
	return u.String()
}

// reader collects missing names and invalid values instead of stopping at the first one.
type reader struct {
	getenv  func(string) string
	missing []string
	errs    []error
}

func (r *reader) invalid(format string, args ...any) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

func (r *reader) err() error {
	var all []error
	if len(r.missing) > 0 {
		sort.Strings(r.missing)
		all = append(all, fmt.Errorf("missing required environment variables: %s", strings.Join(r.missing, ", ")))
	}
	return errors.Join(append(all, r.errs...)...)
}

func (r *reader) optional(name, fallback string) string {
	if v := strings.TrimSpace(r.getenv(name)); v != "" {
		return v
	}
	return fallback
}

func (r *reader) required(name string) string {
	v := strings.TrimSpace(r.getenv(name))
	if v == "" {
		r.missing = append(r.missing, name)
	}
	return v
}

// requiredSecret is required but kept as written: surrounding spaces may be part of a password.
func (r *reader) requiredSecret(name string) string {
	v := r.getenv(name)
	if strings.TrimSpace(v) == "" {
		r.missing = append(r.missing, name)
	}
	return v
}

func (r *reader) port(name string) int {
	raw := r.optional(name, "5432")
	p, err := strconv.Atoi(raw)
	if err != nil || p < 1 || p > 65535 {
		r.invalid("%s must be a port number between 1 and 65535, got %q", name, raw)
	}
	return p
}

func (r *reader) sslMode(name string, allowed ...string) string {
	v := r.optional(name, "disable")
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	r.invalid("%s must be one of %s, got %q", name, strings.Join(allowed, ", "), v)
	return v
}

func (r *reader) boolean(name string) bool {
	raw := strings.TrimSpace(r.getenv(name))
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		r.invalid("%s must be true or false, got %q", name, raw)
	}
	return v
}

// duration overrides *dst with a positive whole number of units when name is set.
func (r *reader) duration(name string, unit time.Duration, dst *time.Duration) {
	var n int
	if r.positiveInt(name, &n) {
		*dst = time.Duration(n) * unit
	}
}

// positiveInt overrides *dst when name is set and reports whether it did.
func (r *reader) positiveInt(name string, dst *int) bool {
	raw := strings.TrimSpace(r.getenv(name))
	if raw == "" {
		return false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		r.invalid("%s must be a positive whole number, got %q", name, raw)
		return false
	}
	*dst = n
	return true
}

// jsonList decodes a JSON array into dst. Unknown fields are rejected so typos do not pass silently.
func (r *reader) jsonList(name string, required bool, dst any) {
	raw := strings.TrimSpace(r.getenv(name))
	if raw == "" {
		if required {
			r.missing = append(r.missing, name)
		}
		return
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		r.invalid("%s must be a JSON array (see .env.example): %v", name, err)
		return
	}
	if dec.More() {
		r.invalid("%s must be a single JSON array", name)
	}
}

// identities tracks usernames and emails so duplicates are reported by entry.
type identities struct {
	usernames map[string]string // normalized username -> "VAR[i]"
	emails    map[string]string
}

func newIdentities() identities {
	return identities{usernames: map[string]string{}, emails: map[string]string{}}
}

// checkAccount normalizes and validates one account in place. where is e.g. "SEED_DEMO_USERS[1]".
func (r *reader) checkAccount(where string, username, email *string, password string, seen identities) {
	*username = authn.NormalizeUsername(*username)
	*email = authn.NormalizeEmail(*email)
	if err := authn.ValidateUsername(*username); err != nil {
		r.invalid("%s: username %q: %v", where, *username, err)
	}
	if err := authn.ValidateEmail(*email); err != nil {
		r.invalid("%s: email %q: %v", where, *email, err)
	}
	if err := authn.ValidatePassword(password); err != nil {
		r.invalid("%s: password: %v", where, err)
	}
	if prev, ok := seen.usernames[*username]; ok && *username != "" {
		r.invalid("%s: username %q is already used by %s", where, *username, prev)
	}
	seen.usernames[*username] = where
	if prev, ok := seen.emails[*email]; ok && *email != "" {
		r.invalid("%s: email %q is already used by %s", where, *email, prev)
	}
	seen.emails[*email] = where
}

func (r *reader) checkAdmins(admins []bootstrap.Admin) identities {
	seen := newIdentities()
	for i := range admins {
		a := &admins[i]
		r.checkAccount(fmt.Sprintf("BOOTSTRAP_ADMINS[%d]", i), &a.Username, &a.Email, a.Password, seen)
	}
	return seen
}

// checkDemoUsers validates SEED_DEMO_USERS against itself and the bootstrap admins,
// and returns the set of demo usernames for group membership checks.
func (r *reader) checkDemoUsers(users []bootstrap.DemoUser, seen identities) map[string]bool {
	names := map[string]bool{}
	for i := range users {
		u := &users[i]
		where := fmt.Sprintf("SEED_DEMO_USERS[%d]", i)
		r.checkAccount(where, &u.Username, &u.Email, u.Password, seen)
		u.Role = strings.ToUpper(strings.TrimSpace(u.Role))
		if u.Role == "" {
			u.Role = authn.RoleUser
		}
		if err := authn.ValidateRole(u.Role); err != nil {
			r.invalid("%s: role must be %s or %s, got %q", where, authn.RoleUser, authn.RolePlatformAdmin, u.Role)
		}
		names[u.Username] = true
	}
	return names
}

func (r *reader) checkDemoGroups(list []bootstrap.DemoGroup, demoUsers map[string]bool) {
	names := map[string]string{}
	codes := map[string]string{}
	for i := range list {
		g := &list[i]
		where := fmt.Sprintf("SEED_DEMO_GROUPS[%d]", i)

		g.Name = strings.TrimSpace(g.Name)
		if n := len([]rune(g.Name)); n < groups.MinGroupNameLength || n > groups.MaxGroupNameLength {
			r.invalid("%s: name must be %d-%d characters, got %q", where, groups.MinGroupNameLength, groups.MaxGroupNameLength, g.Name)
		}
		key := strings.ToLower(g.Name)
		if prev, ok := names[key]; ok {
			r.invalid("%s: name %q is already used by %s", where, g.Name, prev)
		}
		names[key] = where

		g.InviteCode = groups.NormalizeCode(g.InviteCode)
		if g.InviteCode != "" {
			if !groups.IsValidCode(g.InviteCode) {
				r.invalid("%s: invite_code %q must be 4-64 characters of A-Z, 0-9, '_' or '-', starting with a letter or digit", where, g.InviteCode)
			}
			if prev, ok := codes[g.InviteCode]; ok {
				r.invalid("%s: invite_code %q is already used by %s", where, g.InviteCode, prev)
			}
			codes[g.InviteCode] = where
		}

		g.Grant = strings.ToUpper(strings.TrimSpace(g.Grant))
		switch rbac.AccessLevel(g.Grant) {
		case "", rbac.AccessNone, rbac.AccessViewOnly, rbac.AccessViewEdit:
		default:
			r.invalid("%s: grant must be %s, %s or %s, got %q", where, rbac.AccessNone, rbac.AccessViewOnly, rbac.AccessViewEdit, g.Grant)
		}

		for j, m := range g.Members {
			m = authn.NormalizeUsername(m)
			g.Members[j] = m
			if !demoUsers[m] {
				r.invalid("%s: member %q is not in SEED_DEMO_USERS", where, m)
			}
		}
	}
}
