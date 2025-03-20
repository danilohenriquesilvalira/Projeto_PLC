// logger_repository.go
package database

import (
	"fmt"
	"time"
)

// Logger encapsula operações de logging para o banco de dados
type Logger struct {
	db *DB
}

// NewLogger cria um novo logger usando a conexão de banco existente
func NewLogger(db *DB) *Logger {
	return &Logger{db: db}
}

// logToDatabase registra uma mensagem no banco de dados
func (l *Logger) logToDatabase(level LogLevel, source, message, details string) error {
	query := `
		INSERT INTO system_logs (level, source, message, details, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`

	_, err := l.db.Exec(query, level, source, message, details)
	if err != nil {
		return fmt.Errorf("erro ao registrar log: %w", err)
	}

	return nil
}

// Info registra mensagem informativa
func (l *Logger) Info(source, message string) error {
	return l.logToDatabase(LogInfo, source, message, "")
}

// Warn registra mensagem de aviso
func (l *Logger) Warn(source, message string) error {
	return l.logToDatabase(LogWarn, source, message, "")
}

// Error registra mensagem de erro
func (l *Logger) Error(source, message string) error {
	return l.logToDatabase(LogError, source, message, "")
}

// Debug registra mensagem de depuração
func (l *Logger) Debug(source, message string) error {
	return l.logToDatabase(LogDebug, source, message, "")
}

// ErrorWithDetails registra erro com detalhes adicionais
func (l *Logger) ErrorWithDetails(source, message, details string) error {
	return l.logToDatabase(LogError, source, message, details)
}

// GetLogs recupera logs do sistema
func (l *Logger) GetLogs(level LogLevel, limit, offset int) ([]SystemLog, error) {
	var query string
	var args []interface{}

	if level != "" {
		query = `
			SELECT * FROM system_logs 
			WHERE level = $1
			ORDER BY created_at DESC 
			LIMIT $2 OFFSET $3
		`
		args = []interface{}{level, limit, offset}
	} else {
		query = `
			SELECT * FROM system_logs 
			ORDER BY created_at DESC 
			LIMIT $1 OFFSET $2
		`
		args = []interface{}{limit, offset}
	}

	var logs []SystemLog
	err := l.db.Select(&logs, query, args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar logs: %w", err)
	}

	return logs, nil
}

// GetLogsByTimeRange recupera logs em um período de tempo específico
func (l *Logger) GetLogsByTimeRange(start, end time.Time, level LogLevel) ([]SystemLog, error) {
	var query string
	var args []interface{}

	if level != "" {
		query = `
			SELECT * FROM system_logs 
			WHERE created_at BETWEEN $1 AND $2 AND level = $3
			ORDER BY created_at DESC
		`
		args = []interface{}{start, end, level}
	} else {
		query = `
			SELECT * FROM system_logs 
			WHERE created_at BETWEEN $1 AND $2
			ORDER BY created_at DESC
		`
		args = []interface{}{start, end}
	}

	var logs []SystemLog
	err := l.db.Select(&logs, query, args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar logs: %w", err)
	}

	return logs, nil
}

// GetLogsBySource recupera logs de uma fonte específica
func (l *Logger) GetLogsBySource(source string, limit, offset int) ([]SystemLog, error) {
	query := `
		SELECT * FROM system_logs 
		WHERE source = $1
		ORDER BY created_at DESC 
		LIMIT $2 OFFSET $3
	`

	var logs []SystemLog
	err := l.db.Select(&logs, query, source, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar logs: %w", err)
	}

	return logs, nil
}

// ClearOldLogs remove logs antigos do sistema
func (l *Logger) ClearOldLogs(olderThan time.Time) (int, error) {
	query := `DELETE FROM system_logs WHERE created_at < $1`

	result, err := l.db.Exec(query, olderThan)
	if err != nil {
		return 0, fmt.Errorf("erro ao limpar logs antigos: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("erro ao obter número de logs removidos: %w", err)
	}

	return int(rowsAffected), nil
}
