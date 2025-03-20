//writer.go

package plcmanager

import (
	"Projeto_PLC/internal/database"
	"fmt"
	"log"
	"strings"
	"time"

	plclib "Projeto_PLC/internal/plc"
)

// WriteTagByName encontra uma tag pelo nome em todos os PLCs e escreve o valor nela
func (m *Manager) WriteTagByName(tagName string, value interface{}) error {
	log.Printf("Solicitação para escrever na tag '%s': %v", tagName, value)

	// Buscar PLCs ativos com timeout específico
	dbForQuery := m.db.WithTimeout(10 * time.Second)
	plcs, err := dbForQuery.GetActivePLCs()
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			log.Printf("Tempo limite excedido ao buscar PLCs ativos: %v", err)
			m.logger.Error("Tempo limite excedido ao buscar PLCs ativos", err.Error())
			return fmt.Errorf("tempo limite excedido ao buscar PLCs ativos: %w", err)
		}
		log.Printf("Erro ao buscar PLCs ativos: %v", err)
		m.logger.Error("Erro ao buscar PLCs ativos", err.Error())
		return fmt.Errorf("erro ao buscar PLCs ativos: %w", err)
	}

	// Procurar a tag em todos os PLCs
	for _, plcConfig := range plcs {
		// Verificar se o PLC está online
		if plcConfig.Status != "online" {
			log.Printf("PLC %s (ID=%d) está offline, ignorando", plcConfig.Name, plcConfig.ID)
			continue
		}

		// Tentar encontrar a tag pelo nome
		tag, err := dbForQuery.GetTagByName(plcConfig.ID, tagName)
		if err != nil {
			// Tag não encontrada neste PLC, continua para o próximo
			log.Printf("Tag '%s' não encontrada no PLC %s (ID=%d)", tagName, plcConfig.Name, plcConfig.ID)
			continue
		}

		log.Printf("Tag '%s' encontrada no PLC %s (ID=%d)", tagName, plcConfig.Name, plcConfig.ID)

		// Verificar se a tag permite escrita
		if !tag.CanWrite {
			log.Printf("Tag '%s' não permite escrita", tagName)
			m.logger.Warn("Tentativa de escrita em tag sem permissão", fmt.Sprintf("Tag: %s, PLC: %s", tagName, plcConfig.Name))
			return fmt.Errorf("tag '%s' não permite escrita", tagName)
		}

		// Conectar ao PLC para escrita
		var client *plclib.Client

		if plcConfig.UseVLAN && plcConfig.Gateway != "" {
			// Usando configuração avançada com VLAN
			config := plclib.ClientConfig{
				IPAddress:  plcConfig.IPAddress,
				Rack:       plcConfig.Rack,
				Slot:       plcConfig.Slot,
				Timeout:    10 * time.Second,
				UseVLAN:    true,
				Gateway:    plcConfig.Gateway,
				SubnetMask: plcConfig.SubnetMask,
				VLANID:     plcConfig.VLANID,
			}

			log.Printf("Conectando ao PLC %s (ID=%d) com VLAN para escrita...", plcConfig.Name, plcConfig.ID)
			client, err = plclib.NewClientWithConfig(config)
		} else {
			// Configuração básica sem VLAN
			log.Printf("Conectando ao PLC %s (ID=%d, IP: %s) para escrita...", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
			client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
		}

		if err != nil {
			log.Printf("Erro ao conectar ao PLC %s para escrita: %v", plcConfig.Name, err)
			m.logger.Error("Erro de conexão para escrita", fmt.Sprintf("PLC: %s, Erro: %v", plcConfig.Name, err))

			// Atualiza o status para offline já que não conseguiu conectar
			offlineStatus := database.PLCStatus{
				PLCID:      plcConfig.ID,
				Status:     "offline",
				LastUpdate: time.Now(),
			}

			// Usa um DB com timeout específico para atualização
			dbForUpdate := m.db.WithTimeout(5 * time.Second)
			if errUpd := dbForUpdate.UpdatePLCStatus(offlineStatus); errUpd != nil {
				log.Printf("Erro ao atualizar status offline do PLC ID %d: %v", plcConfig.ID, errUpd)
			}

			return fmt.Errorf("erro ao conectar ao PLC %s para escrita: %w", plcConfig.Name, err)
		}
		defer client.Close()

		// Chamar a função WriteTag do client PLC
		if err := client.WriteTag(tag.DBNumber, tag.ByteOffset, tag.DataType, value); err != nil {
			log.Printf("Erro ao escrever no PLC: %v", err)
			m.logger.Error("Erro ao escrever tag", fmt.Sprintf("Tag: %s, PLC: %s, Erro: %v", tagName, plcConfig.Name, err))
			return fmt.Errorf("erro ao escrever no PLC: %w", err)
		} else {
			log.Printf("Valor escrito com sucesso no PLC para tag %s", tagName)
			m.logger.Info("Valor escrito com sucesso", fmt.Sprintf("Tag: %s, PLC: %s, Valor: %v", tagName, plcConfig.Name, value))
		}

		// Atualizar valor no Redis também
		if err := m.cache.SetTagValue(plcConfig.ID, tag.ID, value); err != nil {
			log.Printf("Aviso: Erro ao atualizar cache Redis após escrita: %v", err)
			m.logger.Warn("Erro ao atualizar Redis após escrita", fmt.Sprintf("Tag: %s, Erro: %v", tagName, err))
			// Não falha se apenas o Redis falhar
		} else {
			log.Printf("Valor da tag '%s' atualizado no Redis com sucesso", tagName)

			// Publicar atualização
			_ = m.cache.PublishTagUpdate(plcConfig.ID, tag.ID, value)
		}

		return nil // Sucesso
	}

	log.Printf("Tag '%s' não encontrada em nenhum PLC ativo", tagName)
	m.logger.Warn("Tag não encontrada", fmt.Sprintf("Tag: %s", tagName))
	return fmt.Errorf("tag '%s' não encontrada em nenhum PLC", tagName)
}

