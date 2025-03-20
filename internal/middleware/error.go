package middleware

import (
	"encoding/json"
	"log"
	"net/http"
)

// ErrorHandler centraliza o tratamento de erros da aplicação
func ErrorHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log do erro
				log.Printf("Panic recuperado em %s: %v", r.URL.Path, err)

				// Retorna erro 500 para o cliente
				http.Error(w, "Erro interno do servidor", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// CustomError representa um erro personalizado da API
type CustomError struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
}

// NewError cria um novo erro personalizado
func NewError(status int, message string) *CustomError {
	return &CustomError{
		StatusCode: status,
		Message:    message,
	}
}

// WriteError escreve o erro na resposta HTTP
func WriteError(w http.ResponseWriter, err *CustomError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.StatusCode)
	json.NewEncoder(w).Encode(err)
}
