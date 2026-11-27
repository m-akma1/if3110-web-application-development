package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"dbplatform/internal/admin"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/groups"
	"dbplatform/internal/logging"
	"dbplatform/internal/migration"
	"dbplatform/internal/rbac"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Server is the HTTP side of the platform: the JSON API under /api and the host of the SPA.
type Server struct {
	mux        *http.ServeMux
	handler    http.Handler
	logger     *slog.Logger
	sessions   *authn.SessionStore
	groups     *groups.Repository
	access     *rbac.Authorizer
	admin      *admin.Service
	migrations *migration.Service
	dbRegistry *dbmanager.ConnectionRegistry
}

// Deps are the services the HTTP layer needs. All are backed by the platform PostgreSQL in production.
type Deps struct {
	Logger   *slog.Logger
	Sessions *authn.SessionStore // identity and session lifecycle
	Groups   *groups.Repository  // groups, membership, invite codes
	Access   *rbac.Authorizer    // every database authorization decision
	Admin    *admin.Service      // platform admin console (users, groups, grants, codes, targets)
	// Registry holds live pools to target databases; shared by the viewer, the target manager
	// and the migration engine. Nil creates an empty one.
	Registry   *dbmanager.ConnectionRegistry
	Migrations *migration.Service // drafts, dry runs, execution and history of target migrations
}

// NewServer builds the routes and the middleware chain:
// tracing -> request context -> access log -> CSRF guard -> span name -> routes.
func NewServer(logOutput io.Writer, deps Deps) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}

	logger := deps.Logger
	if logger == nil {
		logger = logging.New(logOutput, slog.LevelInfo)
	}

	s := &Server{
		mux:        http.NewServeMux(),
		logger:     logger,
		sessions:   deps.Sessions,
		groups:     deps.Groups,
		access:     deps.Access,
		admin:      deps.Admin,
		migrations: deps.Migrations,
		dbRegistry: deps.Registry,
	}
	if s.dbRegistry == nil {
		s.dbRegistry = dbmanager.NewConnectionRegistry()
	}
	s.registerRoutes()

	var h http.Handler = s.mux
	h = spanNameMiddleware(s.mux, h)
	h = CSRFGuardMiddleware(h)
	h = SanitizedLoggerMiddleware(s.logger)(h)
	h = RequestContextMiddleware(h)
	// Outermost, so every record the request logs (including http.request) is inside its span.
	// Only API calls are traced. The API is public, so a traceparent sent by a client starts a
	// new trace (linked to it) instead of being trusted as the parent.
	h = otelhttp.NewHandler(h, "http.request",
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
		otelhttp.WithFilter(func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }))
	s.handler = h
	return s
}

// spanNameMiddleware names the request's span after its route pattern, e.g.
// "POST /api/migration/{id}/execute". otelhttp only reads the pattern from its own copy of the
// request, which the middlewares in between replace, so the name is set here.
func spanNameMiddleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			span := trace.SpanFromContext(r.Context())
			span.SetName(pattern)
			span.SetAttributes(attribute.String("http.route", pattern))
		}
		next.ServeHTTP(w, r)
	})
}

// Sessions returns the server's session store.
func (s *Server) Sessions() *authn.SessionStore {
	return s.sessions
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Static assets
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))

	// Vite SPA Assets
	for _, assetsDir := range []string{"./dist/assets", "./frontend/dist/assets"} {
		if _, err := os.Stat(assetsDir); err == nil {
			s.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(assetsDir))))
			break
		}
	}

	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.registerAPIRoutes()

	// SPA fallback: all page requests serve index.html
	s.mux.HandleFunc("GET /", s.handleSPA)
}

func findDistDir() string {
	for _, dir := range []string{"./dist", "./frontend/dist"} {
		if _, err := os.Stat(dir + "/index.html"); err == nil {
			return dir
		}
	}
	return ""
}

func isBrowserNavigate(r *http.Request) bool {
	ua := r.Header.Get("User-Agent")
	return strings.Contains(ua, "Mozilla")
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	dist := findDistDir()
	if dist != "" {
		http.ServeFile(w, r, dist+"/index.html")
		return
	}
	http.Error(w, "Frontend SPA belum di-build. Jalankan `cd frontend && npm run build`", http.StatusNotFound)
}

// handleHealth is a liveness check for load balancers and monitoring.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "db-management-platform",
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
