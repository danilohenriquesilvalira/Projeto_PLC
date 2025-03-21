// logger_repository.go
package database

import (
	"fmt"
	"sync"
	"time"
)

// Logger encapsula operações de logging para o banco de dados
type Logger struct {
	db         *DB
	bufferSize int
	buffer     []SystemLog
	mutex      sync.Mutex

	// Configurações de limite de logging
	loggingThresholds     map[string]time.Duration // Mapa de fonte -> duração mínima entre logs
	lastLogTimestamps     map[string]time.Time     // Último timestamp de log por fonte
	maxDuplicateThreshold int                      // Número máximo de logs duplicados antes de suprimir
	duplicateCounters     map[string]int           // Contador de mensagens duplicadas
	duplicateMessages     map[string]time.Time     // Mensagens duplicadas e quando ocorreram pela primeira vez

	// Nível mínimo de log para armazenar no banco de dados
	minLogLevel LogLevel
}

// NewLogger cria um novo logger usando a conexão de banco existente
func NewLogger(db *DB) *Logger {
	return &Logger{
		db:                    db,
		bufferSize:            100, // Buffer para 100 logs antes de fazer flush para o banco
		buffer:                make([]SystemLog, 0, 100),
		loggingThresholds:     make(map[string]time.Duration),
		lastLogTimestamps:     make(map[string]time.Time),
		maxDuplicateThreshold: 5, // Suprimir depois de 5 mensagens duplicadas
		duplicateCounters:     make(map[string]int),
		duplicateMessages:     make(map[string]time.Time),
		minLogLevel:           LogInfo, // Por padrão, armazena INFO e acima no banco
	}
}

// SetMinLogLevel define o nível mínimo de log para armazenar no banco
func (l *Logger) SetMinLogLevel(level LogLevel) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.minLogLevel = level
}

// SetLoggingThreshold define um intervalo mínimo entre logs para uma fonte específica
func (l *Logger) SetLoggingThreshold(source string, minInterval time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.loggingThresholds[source] = minInterval
}

// shouldLog verifica se um log deve ser registrado com base em thresholds e duplicação
// Retorna se deve registrar e uma mensagem possivelmente modificada
func (l *Logger) shouldLog(level LogLevel, source, message string) (bool, string) {
	// Verifica se o nível de log é suficiente
	if level == LogDebug && l.minLogLevel != LogDebug {
		return false, message
	}

	// Cria uma chave única para esta combinação de fonte e mensagem
	key := fmt.Sprintf("%s:%s", source, message)

	// Verifica se esta mensagem está sendo repetida com frequência
	if timestamp, exists := l.duplicateMessages[key]; exists {
		l.duplicateCounters[key]++

		// Se exceder o limite, não registra
		if l.duplicateCounters[key] > l.maxDuplicateThreshold {
			// A cada 100 mensagens duplicadas, registramos um resumo
			if l.duplicateCounters[key]%100 == 0 {
				timeSince := time.Since(timestamp)
				summaryMessage := fmt.Sprintf("Mensagem repetida %d vezes nos últimos %s: %s",
					l.duplicateCounters[key], timeSince.Round(time.Second), message)

				// Substituímos a mensagem original com este resumo
				return true, summaryMessage
			}
			return false, message
		}
	} else {
		// Nova mensagem, inicializa contadores
		l.duplicateMessages[key] = time.Now()
		l.duplicateCounters[key] = 1
	}

	// Verifica thresholds por fonte
	if threshold, exists := l.loggingThresholds[source]; exists {
		lastTimestamp, hasTimestamp := l.lastLogTimestamps[source]

		if hasTimestamp && time.Since(lastTimestamp) < threshold {
			// Ainda dentro do período de threshold, não registra
			return false, message
		}

		// Atualiza o timestamp
		l.lastLogTimestamps[source] = time.Now()
	}

	return true, message
}

