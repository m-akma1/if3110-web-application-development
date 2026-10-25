package config

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/authn"
	"dbplatform/internal/migration"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// minimal is the smallest environment that loads.
func minimal(extra map[string]string) map[string]string {
	m := map[string]string{
		"POSTGRES_USER":          "platform",
		"POSTGRES_PASSWORD":      "secret",
		"POSTGRES_DB":            "core",
		"TARGET_CREDENTIALS_KEY": "a2V5",
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// withSeed adds a valid demo seed on top of minimal.
func withSeed(extra map[string]string) map[string]string {
	m := minimal(map[string]string{
		"SEED_DEMO_DATA":      "true",
		"SEED_DEMO_USERS":     `[{"username":"alice","password":"AlicePass123","role":"PLATFORM_ADMIN","email":"alice@example.com"},{"username":"bob","password":"BobPass123"}]`,
		"SEED_DEMO_GROUPS":    `[{"name":"Engineers","invite_code":"eng-demo","grant":"view_edit","members":["Bob"]}]`,
		"TARGET_POSTGRES_DB":  "demo",
		"TARGET_APP_USER":     "app_target",
		"TARGET_APP_PASSWORD": "target-secret",
	})
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func mustFail(t *testing.T, env map[string]string, wants ...string) {
	t.Helper()
	_, err := load(envOf(env))
	if err == nil {
		t.Fatalf("configuration should be rejected (want %v)", wants)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error should mention %q, got:\n%v", w, err)
		}
	}
}

func TestLoad_ListsEveryMissingVariable(t *testing.T) {
	_, err := load(envOf(map[string]string{}))
	if err == nil {
		t.Fatal("an empty environment should be rejected")
	}
	want := "missing required environment variables: POSTGRES_DB, POSTGRES_PASSWORD, POSTGRES_USER, TARGET_CREDENTIALS_KEY"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q, got:\n%v", want, err)
	}
}

func TestLoad_BuildsDatabaseURL(t *testing.T) {
	cfg, err := load(envOf(minimal(map[string]string{"POSTGRES_PASSWORD": "p@ss:w/rd#?%"})))
	if err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("built URL does not parse: %v", err)
	}
	pw, _ := u.User.Password()
	if pw != "p@ss:w/rd#?%" || u.User.Username() != "platform" || u.Host != "postgres:5432" ||
		u.Path != "/core" || u.Query().Get("sslmode") != "disable" {
		t.Fatalf("URL parts wrong: %s", cfg.DatabaseURL)
	}

	cfg, err = load(envOf(minimal(map[string]string{"POSTGRES_HOST": "localhost", "POSTGRES_PORT": "5433", "POSTGRES_SSLMODE": "require"})))
	if err != nil || !strings.Contains(cfg.DatabaseURL, "@localhost:5433/core?sslmode=require") {
		t.Fatalf("host, port and sslmode should be applied: %s, %v", cfg.DatabaseURL, err)
	}
	mustFail(t, minimal(map[string]string{"POSTGRES_PORT": "70000"}), "POSTGRES_PORT")
	mustFail(t, minimal(map[string]string{"POSTGRES_SSLMODE": "sometimes"}), "POSTGRES_SSLMODE")
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := load(envOf(minimal(nil)))
	if err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	if cfg.ListenAddr != ":8080" || cfg.SeedDemoData || len(cfg.BootstrapAdmins) != 0 {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	if cfg.Session != authn.DefaultSessionConfig() || cfg.Migration != migration.DefaultLimits() ||
		cfg.Query.Timeout != 5*time.Second || cfg.Query.MaxRows != 500 {
		t.Fatalf("limit defaults wrong: %+v", cfg)
	}
}

func TestLoad_Limits(t *testing.T) {
	cfg, err := load(envOf(minimal(map[string]string{
		"SESSION_IDLE_TIMEOUT_MINUTES":      "10",
		"SESSION_ABSOLUTE_EXPIRATION_HOURS": "2",
		"SESSION_ROTATION_INTERVAL_MINUTES": "5",
		"SESSION_GRACE_PERIOD_SECONDS":      "3",
		"QUERY_TIMEOUT_MS":                  "1500",
		"QUERY_ROW_LIMIT":                   "42",
		"MIGRATION_LOCK_TIMEOUT_MS":         "2500",
		"MIGRATION_STATEMENT_TIMEOUT_MS":    "30000",
		"MIGRATION_QUEUE_TIMEOUT_MS":        "1000",
	})))
	if err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	if cfg.Session != (authn.SessionConfig{IdleTimeout: 10 * time.Minute, AbsoluteExpiration: 2 * time.Hour,
		RotationInterval: 5 * time.Minute, GracePeriod: 3 * time.Second}) {
		t.Fatalf("session limits not read: %+v", cfg.Session)
	}
	if cfg.Query.Timeout != 1500*time.Millisecond || cfg.Query.MaxRows != 42 {
		t.Fatalf("query limits not read: %+v", cfg.Query)
	}
	if cfg.Migration != (migration.Limits{LockTimeout: 2500 * time.Millisecond, StatementTimeout: 30 * time.Second, QueueTimeout: time.Second}) {
		t.Fatalf("migration limits not read: %+v", cfg.Migration)
	}

	for _, name := range []string{"SESSION_IDLE_TIMEOUT_MINUTES", "QUERY_ROW_LIMIT", "QUERY_TIMEOUT_MS", "MIGRATION_STATEMENT_TIMEOUT_MS"} {
		for _, bad := range []string{"0", "-5", "abc", "1.5"} {
			mustFail(t, minimal(map[string]string{name: bad}), name)
		}
	}
}

func TestLoad_InvalidBool(t *testing.T) {
	mustFail(t, minimal(map[string]string{"SEED_DEMO_DATA": "maybe"}), "SEED_DEMO_DATA")
}

func TestLoad_BootstrapAdmins(t *testing.T) {
	cfg, err := load(envOf(minimal(map[string]string{
		"BOOTSTRAP_ADMINS": `[{"username":" Root ","password":"RootPassword1","email":"ROOT@example.com"},{"username":"ops","password":"OpsPassword1"}]`,
	})))
	if err != nil {
		t.Fatalf("valid admins rejected: %v", err)
	}
	if len(cfg.BootstrapAdmins) != 2 || cfg.BootstrapAdmins[0].Username != "root" || cfg.BootstrapAdmins[0].Email != "root@example.com" {
		t.Fatalf("admins should be parsed and normalized: %+v", cfg.BootstrapAdmins)
	}

	mustFail(t, minimal(map[string]string{"BOOTSTRAP_ADMINS": `{"username":"root"}`}), "BOOTSTRAP_ADMINS must be a JSON array")
	mustFail(t, minimal(map[string]string{"BOOTSTRAP_ADMINS": `[{"username":"root","password":"RootPassword1","role":"USER"}]`}), `unknown field "role"`)
	mustFail(t, minimal(map[string]string{"BOOTSTRAP_ADMINS": `[{"username":"root","password":"short"}]`}), "BOOTSTRAP_ADMINS[0]: password")
	mustFail(t, minimal(map[string]string{"BOOTSTRAP_ADMINS": `[{"username":"x","password":"RootPassword1"}]`}), "BOOTSTRAP_ADMINS[0]: username")
	mustFail(t, minimal(map[string]string{
		"BOOTSTRAP_ADMINS": `[{"username":"root","password":"RootPassword1"},{"username":"ROOT","password":"RootPassword2"}]`,
	}), `BOOTSTRAP_ADMINS[1]: username "root" is already used by BOOTSTRAP_ADMINS[0]`)
}

func TestLoad_SeedIgnoredWhenOff(t *testing.T) {
	cfg, err := load(envOf(minimal(map[string]string{"SEED_DEMO_DATA": "false", "SEED_DEMO_USERS": "not json"})))
	if err != nil || len(cfg.DemoUsers) != 0 {
		t.Fatalf("seed variables should be ignored when SEED_DEMO_DATA=false: %+v, %v", cfg.DemoUsers, err)
	}
}

func TestLoad_Seed(t *testing.T) {
	cfg, err := load(envOf(withSeed(nil)))
	if err != nil {
		t.Fatalf("valid seed rejected: %v", err)
	}
	if len(cfg.DemoUsers) != 2 || cfg.DemoUsers[1].Role != authn.RoleUser {
		t.Fatalf("demo users should be parsed with USER as default role: %+v", cfg.DemoUsers)
	}
	g := cfg.DemoGroups[0]
	if g.InviteCode != "ENG-DEMO" || g.Grant != "VIEW_EDIT" || g.Members[0] != "bob" {
		t.Fatalf("demo group should be normalized: %+v", g)
	}
	tgt := cfg.DemoTarget
	if tgt.Host != "target-postgres" || tgt.Port != 5432 || tgt.DBName != "demo" || tgt.Username != "app_target" ||
		tgt.Password != "target-secret" || tgt.SSLMode != "disable" {
		t.Fatalf("demo target should be built from TARGET_*: %+v", tgt)
	}
}

func TestLoad_SeedRequiresUsersAndTarget(t *testing.T) {
	mustFail(t, minimal(map[string]string{"SEED_DEMO_DATA": "true"}),
		"missing required environment variables: SEED_DEMO_USERS, TARGET_APP_PASSWORD, TARGET_APP_USER, TARGET_POSTGRES_DB")
}

func TestLoad_SeedRejectsBadEntries(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"bad role":         {map[string]string{"SEED_DEMO_USERS": `[{"username":"alice","password":"AlicePass123","role":"ROOT"}]`, "SEED_DEMO_GROUPS": ""}, "SEED_DEMO_USERS[0]: role"},
		"duplicate email":  {map[string]string{"SEED_DEMO_USERS": `[{"username":"alice","password":"AlicePass123","email":"a@example.com"},{"username":"bob","password":"BobPass123","email":"A@example.com"}]`, "SEED_DEMO_GROUPS": ""}, `SEED_DEMO_USERS[1]: email "a@example.com" is already used by SEED_DEMO_USERS[0]`},
		"clashes w/ admin": {map[string]string{"BOOTSTRAP_ADMINS": `[{"username":"alice","password":"RootPassword1"}]`}, `SEED_DEMO_USERS[0]: username "alice" is already used by BOOTSTRAP_ADMINS[0]`},
		"unknown member":   {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"Engineers","members":["carol"]}]`}, `SEED_DEMO_GROUPS[0]: member "carol" is not in SEED_DEMO_USERS`},
		"bad grant":        {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"Engineers","grant":"ADMIN"}]`}, "SEED_DEMO_GROUPS[0]: grant"},
		"bad invite code":  {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"Engineers","invite_code":"a b"}]`}, "SEED_DEMO_GROUPS[0]: invite_code"},
		"short group name": {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"E"}]`}, "SEED_DEMO_GROUPS[0]: name"},
		"duplicate group":  {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"Engineers"},{"name":"engineers"}]`}, "SEED_DEMO_GROUPS[1]: name"},
		"duplicate code":   {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"One","invite_code":"SAME-CODE"},{"name":"Two","invite_code":"same-code"}]`}, "SEED_DEMO_GROUPS[1]: invite_code"},
		"bad target port":  {map[string]string{"TARGET_POSTGRES_PORT": "abc"}, "TARGET_POSTGRES_PORT"},
		"bad target ssl":   {map[string]string{"TARGET_POSTGRES_SSLMODE": "prefer"}, "TARGET_POSTGRES_SSLMODE"},
		"unknown seed key": {map[string]string{"SEED_DEMO_GROUPS": `[{"name":"Engineers","members":["bob"],"memebers":[]}]`}, `unknown field "memebers"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { mustFail(t, withSeed(c.env), c.want) })
	}
}

func TestLoad_SeedEmptyUserList(t *testing.T) {
	// "[]" is set but empty: demo seeding with no users is pointless, but not an error by itself.
	cfg, err := load(envOf(withSeed(map[string]string{"SEED_DEMO_USERS": `[]`, "SEED_DEMO_GROUPS": `[{"name":"Engineers"}]`})))
	if err != nil || len(cfg.DemoUsers) != 0 || len(cfg.DemoGroups) != 1 {
		t.Fatalf("an empty user list with member-less groups should load: %+v, %v", cfg, err)
	}
}
