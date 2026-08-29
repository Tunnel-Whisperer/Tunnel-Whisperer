package dashboard

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// loginData is passed to the login template.
type loginData struct {
	Error string
	Next  string
}

// handleLogin renders the token form (GET) and establishes a session on a
// correct token (POST), mirroring Headlamp's paste-your-token login.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.isAuthenticated(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		s.renderLogin(w, "", safeNext(r.URL.Query().Get("next")))
	case http.MethodPost:
		if !tokenValid(r.FormValue("token")) {
			w.WriteHeader(http.StatusUnauthorized)
			s.renderLogin(w, "That token is not valid.", safeNext(r.FormValue("next")))
			return
		}
		cur, err := readDashboardToken()
		if err != nil || cur == "" {
			slog.Error("reading dashboard token at login", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		id, err := s.sessions.create(cur)
		if err != nil {
			slog.Error("creating dashboard session", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    id,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			// Mark the cookie Secure whenever the request arrived over TLS
			// (directly or via a terminating proxy), so it is never sent back
			// over cleartext once TLS is in use (finding SP-8).
			Secure: isRequestTLS(r),
		})
		http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// isRequestTLS reports whether the request reached us over TLS, either directly
// or through a terminating reverse proxy that set X-Forwarded-Proto.
func isRequestTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// handleLogout clears the session and returns to the login page.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLogin(w http.ResponseWriter, errMsg, next string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.loginTmpl.Execute(w, loginData{Error: errMsg, Next: next}); err != nil {
		slog.Error("rendering login page", "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// safeNext keeps post-login redirects on this site: a local absolute path only,
// never a scheme/host or protocol-relative URL that could be an open redirect.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return next
}

func safeNextEscape(next string) string {
	return url.QueryEscape(safeNext(next))
}