// WriteTagByID escreve um valor em uma tag específica usando o ID do PLC e da tag
func (m *Manager) WriteTagByID(plcID int, tagID int, value interface{}) error {
	log.Printf("Solicitação para escrever na tag ID %d do PLC ID %d: %v", tagID, plcID, value)

	// Buscar o PLC com timeout específico
	dbForQuery := m.db.WithTimeout(5 * time.Second)
	plcConfig, err := dbForQuery.GetPLCByID(plcID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			log.Printf("Tempo limite excedido ao buscar PLC ID %d: %v", plcID, err)
			m.logger.Error("Tempo limite excedido ao buscar PLC", fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))
			return fmt.Errorf("tempo limite excedido ao buscar PLC ID %d: %w", plcID, err)
		}
		log.Printf("Erro ao buscar PLC ID %d: %v", plcID, err)
		m.logger.Error("Erro ao buscar PLC", fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))
		return fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
	}

	// Verificar se o PLC está online
	if plcConfig.Status != "online" {
		log.Printf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
		m.logger.Warn("Tentativa de escrita em PLC offline", fmt.Sprintf("PLC: %s (ID=%d)", plcConfig.Name, plcConfig.ID))
		return fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	}

	// Buscar a tag
	tag, err := dbForQuery.GetTagByID(tagID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			log.Printf("Tempo limite excedido ao buscar tag ID %d: %v", tagID, err)
			m.logger.Error("Tempo limite excedido ao buscar tag", fmt.Sprintf("Tag ID: %d, Erro: %v", tagID, err))
			return fmt.Errorf("tempo limite excedido ao buscar tag ID %d: %w", tagID, err)
		}
		log.Printf("Erro ao buscar tag ID %d: %v", tagID, err)
		m.logger.Error("Erro ao buscar tag", fmt.Sprintf("Tag ID: %d, Erro: %v", tagID, err))
		return fmt.Errorf("erro ao buscar tag ID %d: %w", tagID, err)
	}

	// Verificar se a tag pertence ao PLC correto
	if tag.PLCID != plcID {
		log.Printf("Tag ID %d não pertence ao PLC ID %d", tagID, plcID)
		m.logger.Warn("Tag não pertence ao PLC", fmt.Sprintf("Tag ID: %d, PLC ID: %d", tagID, plcID))
		return fmt.Errorf("tag ID %d não pertence ao PLC ID %d", tagID, plcID)
	}

	// Verificar se a tag permite escrita
	if !tag.CanWrite {
		log.Printf("Tag '%s' não permite escrita", tag.Name)
		m.logger.Warn("Tentativa de escrita em tag sem permissão", fmt.Sprintf("Tag: %s", tag.Name))
		return fmt.Errorf("tag '%s' não permite escrita", tag.Name)
	}

	// Conectar ao PLC
	var client *plclib.Client

	if plcConfig.UseVLAN && plcConfig.Gateway != "" {
		config := plclib.ClientConfig{
			IPAddress:  plcConfig.IPAddress,
			Rack:       plcConfig.Rack,
			Slot:       plcConfig.Slot,
			Timeout:    10 * time.Second,
			UseVLAN:    true,
			Gateway:    plcConfig.Gateway,
			SubnetMask: plcConfig.SubnetMask,
			VLANID:     plcConfig.VLANID,
		}

		log.Printf("Conectando ao PLC %s (ID=%d) com VLAN para escrita...", plcConfig.Name, plcConfig.ID)
		client, err = plclib.NewClientWithConfig(config)
	} else {
		log.Printf("Conectando ao PLC %s (ID=%d, IP: %s) para escrita...", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
		client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
	}

	if err != nil {
		log.Printf("Erro ao conectar ao PLC %s: %v", plcConfig.Name, err)
		m.logger.Error("Erro de conexão para escrita", fmt.Sprintf("PLC: %s, Erro: %v", plcConfig.Name, err))

		// Atualiza o status para offline já que não conseguiu conectar
		offlineStatus := database.PLCStatus{
			PLCID:      plcConfig.ID,
			Status:     "offline",
			LastUpdate: time.Now(),
		}

		// Usa um DB com timeout específico para atualização
		dbForUpdate := m.db.WithTimeout(5 * time.Second)
		if errUpd := dbForUpdate.UpdatePLCStatus(offlineStatus); errUpd != nil {
			log.Printf("Erro ao atualizar status offline do PLC ID %d: %v", plcConfig.ID, errUpd)
		}

		return fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, err)
	}
	defer client.Close()

	// Escrever no PLC
	if err := client.WriteTag(tag.DBNumber, tag.ByteOffset, tag.DataType, value); err != nil {
		log.Printf("Erro ao escrever no PLC para tag %s: %v", tag.Name, err)
		m.logger.Error("Erro ao escrever tag", fmt.Sprintf("Tag: %s, PLC: %s, Erro: %v", tag.Name, plcConfig.Name, err))
		return fmt.Errorf("erro ao escrever no PLC: %w", err)
	}

	log.Printf("Valor escrito com sucesso para tag %s (ID: %d)", tag.Name, tagID)
	m.logger.Info("Valor escrito com sucesso", fmt.Sprintf("Tag: %s, PLC: %s, Valor: %v", tag.Name, plcConfig.Name, value))

	// Atualizar o Redis
	if err := m.cache.SetTagValue(plcID, tagID, value); err != nil {
		log.Printf("Aviso: Erro ao atualizar cache Redis após escrita: %v", err)
		m.logger.Warn("Erro ao atualizar Redis após escrita", fmt.Sprintf("Tag: %s, Erro: %v", tag.Name, err))
	} else {
		_ = m.cache.PublishTagUpdate(plcID, tagID, value)
		log.Printf("Cache Redis atualizado para tag %s", tag.Name)
	}

	return nil
}

