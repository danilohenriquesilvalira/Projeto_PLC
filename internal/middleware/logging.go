package middleware

import (
	"log"
	"net/http"
	"time"
)

// RequestLogger retorna um middleware que loga informações sobre as requisições
func RequestLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Log da requisição
			log.Printf("Iniciando %s %s", r.Method, r.URL.Path)

			next.ServeHTTP(w, r)

			// Log do tempo de execução
			duration := time.Since(start)
			log.Printf("Completado %s %s em %v", r.Method, r.URL.Path, duration)
		})
	}
}
