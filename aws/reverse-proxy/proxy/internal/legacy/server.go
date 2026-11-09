package legacy

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
)

//go:embed public/index.html
var publicFS embed.FS

type server struct {
	logger    *slog.Logger
	templates struct {
		users *template.Template
	}
}

func StartServer(logger *slog.Logger, addr string) error {
	mux := http.NewServeMux()

	usersTmpl, err := template.ParseFS(publicFS, "public/index.html")
	if err != nil {
		return err
	}

	srv := &server{
		logger: logger,
		templates: struct{ users *template.Template }{
			users: usersTmpl,
		},
	}

	mux.HandleFunc("GET /api", srv.handleAPIRoot)
	mux.HandleFunc("GET /api/users", srv.handleUsers)
	mux.HandleFunc("GET /web/users", srv.handleWebUsers)

	return http.ListenAndServe(addr, srv.logRequest(mux))
}

func (s *server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.logger.Info("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("request_id", r.Header.Get("X-Request-Id")),
			slog.String("remote_addr", r.RemoteAddr),
			slog.Group("request",
				slog.Any("headers", r.Header),
			),
		)

		next.ServeHTTP(w, r)
	})
}

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *server) handleWebUsers(w http.ResponseWriter, req *http.Request) {
	users, err := fetchUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.users.Execute(w, users); err != nil {
		s.logger.Error("render users failed", "err", err)
	}
}

func (s *server) handleAPIRoot(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func (s *server) handleUsers(w http.ResponseWriter, req *http.Request) {
	users, err := fetchUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	body, err := json.Marshal(users)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func fetchUsers() ([]User, error) {
	return []User{
		{
			ID:   "1",
			Name: "John",
		},
		{
			ID:   "2",
			Name: "Marya",
		},
	}, nil
}
