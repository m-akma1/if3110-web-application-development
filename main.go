package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dbplatform/internal/admin"
	"dbplatform/internal/authn"
	"dbplatform/internal/bootstrap"
	"dbplatform/internal/config"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/groups"
	apphttp "dbplatform/internal/http"
	"dbplatform/internal/logging"
	"dbplatform/internal/migration"
	"dbplatform/internal/platformdb"
	"dbplatform/internal/rbac"
	"dbplatform/internal/tracing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	logger := logging.New(os.Stdout, logging.ParseLevel(os.Getenv("LOG_LEVEL")))
	slog.SetDefault(logger)
	cfg, err := config.Load()
	if err != nil {
		fatal("app.config_invalid", err)
	}
	shutdownTracing, err := tracing.Setup(context.Background(), cfg.Tracing)
	if err != nil {
		fatal("tracing.setup_failed", err)
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		fatal("platform_db.open_failed", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		fatal("platform_db.unreachable", err)
	}

	applied, err := platformdb.Migrate(ctx, db)
	if err != nil {
		fatal("platform_db.migrate_failed", err)
	}
	if len(applied) > 0 {
		slog.Info("platform_db.migrated", slog.Any("applied", applied))
	}

	cipher, err := dbmanager.NewCredentialCipher(cfg.TargetCredentialsKey)
	if err != nil {
		fatal("app.credential_key_invalid", err)
	}

	sessionStore := authn.NewPostgresSessionStore(db, cfg.Session)
	groupRepo := groups.NewRepository(db)
	grantStore := rbac.NewPostgresGrantStore(db)
	authorizer := rbac.NewAuthorizer(grantStore)
	// Target pools: the viewer, the target manager and the migration engine share one registry.
	registry := dbmanager.NewConnectionRegistryWithLimits(cfg.Query)
	registry.SetLogger(logger.With("component", "dbmanager"))
	defer registry.Close()

	targets := dbmanager.NewManager(dbmanager.NewTargetRepository(db), cipher, registry)
	adminService := admin.NewService(sessionStore, groupRepo, grantStore, targets)
	migrationEngine := migration.NewEngine(db, registry, cfg.Migration)
	migrationEngine.SetLogger(logger.With("component", "migration"))
	migrationRepo := migration.NewRepository(db)
	migrations := migration.NewService(migrationRepo, migrationEngine, authorizer, grantStore)
	worker := migration.NewWorker(migrationRepo, migrationEngine, migration.DefaultWorkerConfig())
	worker.SetLogger(logger.With("component", "worker"))

	admins, err := bootstrap.EnsureAdmin(ctx, sessionStore, cfg.BootstrapAdmins)
	if err != nil {
		fatal("bootstrap.admin_failed", err)
	}
	if len(admins) > 0 {
		slog.Info("bootstrap.admin_created", slog.Any("admins", admins))
	}

	if cfg.SeedDemoData {
		seeded, err := bootstrap.SeedDemo(ctx, sessionStore, groupRepo, cfg.DemoUsers, cfg.DemoGroups)
		if err != nil {
			fatal("bootstrap.seed_demo_failed", err)
		}
		if len(seeded) > 0 {
			slog.Info("bootstrap.demo_seeded", slog.Any("items", seeded))
		}
		registered, err := bootstrap.SeedDemoTarget(ctx, targets, grantStore, groupRepo, cfg.DemoTarget, cfg.DemoGroups)
		if err != nil {
			fatal("bootstrap.seed_demo_target_failed", err)
		}
		if registered {
			slog.Info("bootstrap.demo_target_registered", slog.String("name", bootstrap.DemoTargetName))
		}
	}

	// Every group with database access needs an active leader to approve changes.
	if err := groupRepo.EnsureLeaders(ctx); err != nil {
		slog.Warn("groups.ensure_leaders_failed", slog.String("error", err.Error()))
	}
	if leader, err := bootstrap.EnsurePublicLeader(ctx, sessionStore, groupRepo, cfg.BootstrapAdmins); err != nil {
		slog.Warn("groups.public_leader_failed", slog.String("error", err.Error()))
	} else if leader != "" {
		slog.Info("groups.public_leader_set", slog.String("leader", leader))
	}
	if leaderless, err := groupRepo.LeaderlessWithGrants(ctx); err == nil && len(leaderless) > 0 {
		names := make([]string, 0, len(leaderless))
		for _, g := range leaderless {
			names = append(names, g.Name)
		}
		slog.Warn("groups.leaderless_with_grants", slog.Any("groups", names))
	}

	// Connect every registered target. Unreachable targets are marked ERROR, never fatal.
	loadCtx, cancelLoad := context.WithTimeout(context.Background(), 60*time.Second)
	connected, failed, err := targets.LoadAll(loadCtx)
	cancelLoad()
	if err != nil {
		slog.Warn("targets.load_failed", slog.String("error", err.Error()))
	} else {
		slog.Info("targets.loaded", slog.Int("connected", connected), slog.Int("failed", failed))
	}

	// The platform DB (users, sessions, groups) is never registered as a viewer target.
	srv := apphttp.NewServer(os.Stdout, apphttp.Deps{
		Logger:     logger.With("component", "http"),
		Sessions:   sessionStore,
		Groups:     groupRepo,
		Access:     authorizer,
		Admin:      adminService,
		Registry:   registry,
		Migrations: migrations,
	})

	// SIGINT/SIGTERM (Ctrl+C, docker stop) cancel runCtx: stop taking new requests and jobs,
	// let running jobs finish, then exit.
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerDone := make(chan struct{})
	go func() {
		worker.Run(runCtx)
		close(workerDone)
	}()

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- httpSrv.ListenAndServe() }()
	slog.Info("server.started", slog.String("addr", cfg.ListenAddr))

	select {
	case err := <-serverErr:
		fatal("server.stopped", err)
	case <-runCtx.Done():
	}
	slog.Info("server.shutting_down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server.shutdown_failed", slog.String("error", err.Error()))
	}
	<-workerDone
	// Last: running jobs have ended their spans by now; flush them to the exporter.
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFlush()
	if err := shutdownTracing(flushCtx); err != nil {
		slog.Warn("tracing.shutdown_failed", slog.String("error", err.Error()))
	}
	slog.Info("server.stopped")
}

// fatal logs an error event and stops the process. Like log.Fatal, deferred calls do not run.
func fatal(event string, err error) {
	slog.Error(event, slog.String("error", err.Error()))
	os.Exit(1)
}