// flushLogs envia os logs armazenados em buffer para o banco de dados
func (l *Logger) flushLogs() error {
	if len(l.buffer) == 0 {
		return nil
	}

	// Inicia uma transação para inserir todos os logs de uma vez
	tx, err := l.db.BeginTx()
	if err != nil {
		return fmt.Errorf("erro ao iniciar transação para flush de logs: %w", err)
	}

	query := `
		INSERT INTO system_logs (level, source, message, details, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	for _, log := range l.buffer {
		_, err := tx.Exec(query, log.Level, log.Source, log.Message, log.Details, log.CreatedAt)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("erro ao inserir log: %w", err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("erro ao commit de logs: %w", err)
	}

	// Limpa o buffer após o flush bem-sucedido
	l.buffer = l.buffer[:0]
	return nil
}

// logToDatabase registra uma mensagem no banco de dados
func (l *Logger) logToDatabase(level LogLevel, source, message, details string) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	// Verifica se este log deve ser registrado
	shouldLogMsg, modifiedMessage := l.shouldLog(level, source, message)
	if !shouldLogMsg {
		return nil
	}

	// Cria o registro de log com a mensagem potencialmente modificada
	logEntry := SystemLog{
		Level:     string(level),
		Source:    source,
		Message:   modifiedMessage,
		Details:   details,
		CreatedAt: time.Now(),
	}

	// Adiciona ao buffer
	l.buffer = append(l.buffer, logEntry)

	// Se o buffer atingiu o tamanho limite, faz flush
	if len(l.buffer) >= l.bufferSize {
		return l.flushLogs()
	}

	return nil
}

// Info registra mensagem informativa
func (l *Logger) Info(source, message string) error {
	return l.logToDatabase(LogInfo, source, message, "")
}

// InfoWithDetails registra mensagem informativa com detalhes adicionais
func (l *Logger) InfoWithDetails(source, message, details string) error {
	return l.logToDatabase(LogInfo, source, message, details)
}

// Warn registra mensagem de aviso
func (l *Logger) Warn(source, message string) error {
	return l.logToDatabase(LogWarn, source, message, "")
}

// WarnWithDetails registra aviso com detalhes adicionais
func (l *Logger) WarnWithDetails(source, message, details string) error {
	return l.logToDatabase(LogWarn, source, message, details)
}

// Error registra mensagem de erro
func (l *Logger) Error(source, message string) error {
	return l.logToDatabase(LogError, source, message, "")
}

// ErrorWithDetails registra erro com detalhes adicionais
func (l *Logger) ErrorWithDetails(source, message, details string) error {
	return l.logToDatabase(LogError, source, message, details)
}

// Debug registra mensagem de depuração
func (l *Logger) Debug(source, message string) error {
	return l.logToDatabase(LogDebug, source, message, "")
}

// DebugWithDetails registra depuração com detalhes adicionais
func (l *Logger) DebugWithDetails(source, message, details string) error {
	return l.logToDatabase(LogDebug, source, message, details)
}

// GetLogs recupera logs do sistema
func (l *Logger) GetLogs(level LogLevel, limit, offset int) ([]SystemLog, error) {
	// Faz flush dos logs em buffer antes de consultar
	l.mutex.Lock()
	if err := l.flushLogs(); err != nil {
		l.mutex.Unlock()
		return nil, fmt.Errorf("erro ao fazer flush de logs: %w", err)
	}
	l.mutex.Unlock()

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
	// Faz flush dos logs em buffer antes de consultar
	l.mutex.Lock()
	if err := l.flushLogs(); err != nil {
		l.mutex.Unlock()
		return nil, fmt.Errorf("erro ao fazer flush de logs: %w", err)
	}
	l.mutex.Unlock()

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
	// Faz flush dos logs em buffer antes de consultar
	l.mutex.Lock()
	if err := l.flushLogs(); err != nil {
		l.mutex.Unlock()
		return nil, fmt.Errorf("erro ao fazer flush de logs: %w", err)
	}
	l.mutex.Unlock()

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
	// Faz flush dos logs em buffer antes de limpar
	l.mutex.Lock()
	if err := l.flushLogs(); err != nil {
		l.mutex.Unlock()
		return 0, fmt.Errorf("erro ao fazer flush de logs: %w", err)
	}
	l.mutex.Unlock()

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

// ResetDuplicateCounters limpa os contadores de mensagens duplicadas
func (l *Logger) ResetDuplicateCounters() {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.duplicateCounters = make(map[string]int)
	l.duplicateMessages = make(map[string]time.Time)
}
