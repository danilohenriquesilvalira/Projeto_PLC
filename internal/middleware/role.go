package middleware

import (
	"net/http"
)

// RequireRole garante que o usuário tenha uma das roles permitidas
func RequireRole(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(UserContextKey).(*Claims)
			if !ok {
				http.Error(w, "Não autorizado", http.StatusUnauthorized)
				return
			}

			// Se for superadmin, permitir tudo
			if claims.Role == "superadmin" {
				next(w, r)
				return
			}

			for _, role := range roles {
				if claims.Role == role {
					next(w, r)
					return
				}
			}

			http.Error(w, "Permissão negada", http.StatusForbidden)
		}
	}
}
