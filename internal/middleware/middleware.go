package middleware

import (
	"net/http"

	"github.com/golang-jwt/jwt"
)

// Tipos comuns usados pelos middlewares
type contextKey string

const (
	UserContextKey contextKey = "user"
	RequestIDKey   contextKey = "request_id"
)

// Claims representa os dados que serão codificados no token JWT
type Claims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.StandardClaims
}

// MiddlewareFunc é o tipo base para todos os middlewares
type MiddlewareFunc func(http.Handler) http.Handler

// Chain permite encadear múltiplos middlewares
func Chain(h http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for _, m := range middlewares {
		h = m(h)
	}
	return h
}
