package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// AuditEntry representa uma entrada no log de auditoria
type AuditEntry struct {
	ID         int64       `json:"id" db:"id"`
	Timestamp  time.Time   `json:"timestamp" db:"timestamp"`
	UserID     int         `json:"user_id" db:"user_id"`
	Username   string      `json:"username" db:"username"`
	Action     string      `json:"action" db:"action"`
	Target     string      `json:"target" db:"target"`
	TargetID   int         `json:"target_id" db:"target_id"`
	TargetName string      `json:"target_name" db:"target_name"`
	OldValue   interface{} `json:"old_value"`
	NewValue   interface{} `json:"new_value"`
	Reason     string      `json:"reason" db:"reason"`
	IPAddress  string      `json:"ip_address" db:"ip_address"`
	Status     string      `json:"status" db:"status"`
	Details    string      `json:"details" db:"details"`
}

// WriteAuditor registra todas as operações de escrita para auditoria
type WriteAuditor struct {
	db           *sql.DB
	queue        chan AuditEntry
	batchSize    int
	flushTimeout time.Duration
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	tableName    string
}

// NewWriteAuditor cria um novo auditor de escritas
func NewWriteAuditor(ctx context.Context, db *sql.DB, tableName string) *WriteAuditor {
	auditCtx, cancel := context.WithCancel(ctx)

	auditor := &WriteAuditor{
		db:           db,
		queue:        make(chan AuditEntry, 1000), // Buffer para 1000 entradas
		batchSize:    100,                         // Salvar em lotes de 100 entradas
		flushTimeout: 5 * time.Second,             // Ou a cada 5 segundos
		ctx:          auditCtx,
		cancel:       cancel,
		tableName:    tableName,
	}

	// Iniciar goroutine para processar a fila
	auditor.wg.Add(1)
	go auditor.processQueue()

	return auditor
}

// LogEntry registra uma entrada de auditoria
func (wa *WriteAuditor) LogEntry(entry AuditEntry) error {
	// Validação básica
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	if entry.Action == "" {
		return fmt.Errorf("ação de auditoria não pode ser vazia")
	}

	// Converter valores para JSON antes de enfileirar
	if entry.OldValue != nil {
		oldJSON, err := json.Marshal(entry.OldValue)
		if err != nil {
			log.Printf("Erro ao serializar OldValue: %v", err)
			entry.OldValue = fmt.Sprintf("Erro ao serializar: %v", err)
		} else {
			entry.OldValue = string(oldJSON)
		}
	}

	if entry.NewValue != nil {
		newJSON, err := json.Marshal(entry.NewValue)
		if err != nil {
			log.Printf("Erro ao serializar NewValue: %v", err)
			entry.NewValue = fmt.Sprintf("Erro ao serializar: %v", err)
		} else {
			entry.NewValue = string(newJSON)
		}
	}

	// Enfileirar a entrada (não bloqueante com timeout)
	select {
	case wa.queue <- entry:
		return nil
	case <-time.After(1 * time.Second):
		return fmt.Errorf("timeout ao enfileirar entrada de auditoria: fila cheia")
	}
}

// Close fecha o auditor e salva todas as entradas pendentes
func (wa *WriteAuditor) Close() error {
	// Sinalizar que não haverá mais entradas
	wa.cancel()

	// Aguardar processamento de todas as entradas na fila
	waitChan := make(chan struct{})
	go func() {
		wa.wg.Wait()
		close(waitChan)
	}()

	// Aguardar com timeout
	select {
	case <-waitChan:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout ao fechar auditor: entradas pendentes não processadas")
	}
}

// processQueue processa entradas da fila em lotes
func (wa *WriteAuditor) processQueue() {
	defer wa.wg.Done()

	batch := make([]AuditEntry, 0, wa.batchSize)
	ticker := time.NewTicker(wa.flushTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-wa.ctx.Done():
			// Contexto cancelado, salvar entradas pendentes e sair
			if len(batch) > 0 {
				if err := wa.saveBatch(batch); err != nil {
					log.Printf("Erro ao salvar lote final de auditoria: %v", err)
				}
			}
			return

		case entry := <-wa.queue:
			// Adicionar ao lote atual
			batch = append(batch, entry)

			// Se o lote atingiu o tamanho máximo, salvar
			if len(batch) >= wa.batchSize {
				if err := wa.saveBatch(batch); err != nil {
					log.Printf("Erro ao salvar lote de auditoria: %v", err)
				}
				batch = make([]AuditEntry, 0, wa.batchSize)
			}

		case <-ticker.C:
			// Tempo expirou, salvar lote atual mesmo que incompleto
			if len(batch) > 0 {
				if err := wa.saveBatch(batch); err != nil {
					log.Printf("Erro ao salvar lote de auditoria (por timeout): %v", err)
				}
				batch = make([]AuditEntry, 0, wa.batchSize)
			}
		}
	}
}

