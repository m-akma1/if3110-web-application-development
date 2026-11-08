package http

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"dbplatform/internal/admin"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/groups"
	"dbplatform/internal/migration"
	"dbplatform/internal/rbac"
)

// Server is the HTML (GOTTH: Go + templ + htmx) front end of the platform.
type Server struct {
	mux        *http.ServeMux
	handler    http.Handler
	logger     *log.Logger
	sessions   *authn.SessionStore
	groups     *groups.Repository
	access     *rbac.Authorizer
	admin      *admin.Service
	migrations *migration.Service
	dbRegistry *dbmanager.ConnectionRegistry
}

// Deps are the services the HTTP layer needs. All are backed by the platform PostgreSQL in production.
type Deps struct {
	Sessions *authn.SessionStore // identity and session lifecycle
	Groups   *groups.Repository  // groups, membership, invite codes
	Access   *rbac.Authorizer    // every database authorization decision
	Admin    *admin.Service      // platform admin console (users, groups, grants, codes, targets)
	// Registry holds live pools to target databases; shared by the viewer, the target manager
	// and the migration engine. Nil creates an empty one.
	Registry   *dbmanager.ConnectionRegistry
	Migrations *migration.Service // drafts, dry runs, execution and history of target migrations
}

// NewServer builds the routes and the middleware chain: logger -> CSRF guard -> routes.
func NewServer(logOutput io.Writer, deps Deps) *Server {
	if logOutput == nil {
		logOutput = os.Stdout
	}

	s := &Server{
		mux:        http.NewServeMux(),
		logger:     log.New(logOutput, "", log.LstdFlags),
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
	h = CSRFGuardMiddleware(h)
	h = SanitizedLoggerMiddleware(s.logger)(h)
	s.handler = h
	return s
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