// WriteBulkTags escreve múltiplas tags de uma vez em um PLC
func (m *Manager) WriteBulkTags(plcID int, tagValues map[string]interface{}) (map[string]error, error) {
	// Resultado para erros individuais por tag
	results := make(map[string]error)
	log.Printf("Solicitação para escrita em lote de %d tags no PLC ID %d", len(tagValues), plcID)

	// Verificar se o PLC existe e está online - usa timeout específico
	dbForQuery := m.db.WithTimeout(5 * time.Second)
	plcConfig, err := dbForQuery.GetPLCByID(plcID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			log.Printf("Tempo limite excedido ao buscar PLC ID %d: %v", plcID, err)
			m.logger.Error("Tempo limite excedido ao buscar PLC", fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))
			return nil, fmt.Errorf("tempo limite excedido ao buscar PLC ID %d: %w", plcID, err)
		}
		log.Printf("Erro ao buscar PLC ID %d: %v", plcID, err)
		m.logger.Error("Erro ao buscar PLC", fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))
		return nil, fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
	}

	if plcConfig.Status != "online" {
		log.Printf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
		m.logger.Warn("Tentativa de escrita em lote em PLC offline", fmt.Sprintf("PLC: %s (ID=%d)", plcConfig.Name, plcConfig.ID))
		return nil, fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	}

	// Buscar todas as tags do PLC e criar um mapa por nome - usa timeout aumentado para operação em lote
	dbForTags := m.db.WithTimeout(10 * time.Second)
	tags, err := dbForTags.GetPLCTags(plcID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			log.Printf("Tempo limite excedido ao buscar tags do PLC %d: %v", plcID, err)
			m.logger.Error("Tempo limite excedido ao buscar tags do PLC", fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))
			return nil, fmt.Errorf("tempo limite excedido ao buscar tags do PLC: %w", err)
		}
		log.Printf("Erro ao buscar tags do PLC %d: %v", plcID, err)
		m.logger.Error("Erro ao buscar tags do PLC", fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))
		return nil, fmt.Errorf("erro ao buscar tags do PLC: %w", err)
	}

	tagsByName := make(map[string]database.Tag)
	for _, tag := range tags {
		tagsByName[tag.Name] = tag
	}

	log.Printf("Encontradas %d tags no PLC %s, iniciando processamento do lote", len(tagsByName), plcConfig.Name)

	// Conectar ao PLC uma única vez para todas as escritas
	var client *plclib.Client

	if plcConfig.UseVLAN && plcConfig.Gateway != "" {
		config := plclib.ClientConfig{
			IPAddress:  plcConfig.IPAddress,
			Rack:       plcConfig.Rack,
			Slot:       plcConfig.Slot,
			Timeout:    15 * time.Second, // Timeout maior para operações em lote
			UseVLAN:    true,
			Gateway:    plcConfig.Gateway,
			SubnetMask: plcConfig.SubnetMask,
			VLANID:     plcConfig.VLANID,
		}

		log.Printf("Conectando ao PLC %s (ID=%d) com VLAN para escrita em lote...", plcConfig.Name, plcConfig.ID)
		client, err = plclib.NewClientWithConfig(config)
	} else {
		log.Printf("Conectando ao PLC %s (ID=%d, IP: %s) para escrita em lote...", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
		client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
	}

	if err != nil {
		log.Printf("Erro ao conectar ao PLC %s: %v", plcConfig.Name, err)
		m.logger.Error("Erro de conexão para escrita em lote", fmt.Sprintf("PLC: %s, Erro: %v", plcConfig.Name, err))

		// Atualiza o status para offline já que não conseguiu conectar
		offlineStatus := database.PLCStatus{
			PLCID:      plcConfig.ID,
			Status:     "offline",
			LastUpdate: time.Now(),
		}

		// Usa DB específico para atualização
		dbForUpdate := m.db.WithTimeout(5 * time.Second)
		if errUpd := dbForUpdate.UpdatePLCStatus(offlineStatus); errUpd != nil {
			log.Printf("Erro ao atualizar status offline do PLC ID %d: %v", plcConfig.ID, errUpd)
		}

		return nil, fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, err)
	}
	defer client.Close()

	// Processar cada tag
	for tagName, value := range tagValues {
		tag, exists := tagsByName[tagName]
		if !exists {
			log.Printf("Tag '%s' não encontrada no PLC %s", tagName, plcConfig.Name)
			results[tagName] = fmt.Errorf("tag '%s' não encontrada", tagName)
			continue
		}

		if !tag.CanWrite {
			log.Printf("Tag '%s' não permite escrita", tagName)
			m.logger.Warn("Tentativa de escrita em tag sem permissão", fmt.Sprintf("Tag: %s", tagName))
			results[tagName] = fmt.Errorf("tag '%s' não permite escrita", tagName)
			continue
		}

		// Escrever no PLC
		if err := client.WriteTag(tag.DBNumber, tag.ByteOffset, tag.DataType, value); err != nil {
			log.Printf("Erro ao escrever tag '%s' no PLC: %v", tagName, err)
			m.logger.Error("Erro ao escrever tag", fmt.Sprintf("Tag: %s, PLC: %s, Erro: %v", tagName, plcConfig.Name, err))
			results[tagName] = fmt.Errorf("erro ao escrever: %w", err)
			continue
		}

		log.Printf("Valor escrito com sucesso para tag '%s'", tagName)

		// Atualizar no Redis
		if err := m.cache.SetTagValue(plcID, tag.ID, value); err != nil {
			log.Printf("Aviso: Erro ao atualizar Redis para tag %s: %v", tagName, err)
			m.logger.Warn("Erro ao atualizar Redis após escrita", fmt.Sprintf("Tag: %s, Erro: %v", tagName, err))
		} else {
			_ = m.cache.PublishTagUpdate(plcID, tag.ID, value)
			log.Printf("Cache Redis atualizado para tag '%s'", tagName)
		}

		// Sucesso para esta tag
		results[tagName] = nil
	}

	log.Printf("Processo de escrita em lote concluído para o PLC %s", plcConfig.Name)
	return results, nil
}