// saveBatch salva um lote de entradas no banco de dados
func (wa *WriteAuditor) saveBatch(batch []AuditEntry) error {
	if len(batch) == 0 {
		return nil
	}

	// Iniciar transação
	tx, err := wa.db.Begin()
	if err != nil {
		return fmt.Errorf("erro ao iniciar transação: %w", err)
	}

	// Preparar a query de inserção
	query := fmt.Sprintf(`
		INSERT INTO %s (
			timestamp, user_id, username, action, 
			target, target_id, target_name, 
			old_value, new_value, reason, 
			ip_address, status, details
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, wa.tableName)

	stmt, err := tx.Prepare(query)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao preparar statement: %w", err)
	}
	defer stmt.Close()

	// Inserir cada entrada
	for _, entry := range batch {
		_, err := stmt.Exec(
			entry.Timestamp,
			entry.UserID,
			entry.Username,
			entry.Action,
			entry.Target,
			entry.TargetID,
			entry.TargetName,
			entry.OldValue,
			entry.NewValue,
			entry.Reason,
			entry.IPAddress,
			entry.Status,
			entry.Details,
		)

		if err != nil {
			tx.Rollback()
			return fmt.Errorf("erro ao inserir entrada de auditoria: %w", err)
		}
	}

	// Commit da transação
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("erro ao commit de lote de auditoria: %w", err)
	}

	log.Printf("Salvas %d entradas de auditoria", len(batch))
	return nil
}

// GetAuditLog recupera entradas de auditoria com filtragem
func (wa *WriteAuditor) GetAuditLog(
	from, to time.Time,
	userID int,
	action, target string,
	limit, offset int) ([]AuditEntry, error) {

	// Construir query com filtros opcionais
	query := fmt.Sprintf("SELECT * FROM %s WHERE 1=1", wa.tableName)
	args := []interface{}{}

	if !from.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, from)
	}

	if !to.IsZero() {
		query += " AND timestamp <= ?"
		args = append(args, to)
	}

	if userID > 0 {
		query += " AND user_id = ?"
		args = append(args, userID)
	}

	if action != "" {
		query += " AND action = ?"
		args = append(args, action)
	}

	if target != "" {
		query += " AND target LIKE ?"
		args = append(args, "%"+target+"%")
	}

	// Adicionar ordenação e paginação
	query += " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	// Executar a query
	rows, err := wa.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar logs de auditoria: %w", err)
	}
	defer rows.Close()

	// Processar resultados
	var result []AuditEntry

	for rows.Next() {
		var entry AuditEntry
		var oldValueStr, newValueStr string

		err := rows.Scan(
			&entry.ID,
			&entry.Timestamp,
			&entry.UserID,
			&entry.Username,
			&entry.Action,
			&entry.Target,
			&entry.TargetID,
			&entry.TargetName,
			&oldValueStr,
			&newValueStr,
			&entry.Reason,
			&entry.IPAddress,
			&entry.Status,
			&entry.Details,
		)

		if err != nil {
			return nil, fmt.Errorf("erro ao ler entrada de auditoria: %w", err)
		}

		// Converter valores JSON para interface{}
		if oldValueStr != "" {
			if err := json.Unmarshal([]byte(oldValueStr), &entry.OldValue); err != nil {
				entry.OldValue = oldValueStr // fallback para string bruta
			}
		}

		if newValueStr != "" {
			if err := json.Unmarshal([]byte(newValueStr), &entry.NewValue); err != nil {
				entry.NewValue = newValueStr // fallback para string bruta
			}
		}

		result = append(result, entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar resultados de auditoria: %w", err)
	}

	return result, nil
}

// GetLatestValueChanges recupera as últimas alterações de valor para uma tag específica
func (wa *WriteAuditor) GetLatestValueChanges(targetID int, limit int) ([]AuditEntry, error) {
	query := fmt.Sprintf(`
		SELECT * FROM %s 
		WHERE target_id = ? AND action = 'write' AND status = 'success'
		ORDER BY timestamp DESC 
		LIMIT ?
	`, wa.tableName)

	rows, err := wa.db.Query(query, targetID, limit)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar alterações de valor: %w", err)
	}
	defer rows.Close()

	var result []AuditEntry

	for rows.Next() {
		var entry AuditEntry
		var oldValueStr, newValueStr string

		err := rows.Scan(
			&entry.ID,
			&entry.Timestamp,
			&entry.UserID,
			&entry.Username,
			&entry.Action,
			&entry.Target,
			&entry.TargetID,
			&entry.TargetName,
			&oldValueStr,
			&newValueStr,
			&entry.Reason,
			&entry.IPAddress,
			&entry.Status,
			&entry.Details,
		)

		if err != nil {
			return nil, fmt.Errorf("erro ao ler entrada de auditoria: %w", err)
		}

		// Converter valores JSON para interface{}
		if oldValueStr != "" {
			if err := json.Unmarshal([]byte(oldValueStr), &entry.OldValue); err != nil {
				entry.OldValue = oldValueStr
			}
		}

		if newValueStr != "" {
			if err := json.Unmarshal([]byte(newValueStr), &entry.NewValue); err != nil {
				entry.NewValue = newValueStr
			}
		}

		result = append(result, entry)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("erro ao iterar resultados: %w", err)
	}

	return result, nil
}
