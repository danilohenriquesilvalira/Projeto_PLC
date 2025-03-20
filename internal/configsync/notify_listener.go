package configsync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"Projeto_PLC/internal/cache"
	"Projeto_PLC/internal/database"

	"github.com/lib/pq"
)

// PostgresListener escuta notificações do PostgreSQL
type PostgresListener struct {
	listener *pq.Listener
	ctx      context.Context
	cache    cache.Cache
	syncer   *SimpleSyncManager
	logger   *database.Logger
}

// NewPostgresListener cria um novo listener
func NewPostgresListener(ctx context.Context, connectionString string,
	cache cache.Cache, syncer *SimpleSyncManager, logger *database.Logger) (*PostgresListener, error) {

	// Criar o listener
	listener := pq.NewListener(
		connectionString,
		10*time.Second,
		time.Minute,
		nil,
	)

	// Testar a conexão
	if err := listener.Ping(); err != nil {
		return nil, fmt.Errorf("falha ao conectar ao PostgreSQL para notificações: %w", err)
	}

	// Registrar canais de interesse
	if err := listener.Listen("plc_changes"); err != nil {
		return nil, fmt.Errorf("erro ao registrar canal plc_changes: %w", err)
	}
	if err := listener.Listen("tag_changes"); err != nil {
		return nil, fmt.Errorf("erro ao registrar canal tag_changes: %w", err)
	}

	return &PostgresListener{
		listener: listener,
		ctx:      ctx,
		cache:    cache,
		syncer:   syncer,
		logger:   logger,
	}, nil
}

// Start inicia o processamento de notificações
func (pl *PostgresListener) Start() {
	log.Println("Iniciando processamento de notificações PostgreSQL")

	go pl.processNotifications()
}

// Close fecha o listener
func (pl *PostgresListener) Close() {
	pl.listener.Close()
}

// processNotifications processa notificações recebidas
func (pl *PostgresListener) processNotifications() {
	for {
		select {
		case <-pl.ctx.Done():
			log.Println("Encerrando processamento de notificações PostgreSQL")
			return

		case notification := <-pl.listener.Notify:
			if notification == nil {
				// Reconexão
				continue
			}

			log.Printf("Notificação recebida do canal %s: %s",
				notification.Channel, notification.Extra)

			// Processar baseado no canal
			switch notification.Channel {
			case "plc_changes":
				pl.handlePLCChange(notification.Extra)
			case "tag_changes":
				pl.handleTagChange(notification.Extra)
			}
		}
	}
}

// Estruturas para notificações
type PLCNotification struct {
	Type   string `json:"type"` // insert, update, delete
	PLCID  int    `json:"plc_id"`
	Active bool   `json:"active"`
}

type TagNotification struct {
	Type   string `json:"type"` // insert, update, delete
	TagID  int    `json:"tag_id"`
	PLCID  int    `json:"plc_id"`
	Active bool   `json:"active"`
}

// handlePLCChange processa mudanças em PLCs
func (pl *PostgresListener) handlePLCChange(jsonData string) {
	var notification PLCNotification
	if err := json.Unmarshal([]byte(jsonData), &notification); err != nil {
		log.Printf("Erro ao deserializar notificação de PLC: %v", err)
		return
	}

	// Sincronizar PLCs após qualquer mudança
	if _, err := pl.syncer.SyncPLCs(); err != nil {
		log.Printf("Erro ao sincronizar PLCs após notificação: %v", err)
	} else {
		log.Printf("PLCs sincronizados após notificação")
	}
}

// handleTagChange processa mudanças em tags
func (pl *PostgresListener) handleTagChange(jsonData string) {
	var notification TagNotification
	if err := json.Unmarshal([]byte(jsonData), &notification); err != nil {
		log.Printf("Erro ao deserializar notificação de tag: %v", err)
		return
	}

	// Sincronizar tags do PLC específico
	if _, err := pl.syncer.SyncPLCTags(notification.PLCID); err != nil {
		log.Printf("Erro ao sincronizar tags do PLC %d após notificação: %v",
			notification.PLCID, err)
	} else {
		log.Printf("Tags do PLC %d sincronizadas após notificação", notification.PLCID)
	}
}
