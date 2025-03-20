package configsync

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"Projeto_PLC/internal/cache"
	"Projeto_PLC/internal/database"
)

// SimpleSyncManager é uma versão simplificada do sincronizador para resolver o problema urgente
type SimpleSyncManager struct {
	db     *database.DB
	cache  cache.Cache
	logger *database.Logger
}

// NewSimpleSyncManager cria um sincronizador simplificado
func NewSimpleSyncManager(db *database.DB, cache cache.Cache, logger *database.Logger) *SimpleSyncManager {
	return &SimpleSyncManager{
		db:     db,
		cache:  cache,
		logger: logger,
	}
}

// SyncPLCs sincroniza PLCs diretamente da consulta SQL para o Redis, sem camadas intermediárias
func (s *SimpleSyncManager) SyncPLCs() (int, error) {
	log.Println("Sincronizando PLCs diretamente via SQL...")

	// Criar uma query direta SQL que seja rápida e não use ORM
	// Isso evita o context canceled do banco de dados
	query := `
        SELECT p.id, p.name, p.ip_address, p.rack, p.slot, 
               p.description, p.active, p.use_vlan, p.gateway, 
               p.subnet_mask, p.vlan_id,
               COALESCE(s.status, 'unknown') as status
        FROM plcs p 
        LEFT JOIN plc_status s ON p.id = s.plc_id 
        WHERE p.active = true
    `

	// Obter conexão direta com o DB (sem camadas que podem causar timeout)
	db := s.db.GetDB()

	// Executar a query
	rows, err := db.Query(query)
	if err != nil {
		log.Printf("ERRO SQL: %v", err)
		return 0, err
	}
	defer rows.Close()

	// Processar resultados
	plcs := []database.PLC{}

	for rows.Next() {
		var plc database.PLC
		var status, gateway, subnetMask sql.NullString
		var vlanID sql.NullInt64

		err := rows.Scan(
			&plc.ID, &plc.Name, &plc.IPAddress, &plc.Rack, &plc.Slot,
			&plc.Description, &plc.Active, &plc.UseVLAN, &gateway,
			&subnetMask, &vlanID, &status)

		if err != nil {
			log.Printf("Erro ao ler PLC: %v", err)
			continue
		}

		// Converter campos NULL
		if gateway.Valid {
			plc.Gateway = gateway.String
		}
		if subnetMask.Valid {
			plc.SubnetMask = subnetMask.String
		}
		if vlanID.Valid {
			plc.VLANID = int(vlanID.Int64)
		}
		if status.Valid {
			plc.Status = status.String
		} else {
			plc.Status = "unknown"
		}

		plcs = append(plcs, plc)
	}

	// Verificar se obtivemos dados
	if len(plcs) == 0 {
		log.Println("Nenhum PLC ativo encontrado!")
		return 0, nil
	}

	// Armazenar no Redis
	plcsJSON, err := json.Marshal(plcs)
	if err != nil {
		return 0, err
	}

	// Chave para lista completa
	if err := s.cache.SetValue("config:plcs:all", string(plcsJSON)); err != nil {
		log.Printf("Erro ao armazenar PLCs no cache: %v", err)
		return 0, err
	}

	// Armazenar PLCs individuais
	for _, plc := range plcs {
		plcJSON, _ := json.Marshal(plc)
		s.cache.SetValue(fmt.Sprintf("config:plc:%d", plc.ID), string(plcJSON))
		if plc.Status != "" {
			s.cache.SetValue(fmt.Sprintf("config:plc:%d:status", plc.ID), plc.Status)
		}
	}

	log.Printf("Sincronização bem-sucedida de %d PLCs!", len(plcs))
	return len(plcs), nil
}

// SyncPLCTags sincroniza as tags de um PLC específico
func (s *SimpleSyncManager) SyncPLCTags(plcID int) (int, error) {
	log.Printf("Sincronizando tags do PLC ID %d diretamente via SQL...", plcID)

	// Query SQL direta para obter as tags
	query := "SELECT * FROM tags WHERE plc_id = $1 AND active = true"

	// Obter conexão direta
	db := s.db.GetDB()

	// Executar a query
	rows, err := db.Query(query, plcID)
	if err != nil {
		log.Printf("ERRO SQL tags: %v", err)
		return 0, err
	}
	defer rows.Close()

	// Processar resultados - esta parte pode ser complexa devido à estrutura da Tag
	// Para simplificar, vamos executar a mesma lógica do DB mas sem usar o context
	tags, err := s.db.GetPLCTags(plcID)
	if err != nil {
		return 0, err
	}

	// Filtrar apenas tags ativas
	var activeTags []database.Tag
	for _, tag := range tags {
		if tag.Active {
			activeTags = append(activeTags, tag)
		}
	}

	// Se não houver tags, armazenar array vazio
	if len(activeTags) == 0 {
		s.cache.SetValue(fmt.Sprintf("config:plc:%d:tags", plcID), "[]")
		return 0, nil
	}

	// Serializar e armazenar tags
	tagsJSON, err := json.Marshal(activeTags)
	if err != nil {
		return 0, err
	}

	// Armazenar lista completa
	if err := s.cache.SetValue(fmt.Sprintf("config:plc:%d:tags", plcID), string(tagsJSON)); err != nil {
		return 0, err
	}

	// Armazenar cada tag individual e mapeamento de nomes
	for _, tag := range activeTags {
		tagJSON, _ := json.Marshal(tag)
		s.cache.SetValue(fmt.Sprintf("config:plc:%d:tag:%d", plcID, tag.ID), string(tagJSON))
		s.cache.SetValue(fmt.Sprintf("config:plc:%d:tagname:%s", plcID, tag.Name), fmt.Sprintf("%d", tag.ID))
	}

	log.Printf("Sincronização bem-sucedida de %d tags para o PLC ID %d!", len(activeTags), plcID)
	return len(activeTags), nil
}

// SyncAll sincroniza todos PLCs e suas tags
func (s *SimpleSyncManager) SyncAll() error {
	// Sincronizar PLCs
	plcCount, err := s.SyncPLCs()
	if err != nil {
		return fmt.Errorf("erro ao sincronizar PLCs: %w", err)
	}

	// Se não há PLCs, não há o que sincronizar
	if plcCount == 0 {
		return nil
	}

	// Obter IDs dos PLCs para sincronizar as tags
	plcs, err := s.db.GetActivePLCs()
	if err != nil {
		return fmt.Errorf("erro ao obter PLCs para sincronizar tags: %w", err)
	}

	// Sincronizar tags de cada PLC
	var syncErrors []string
	for _, plc := range plcs {
		_, err := s.SyncPLCTags(plc.ID)
		if err != nil {
			errMsg := fmt.Sprintf("PLC %d: %v", plc.ID, err)
			syncErrors = append(syncErrors, errMsg)
			log.Printf("Erro ao sincronizar tags do PLC %d: %v", plc.ID, err)
		}
	}

	// Se houver erros, retorna um resumo
	if len(syncErrors) > 0 {
		return fmt.Errorf("erros de sincronização: %v", syncErrors)
	}

	return nil
}
