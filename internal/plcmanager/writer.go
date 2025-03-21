package plcmanager

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"Projeto_PLC/internal/database"
	plclib "Projeto_PLC/internal/plc"
)

// WriteTagByName encontra uma tag pelo nome em todos os PLCs e escreve o valor nela
func (m *Manager) WriteTagByName(tagName string, value interface{}) error {
	if tagName == "" {
		return fmt.Errorf("nome da tag não pode ser vazio")
	}

	// Log da solicitação apenas em modo detalhado
	if DetailedLogging {
		log.Printf("Solicitação para escrever na tag '%s': %v", tagName, value)
	}

	// Buscar PLCs ativos com timeout específico
	dbForQuery := m.db.WithTimeout(10 * time.Second)
	plcs, err := dbForQuery.GetActivePLCs()
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			m.logger.ErrorWithDetails("PLC Writer",
				"Tempo limite excedido ao buscar PLCs para escrita",
				fmt.Sprintf("Tag: %s, Erro: %v", tagName, err))

			return fmt.Errorf("tempo limite excedido ao buscar PLCs ativos: %w", err)
		}

		m.logger.ErrorWithDetails("PLC Writer",
			"Falha ao buscar PLCs para escrita",
			fmt.Sprintf("Tag: %s, Erro: %v", tagName, err))

		return fmt.Errorf("erro ao buscar PLCs ativos: %w", err)
	}

	// Variáveis para rastrear a busca
	var plcsTried int
	var plcsOffline int
	var lastError error

	// Procurar a tag em todos os PLCs
	for _, plcConfig := range plcs {
		// Verificar se o PLC está online
		if plcConfig.Status != "online" {
			if DetailedLogging {
				log.Printf("PLC %s (ID=%d) está offline, ignorando", plcConfig.Name, plcConfig.ID)
			}
			plcsOffline++
			continue
		}

		plcsTried++

		// Tentar encontrar a tag pelo nome
		tag, err := dbForQuery.GetTagByName(plcConfig.ID, tagName)
		if err != nil {
			// Tag não encontrada neste PLC, continua para o próximo
			if DetailedLogging {
				log.Printf("Tag '%s' não encontrada no PLC %s (ID=%d)", tagName, plcConfig.Name, plcConfig.ID)
			}
			lastError = err
			continue
		}

		if DetailedLogging {
			log.Printf("Tag '%s' encontrada no PLC %s (ID=%d)", tagName, plcConfig.Name, plcConfig.ID)
		}

		// Verificar se a tag permite escrita
		if !tag.CanWrite {
			m.logger.WarnWithDetails("PLC Writer",
				"Tentativa de escrita em tag sem permissão",
				fmt.Sprintf("Tag: %s, PLC: %s (ID=%d)", tagName, plcConfig.Name, plcConfig.ID))

			return fmt.Errorf("tag '%s' não permite escrita", tagName)
		}

		// Conectar ao PLC para escrita
		var client *plclib.Client
		var clientDetails string

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

			if DetailedLogging {
				log.Printf("Conectando ao PLC %s (ID=%d) com VLAN para escrita...", plcConfig.Name, plcConfig.ID)
			}

			clientDetails = fmt.Sprintf("Configuração VLAN: IP=%s, Gateway=%s, VLAN ID=%d",
				plcConfig.IPAddress, plcConfig.Gateway, plcConfig.VLANID)

			client, err = plclib.NewClientWithConfig(config)
		} else {
			// Configuração básica sem VLAN
			if DetailedLogging {
				log.Printf("Conectando ao PLC %s (ID=%d, IP: %s) para escrita...", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
			}

			clientDetails = fmt.Sprintf("IP=%s, Rack=%d, Slot=%d",
				plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)

			client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
		}

		if err != nil {
			m.logger.ErrorWithDetails("PLC Writer",
				fmt.Sprintf("Erro de conexão ao PLC %s para escrita", plcConfig.Name),
				fmt.Sprintf("Tag: %s, %s, Erro: %v", tagName, clientDetails, err))

			// Atualiza o status para offline já que não conseguiu conectar
			offlineStatus := database.PLCStatus{
				PLCID:      plcConfig.ID,
				Status:     "offline",
				LastUpdate: time.Now(),
			}

			// Usa um DB com timeout específico para atualização
			dbForUpdate := m.db.WithTimeout(5 * time.Second)
			if errUpd := dbForUpdate.UpdatePLCStatus(offlineStatus); errUpd != nil {
				if DetailedLogging {
					log.Printf("Erro ao atualizar status offline do PLC ID %d: %v", plcConfig.ID, errUpd)
				}
			}

			lastError = err
			continue
		}
		defer client.Close()

		// Chamar a função WriteTag do client PLC
		if err := client.WriteTag(tag.DBNumber, tag.ByteOffset, tag.DataType, value); err != nil {
			m.logger.ErrorWithDetails("PLC Writer",
				fmt.Sprintf("Falha na escrita para tag %s", tagName),
				fmt.Sprintf("PLC: %s (ID=%d), DB: %d, ByteOffset: %.1f, DataType: %s, Valor: %v, Erro: %v",
					plcConfig.Name, plcConfig.ID, tag.DBNumber, tag.ByteOffset, tag.DataType, value, err))

			lastError = err
			continue
		} else {
			// Registra o sucesso da operação
			m.logger.InfoWithDetails("PLC Writer",
				fmt.Sprintf("Valor escrito com sucesso na tag %s", tagName),
				fmt.Sprintf("PLC: %s (ID=%d), DB: %d, ByteOffset: %.1f, DataType: %s, Valor: %v",
					plcConfig.Name, plcConfig.ID, tag.DBNumber, tag.ByteOffset, tag.DataType, value))
		}

		// Atualizar valor no Redis também
		if err := m.cache.SetTagValue(plcConfig.ID, tag.ID, value); err != nil {
			if DetailedLogging {
				log.Printf("Aviso: Erro ao atualizar cache Redis após escrita: %v", err)
			}

			m.logger.WarnWithDetails("PLC Writer",
				"Erro ao atualizar cache após escrita bem-sucedida",
				fmt.Sprintf("Tag: %s (ID=%d), PLC: %s, Erro: %v",
					tagName, tag.ID, plcConfig.Name, err))

			// Não falha se apenas o Redis falhar
		} else {
			if DetailedLogging {
				log.Printf("Valor da tag '%s' atualizado no Redis com sucesso", tagName)
			}

			// Publicar atualização
			_ = m.cache.PublishTagUpdate(plcConfig.ID, tag.ID, value)
		}

		return nil // Sucesso
	}

	// Se chegou aqui, a tag não foi encontrada ou todos os PLCs falharam
	if plcsTried == 0 {
		if plcsOffline > 0 {
			m.logger.WarnWithDetails("PLC Writer",
				"Nenhum PLC online disponível para escrita",
				fmt.Sprintf("Tag: %s, PLCs offline: %d", tagName, plcsOffline))

			return fmt.Errorf("nenhum PLC online disponível para escrita na tag '%s'", tagName)
		} else {
			m.logger.WarnWithDetails("PLC Writer",
				"Tag não encontrada em nenhum PLC ativo",
				fmt.Sprintf("Tag: %s, PLCs verificados: %d", tagName, len(plcs)))

			return fmt.Errorf("tag '%s' não encontrada em nenhum PLC", tagName)
		}
	}

	// Falha após tentar em vários PLCs, retorna o último erro
	m.logger.ErrorWithDetails("PLC Writer",
		fmt.Sprintf("Falha na escrita da tag %s após tentar em múltiplos PLCs", tagName),
		fmt.Sprintf("PLCs verificados: %d, PLCs offline: %d, Último erro: %v",
			plcsTried, plcsOffline, lastError))

	return fmt.Errorf("falha ao escrever na tag '%s' após tentar em %d PLCs: %w",
		tagName, plcsTried, lastError)
}

// WriteTagByID escreve um valor em uma tag específica usando o ID do PLC e da tag
func (m *Manager) WriteTagByID(plcID int, tagID int, value interface{}) error {
	if DetailedLogging {
		log.Printf("Solicitação para escrever na tag ID %d do PLC ID %d: %v", tagID, plcID, value)
	}

	// Buscar o PLC com timeout específico
	dbForQuery := m.db.WithTimeout(5 * time.Second)
	plcConfig, err := dbForQuery.GetPLCByID(plcID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			m.logger.ErrorWithDetails("PLC Writer",
				"Tempo limite excedido ao buscar PLC",
				fmt.Sprintf("PLC ID: %d, Tag ID: %d, Erro: %v", plcID, tagID, err))

			return fmt.Errorf("tempo limite excedido ao buscar PLC ID %d: %w", plcID, err)
		}

		m.logger.ErrorWithDetails("PLC Writer",
			"Falha ao buscar PLC",
			fmt.Sprintf("PLC ID: %d, Tag ID: %d, Erro: %v", plcID, tagID, err))

		return fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
	}

	// Verificar se o PLC está online
	if plcConfig.Status != "online" {
		m.logger.WarnWithDetails("PLC Writer",
			"Tentativa de escrita em PLC offline",
			fmt.Sprintf("PLC: %s (ID=%d), Tag ID: %d", plcConfig.Name, plcConfig.ID, tagID))

		return fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	}

	// Buscar a tag
	tag, err := dbForQuery.GetTagByID(tagID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			m.logger.ErrorWithDetails("PLC Writer",
				"Tempo limite excedido ao buscar tag",
				fmt.Sprintf("Tag ID: %d, PLC: %s (ID=%d), Erro: %v",
					tagID, plcConfig.Name, plcID, err))

			return fmt.Errorf("tempo limite excedido ao buscar tag ID %d: %w", tagID, err)
		}

		m.logger.ErrorWithDetails("PLC Writer",
			"Falha ao buscar tag",
			fmt.Sprintf("Tag ID: %d, PLC: %s (ID=%d), Erro: %v",
				tagID, plcConfig.Name, plcID, err))

		return fmt.Errorf("erro ao buscar tag ID %d: %w", tagID, err)
	}

	// Verificar se a tag pertence ao PLC correto
	if tag.PLCID != plcID {
		m.logger.WarnWithDetails("PLC Writer",
			"Tag não pertence ao PLC especificado",
			fmt.Sprintf("Tag ID: %d, PLC solicitado: %d, PLC real: %d",
				tagID, plcID, tag.PLCID))

		return fmt.Errorf("tag ID %d não pertence ao PLC ID %d", tagID, plcID)
	}

	// Verificar se a tag permite escrita
	if !tag.CanWrite {
		m.logger.WarnWithDetails("PLC Writer",
			"Tentativa de escrita em tag sem permissão",
			fmt.Sprintf("Tag: %s (ID=%d), PLC: %s (ID=%d)",
				tag.Name, tag.ID, plcConfig.Name, plcConfig.ID))

		return fmt.Errorf("tag '%s' não permite escrita", tag.Name)
	}

	// Conectar ao PLC
	var client *plclib.Client
	var clientDetails string

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

		if DetailedLogging {
			log.Printf("Conectando ao PLC %s (ID=%d) com VLAN para escrita...", plcConfig.Name, plcConfig.ID)
		}

		clientDetails = fmt.Sprintf("Configuração VLAN: IP=%s, Gateway=%s, VLAN ID=%d",
			plcConfig.IPAddress, plcConfig.Gateway, plcConfig.VLANID)

		client, err = plclib.NewClientWithConfig(config)
	} else {
		if DetailedLogging {
			log.Printf("Conectando ao PLC %s (ID=%d, IP: %s) para escrita...", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
		}

		clientDetails = fmt.Sprintf("IP=%s, Rack=%d, Slot=%d",
			plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)

		client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
	}

	if err != nil {
		m.logger.ErrorWithDetails("PLC Writer",
			fmt.Sprintf("Erro de conexão ao PLC %s para escrita", plcConfig.Name),
			fmt.Sprintf("Tag: %s (ID=%d), %s, Erro: %v",
				tag.Name, tag.ID, clientDetails, err))

		// Atualiza o status para offline já que não conseguiu conectar
		offlineStatus := database.PLCStatus{
			PLCID:      plcConfig.ID,
			Status:     "offline",
			LastUpdate: time.Now(),
		}

		// Usa um DB com timeout específico para atualização
		dbForUpdate := m.db.WithTimeout(5 * time.Second)
		if errUpd := dbForUpdate.UpdatePLCStatus(offlineStatus); errUpd != nil {
			if DetailedLogging {
				log.Printf("Erro ao atualizar status offline do PLC ID %d: %v", plcConfig.ID, errUpd)
			}
		}

		return fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, err)
	}
	defer client.Close()

	// Escrever no PLC
	if err := client.WriteTag(tag.DBNumber, tag.ByteOffset, tag.DataType, value); err != nil {
		m.logger.ErrorWithDetails("PLC Writer",
			fmt.Sprintf("Falha na escrita para tag %s", tag.Name),
			fmt.Sprintf("PLC: %s (ID=%d), DB: %d, ByteOffset: %.1f, DataType: %s, Valor: %v, Erro: %v",
				plcConfig.Name, plcConfig.ID, tag.DBNumber, tag.ByteOffset, tag.DataType, value, err))

		return fmt.Errorf("erro ao escrever no PLC: %w", err)
	}

	// Registra operação bem-sucedida
	m.logger.InfoWithDetails("PLC Writer",
		fmt.Sprintf("Valor escrito com sucesso na tag %s", tag.Name),
		fmt.Sprintf("PLC: %s (ID=%d), Tag ID: %d, DB: %d, ByteOffset: %.1f, DataType: %s, Valor: %v",
			plcConfig.Name, plcConfig.ID, tag.ID, tag.DBNumber, tag.ByteOffset, tag.DataType, value))

	// Atualizar o Redis
	if err := m.cache.SetTagValue(plcID, tag.ID, value); err != nil {
		if DetailedLogging {
			log.Printf("Aviso: Erro ao atualizar cache Redis após escrita: %v", err)
		}

		m.logger.WarnWithDetails("PLC Writer",
			"Erro ao atualizar cache após escrita bem-sucedida",
			fmt.Sprintf("Tag: %s (ID=%d), PLC: %s, Erro: %v",
				tag.Name, tag.ID, plcConfig.Name, err))

		// Não falha se apenas o cache falhar
	} else {
		_ = m.cache.PublishTagUpdate(plcID, tag.ID, value)
	}

	return nil
}

// WriteBulkTags escreve múltiplas tags de uma vez em um PLC
func (m *Manager) WriteBulkTags(plcID int, tagValues map[string]interface{}) (map[string]error, error) {
	// Resultado para erros individuais por tag
	results := make(map[string]error)

	// Variáveis para resumo da operação
	tagsTotal := len(tagValues)
	tagsSuccess := 0
	tagsFailed := 0

	if DetailedLogging {
		log.Printf("Solicitação para escrita em lote de %d tags no PLC ID %d", tagsTotal, plcID)
	}

	// Log da solicitação de escrita em lote
	m.logger.InfoWithDetails("PLC Writer",
		"Iniciando operação de escrita em lote",
		fmt.Sprintf("PLC ID: %d, Quantidade de tags: %d", plcID, tagsTotal))

	// Verificar se o PLC existe e está online - usa timeout específico
	dbForQuery := m.db.WithTimeout(5 * time.Second)
	plcConfig, err := dbForQuery.GetPLCByID(plcID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			m.logger.ErrorWithDetails("PLC Writer",
				"Tempo limite excedido ao buscar PLC para escrita em lote",
				fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))

			return nil, fmt.Errorf("tempo limite excedido ao buscar PLC ID %d: %w", plcID, err)
		}

		m.logger.ErrorWithDetails("PLC Writer",
			"Falha ao buscar PLC para escrita em lote",
			fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))

		return nil, fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
	}

	if plcConfig.Status != "online" {
		m.logger.WarnWithDetails("PLC Writer",
			"Tentativa de escrita em lote em PLC offline",
			fmt.Sprintf("PLC: %s (ID=%d), Tags solicitadas: %d",
				plcConfig.Name, plcConfig.ID, tagsTotal))

		return nil, fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	}

	// Buscar todas as tags do PLC e criar um mapa por nome - usa timeout aumentado para operação em lote
	dbForTags := m.db.WithTimeout(10 * time.Second)
	tags, err := dbForTags.GetPLCTags(plcID)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "deadline exceeded") {
			m.logger.ErrorWithDetails("PLC Writer",
				"Tempo limite excedido ao buscar tags para escrita em lote",
				fmt.Sprintf("PLC: %s (ID=%d), Erro: %v",
					plcConfig.Name, plcID, err))

			return nil, fmt.Errorf("tempo limite excedido ao buscar tags do PLC: %w", err)
		}

		m.logger.ErrorWithDetails("PLC Writer",
			"Falha ao buscar tags para escrita em lote",
			fmt.Sprintf("PLC: %s (ID=%d), Erro: %v",
				plcConfig.Name, plcID, err))

		return nil, fmt.Errorf("erro ao buscar tags do PLC: %w", err)
	}

	tagsByName := make(map[string]database.Tag)
	for _, tag := range tags {
		tagsByName[tag.Name] = tag
	}

	if DetailedLogging {
		log.Printf("Encontradas %d tags no PLC %s, iniciando processamento do lote", len(tagsByName), plcConfig.Name)
	}

	// Conectar ao PLC uma única vez para todas as escritas
	var client *plclib.Client
	var clientDetails string

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

		if DetailedLogging {
			log.Printf("Conectando ao PLC %s (ID=%d) com VLAN para escrita em lote...", plcConfig.Name, plcConfig.ID)
		}

		clientDetails = fmt.Sprintf("Configuração VLAN: IP=%s, Gateway=%s, VLAN ID=%d",
			plcConfig.IPAddress, plcConfig.Gateway, plcConfig.VLANID)

		client, err = plclib.NewClientWithConfig(config)
	} else {
		if DetailedLogging {
			log.Printf("Conectando ao PLC %s (ID=%d, IP: %s) para escrita em lote...", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
		}

		clientDetails = fmt.Sprintf("IP=%s, Rack=%d, Slot=%d",
			plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)

		client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
	}

	if err != nil {
		m.logger.ErrorWithDetails("PLC Writer",
			fmt.Sprintf("Erro de conexão ao PLC %s para escrita em lote", plcConfig.Name),
			fmt.Sprintf("%s, Erro: %v, Tags solicitadas: %d",
				clientDetails, err, tagsTotal))

		// Atualiza o status para offline já que não conseguiu conectar
		offlineStatus := database.PLCStatus{
			PLCID:      plcConfig.ID,
			Status:     "offline",
			LastUpdate: time.Now(),
		}

		// Usa DB específico para atualização
		dbForUpdate := m.db.WithTimeout(5 * time.Second)
		if errUpd := dbForUpdate.UpdatePLCStatus(offlineStatus); errUpd != nil {
			if DetailedLogging {
				log.Printf("Erro ao atualizar status offline do PLC ID %d: %v", plcConfig.ID, errUpd)
			}
		}

		return nil, fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, err)
	}
	defer client.Close()

	// Processar cada tag
	for tagName, value := range tagValues {
		tag, exists := tagsByName[tagName]
		if !exists {
			results[tagName] = fmt.Errorf("tag '%s' não encontrada", tagName)
			tagsFailed++
			continue
		}

		if !tag.CanWrite {
			m.logger.WarnWithDetails("PLC Writer",
				"Tentativa de escrita em tag sem permissão durante operação em lote",
				fmt.Sprintf("Tag: %s, PLC: %s (ID=%d)", tagName, plcConfig.Name, plcConfig.ID))

			results[tagName] = fmt.Errorf("tag '%s' não permite escrita", tagName)
			tagsFailed++
			continue
		}

		// Escrever no PLC
		if err := client.WriteTag(tag.DBNumber, tag.ByteOffset, tag.DataType, value); err != nil {
			m.logger.ErrorWithDetails("PLC Writer",
				fmt.Sprintf("Falha na escrita para tag %s durante operação em lote", tagName),
				fmt.Sprintf("PLC: %s (ID=%d), DB: %d, ByteOffset: %.1f, DataType: %s, Valor: %v, Erro: %v",
					plcConfig.Name, plcConfig.ID, tag.DBNumber, tag.ByteOffset, tag.DataType, value, err))

			results[tagName] = fmt.Errorf("erro ao escrever: %w", err)
			tagsFailed++
			continue
		}

		if DetailedLogging {
			log.Printf("Valor escrito com sucesso para tag '%s'", tagName)
		}

		tagsSuccess++

		// Atualizar no Redis
		if err := m.cache.SetTagValue(plcID, tag.ID, value); err != nil {
			if DetailedLogging {
				log.Printf("Aviso: Erro ao atualizar Redis para tag %s: %v", tagName, err)
			}
		} else {
			_ = m.cache.PublishTagUpdate(plcID, tag.ID, value)
		}

		// Sucesso para esta tag
		results[tagName] = nil
	}

	// Registrar o resumo da operação
	m.logger.InfoWithDetails("PLC Writer",
		fmt.Sprintf("Escrita em lote concluída para o PLC %s", plcConfig.Name),
		fmt.Sprintf("Tags solicitadas: %d, Sucesso: %d, Falhas: %d",
			tagsTotal, tagsSuccess, tagsFailed))

	return results, nil
}

// ValidateValueForTag verifica se um valor é apropriado para o tipo de dado da tag
func (m *Manager) ValidateValueForTag(tagID int, value interface{}) error {
	// Buscar a tag para verificar seu tipo de dados
	tag, err := m.db.GetTagByID(tagID)
	if err != nil {
		return fmt.Errorf("erro ao buscar tag para validação: %w", err)
	}

	// Validar conforme o tipo de dados
	switch tag.DataType {
	case "bool":
		// Para bool, aceita bool direto ou valores conversíveis (0, 1, "true", "false", etc)
		switch v := value.(type) {
		case bool:
			// Tipo correto, nada a fazer
			return nil
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// Números são considerados válidos (0=false, !0=true)
			return nil
		case float32, float64:
			// Floats são aceitos (0.0=false, !0.0=true)
			return nil
		case string:
			// Strings precisam ser "true", "false", "0", "1", etc
			lower := strings.ToLower(v)
			if lower == "true" || lower == "false" || lower == "0" || lower == "1" ||
				lower == "yes" || lower == "no" || lower == "sim" || lower == "não" {
				return nil
			}
			return fmt.Errorf("string '%s' não pode ser convertida para booleano", v)
		default:
			return fmt.Errorf("tipo %T não pode ser convertido para booleano", value)
		}

	case "int", "int16", "word", "uint16":
		// Verificar limites para int16/word/uint16
		var val int64
		switch v := value.(type) {
		case int:
			val = int64(v)
		case int8:
			val = int64(v)
		case int16:
			val = int64(v)
		case int32:
			val = int64(v)
		case int64:
			val = v
		case uint:
			val = int64(v)
		case uint8:
			val = int64(v)
		case uint16:
			val = int64(v)
		case uint32:
			val = int64(v)
		case uint64:
			if v > uint64(^uint16(0)) {
				return fmt.Errorf("valor %d fora do limite para %s (max: %d)",
					v, tag.DataType, uint16(^uint16(0)))
			}
			val = int64(v)
		case float32:
			if v < -32768 || v > 65535 {
				return fmt.Errorf("valor %f fora do limite para %s", v, tag.DataType)
			}
			val = int64(v)
		case float64:
			if v < -32768 || v > 65535 {
				return fmt.Errorf("valor %f fora do limite para %s", v, tag.DataType)
			}
			val = int64(v)
		case string:
			parsed, err := strconv.ParseInt(v, 10, 16)
			if err != nil {
				return fmt.Errorf("string '%s' não pode ser convertida para %s: %v",
					v, tag.DataType, err)
			}
			val = parsed
		default:
			return fmt.Errorf("tipo %T não pode ser convertido para %s", value, tag.DataType)
		}

		// Verifica se o valor está dentro dos limites do tipo
		if tag.DataType == "uint16" || tag.DataType == "word" {
			if val < 0 || val > 65535 {
				return fmt.Errorf("valor %d fora do limite para %s (0-65535)",
					val, tag.DataType)
			}
		} else { // int16
			if val < -32768 || val > 32767 {
				return fmt.Errorf("valor %d fora do limite para %s (-32768 a 32767)",
					val, tag.DataType)
			}
		}

	case "dint", "int32", "dword", "uint32":
		// Verificar limites para int32/dint/dword/uint32
		switch v := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32:
			// Tipos que seguramente cabem em int32/uint32
			return nil
		case uint64:
			if v > uint64(^uint32(0)) {
				return fmt.Errorf("valor %d fora do limite para %s (max: %d)",
					v, tag.DataType, uint32(^uint32(0)))
			}
			return nil
		case float32, float64:
			// Floats precisam estar dentro dos limites
			f64 := float64(0)
			if fv, ok := v.(float64); ok {
				f64 = fv
			} else {
				f64 = float64(v.(float32))
			}

			if tag.DataType == "uint32" || tag.DataType == "dword" {
				if f64 < 0 || f64 > 4294967295 {
					return fmt.Errorf("valor %f fora do limite para %s (0-4294967295)",
						f64, tag.DataType)
				}
			} else { // int32/dint
				if f64 < -2147483648 || f64 > 2147483647 {
					return fmt.Errorf("valor %f fora do limite para %s (-2147483648 a 2147483647)",
						f64, tag.DataType)
				}
			}
			return nil
		case string:
			if tag.DataType == "uint32" || tag.DataType == "dword" {
				_, err := strconv.ParseUint(v, 10, 32)
				if err != nil {
					return fmt.Errorf("string '%s' não pode ser convertida para %s: %v",
						v, tag.DataType, err)
				}
			} else { // int32/dint
				_, err := strconv.ParseInt(v, 10, 32)
				if err != nil {
					return fmt.Errorf("string '%s' não pode ser convertida para %s: %v",
						v, tag.DataType, err)
				}
			}
			return nil
		default:
			return fmt.Errorf("tipo %T não pode ser convertido para %s", value, tag.DataType)
		}

	case "real", "float32":
		// Para float, verificar se o valor está dentro de limites razoáveis
		var val float64
		switch v := value.(type) {
		case float32:
			val = float64(v)
		case float64:
			val = v
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			// Tipos numéricos inteiros são sempre conversíveis para float
			return nil
		case string:
			parsed, err := strconv.ParseFloat(v, 32)
			if err != nil {
				return fmt.Errorf("string '%s' não pode ser convertida para float: %v", v, err)
			}
			val = parsed
		default:
			return fmt.Errorf("tipo %T não pode ser convertido para float", value)
		}

		// Verificar se está fora dos limites de float32
		if val > 3.4e38 || val < -3.4e38 {
			return fmt.Errorf("valor %f fora do limite para float32", val)
		}

	case "string":
		// Converter para string se possível
		switch v := value.(type) {
		case string:
			if len(v) > 254 {
				return fmt.Errorf("string muito longa (máx: 254 caracteres, atual: %d)", len(v))
			}
			return nil
		case []byte:
			if len(v) > 254 {
				return fmt.Errorf("string muito longa (máx: 254 caracteres, atual: %d)", len(v))
			}
			return nil
		default:
			// Outros tipos podem ser convertidos para string na escrita
			return nil
		}

	default:
		// Para tipos não conhecidos, aceitar o valor e deixar o driver PLC validar
		m.logger.WarnWithDetails("PLC Writer",
			"Tipo de dados desconhecido para validação",
			fmt.Sprintf("Tag: %s (ID=%d), DataType: %s", tag.Name, tag.ID, tag.DataType))
	}

	return nil
}

// LogWriteOperation registra operações de escrita em formato padronizado
// Esta função é usada internamente pelos métodos Write* para consistência
func (m *Manager) LogWriteOperation(plcID int, tagID int, value interface{}, result error) {
	// Obtém detalhes da tag e PLC para enriquecer o log
	var plcName, tagName, dataType string
	var dbNumber int
	var byteOffset float64

	// Tenta buscar PLC do cache primeiro
	plcJSON, _ := m.cache.GetValue(fmt.Sprintf("config:plc:%d", plcID))
	if plcJSON != "" {
		var plc database.PLC
		if err := json.Unmarshal([]byte(plcJSON), &plc); err == nil {
			plcName = plc.Name
		}
	}

	// Se não conseguiu do cache, tenta do banco
	if plcName == "" {
		plc, err := m.db.GetPLCByID(plcID)
		if err == nil && plc != nil {
			plcName = plc.Name
		} else {
			plcName = fmt.Sprintf("PLC-%d", plcID)
		}
	}

	// Tenta buscar tag do cache primeiro
	tagJSON, _ := m.cache.GetValue(fmt.Sprintf("config:plc:%d:tag:%d", plcID, tagID))
	if tagJSON != "" {
		var tag database.Tag
		if err := json.Unmarshal([]byte(tagJSON), &tag); err == nil {
			tagName = tag.Name
			dataType = tag.DataType
			dbNumber = tag.DBNumber
			byteOffset = tag.ByteOffset
		}
	}

	// Se não conseguiu do cache, tenta do banco
	if tagName == "" {
		tag, err := m.db.GetTagByID(tagID)
		if err == nil {
			tagName = tag.Name
			dataType = tag.DataType
			dbNumber = tag.DBNumber
			byteOffset = tag.ByteOffset
		} else {
			tagName = fmt.Sprintf("Tag-%d", tagID)
		}
	}

	// Preparar mensagem e detalhes do log
	var logMsg, logDetails string
	var logLevel database.LogLevel

	if result == nil {
		// Operação bem-sucedida
		logMsg = fmt.Sprintf("Escrita realizada com sucesso na tag %s", tagName)
		logDetails = fmt.Sprintf("PLC: %s (ID=%d), Tag: %s (ID=%d), DB: %d, Offset: %.1f, DataType: %s, Valor: %v",
			plcName, plcID, tagName, tagID, dbNumber, byteOffset, dataType, value)
		logLevel = database.LogInfo
	} else {
		// Operação falhou
		logMsg = fmt.Sprintf("Falha na escrita da tag %s", tagName)
		logDetails = fmt.Sprintf("PLC: %s (ID=%d), Tag: %s (ID=%d), DB: %d, Offset: %.1f, DataType: %s, Valor: %v, Erro: %v",
			plcName, plcID, tagName, tagID, dbNumber, byteOffset, dataType, value, result)
		logLevel = database.LogError
	}

	// Registrar log usando o sistema de logging adequado
	if logLevel == database.LogInfo {
		m.logger.InfoWithDetails("PLC Writer", logMsg, logDetails)
	} else {
		m.logger.ErrorWithDetails("PLC Writer", logMsg, logDetails)
	}
}

// WriteTagValueWithConfirmation escreve um valor e confirma a escrita lendo o valor depois
func (m *Manager) WriteTagValueWithConfirmation(plcID int, tagID int, value interface{}, maxRetries int) error {
	// Primeiro, valida o valor para o tipo da tag
	if err := m.ValidateValueForTag(tagID, value); err != nil {
		m.logger.ErrorWithDetails("PLC Writer",
			"Valor inválido para escrita",
			fmt.Sprintf("Tag ID: %d, Valor: %v, Erro: %v", tagID, value, err))
		return fmt.Errorf("valor inválido: %w", err)
	}

	// Escreve o valor
	err := m.WriteTagByID(plcID, tagID, value)
	if err != nil {
		// Erro já será logado pelo WriteTagByID
		return err
	}

	// Aguarda um momento para que o valor seja atualizado no PLC
	time.Sleep(100 * time.Millisecond)

	// Tenta confirmar a escrita lendo o valor
	tries := 0
	for tries < maxRetries {
		// Usar o getOrCreatePLCClient para obter uma conexão
		client, err := getOrCreatePLCClient(m, plcID)
		if err != nil {
			m.logger.WarnWithDetails("PLC Writer",
				"Não foi possível confirmar escrita - erro de conexão",
				fmt.Sprintf("PLC ID: %d, Tag ID: %d, Tentativa: %d/%d, Erro: %v",
					plcID, tagID, tries+1, maxRetries, err))
			tries++
			time.Sleep(250 * time.Millisecond)
			continue
		}
		defer client.Close()

		// Buscar a tag
		tag, err := m.db.GetTagByID(tagID)
		if err != nil {
			m.logger.ErrorWithDetails("PLC Writer",
				"Erro ao obter metadata da tag para confirmação",
				fmt.Sprintf("Tag ID: %d, Erro: %v", tagID, err))
			return fmt.Errorf("erro ao confirmar escrita - tag não encontrada: %w", err)
		}

		// Fazer a leitura direta
		byteOffset := int(tag.ByteOffset)
		readValue, err := client.ReadTag(tag.DBNumber, byteOffset, tag.DataType, tag.BitOffset)
		if err != nil {
			m.logger.WarnWithDetails("PLC Writer",
				"Falha na leitura de confirmação",
				fmt.Sprintf("Tag: %s (ID=%d), Tentativa: %d/%d, Erro: %v",
					tag.Name, tagID, tries+1, maxRetries, err))
			tries++
			time.Sleep(250 * time.Millisecond)
			continue
		}

		// Comparar os valores
		if plclib.CompareValues(value, readValue) {
			// Valores correspondem, escrita confirmada
			m.logger.InfoWithDetails("PLC Writer",
				fmt.Sprintf("Escrita na tag %s confirmada com sucesso", tag.Name),
				fmt.Sprintf("PLC ID: %d, Tag ID: %d, Valor: %v", plcID, tagID, value))
			return nil
		}

		// Valores diferentes, registra e tenta novamente
		m.logger.WarnWithDetails("PLC Writer",
			"Valores não correspondem na confirmação de escrita",
			fmt.Sprintf("Tag: %s, Escrito: %v, Lido: %v, Tentativa: %d/%d",
				tag.Name, value, readValue, tries+1, maxRetries))

		tries++
		if tries < maxRetries {
			// Tenta escrever novamente
			if err := m.WriteTagByID(plcID, tagID, value); err != nil {
				// Erro já logado pelo WriteTagByID
				continue
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	// Falha após várias tentativas
	m.logger.ErrorWithDetails("PLC Writer",
		"Falha na confirmação de escrita após múltiplas tentativas",
		fmt.Sprintf("PLC ID: %d, Tag ID: %d, Valor: %v, Tentativas: %d",
			plcID, tagID, value, maxRetries))

	return fmt.Errorf("falha na confirmação de escrita após %d tentativas", maxRetries)
}

// WriteScheduled agenda uma escrita para ser executada em segundo plano
type ScheduledWrite struct {
	PLCID     int
	TagID     int
	Value     interface{}
	Timestamp time.Time
	Executed  bool
	Result    error
}

var scheduledWrites []ScheduledWrite
var scheduleMutex sync.Mutex
var scheduleRunning bool

// ScheduleWrite agenda uma escrita para ser executada em segundo plano
func (m *Manager) ScheduleWrite(plcID, tagID int, value interface{}, execTime time.Time) (int, error) {
	// Validar o valor antes de agendar
	if err := m.ValidateValueForTag(tagID, value); err != nil {
		return -1, fmt.Errorf("valor inválido para agendamento: %w", err)
	}

	write := ScheduledWrite{
		PLCID:     plcID,
		TagID:     tagID,
		Value:     value,
		Timestamp: execTime,
		Executed:  false,
	}

	scheduleMutex.Lock()
	defer scheduleMutex.Unlock()

	// Adicionar à lista
	scheduledWrites = append(scheduledWrites, write)
	writeID := len(scheduledWrites) - 1

	// Buscar tag e PLC para enriquecer o log
	tag, _ := m.db.GetTagByID(tagID)
	plc, _ := m.db.GetPLCByID(plcID)

	var tagName, plcName string
	if tag != nil {
		tagName = tag.Name
	} else {
		tagName = fmt.Sprintf("Tag-%d", tagID)
	}

	if plc != nil {
		plcName = plc.Name
	} else {
		plcName = fmt.Sprintf("PLC-%d", plcID)
	}

	// Registrar o agendamento
	m.logger.InfoWithDetails("PLC Writer",
		fmt.Sprintf("Escrita agendada para tag %s", tagName),
		fmt.Sprintf("PLC: %s, ID do agendamento: %d, Valor: %v, Data/hora: %s",
			plcName, writeID, value, execTime.Format(time.RFC3339)))

	// Iniciar o processador se ainda não estiver rodando
	if !scheduleRunning {
		scheduleRunning = true
		go m.processScheduledWrites()
	}

	return writeID, nil
}

// processScheduledWrites processa escritas agendadas em segundo plano
func (m *Manager) processScheduledWrites() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()

			scheduleMutex.Lock()

			// Verifica se há escritas pendentes
			pendingWrites := false
			for i := range scheduledWrites {
				if !scheduledWrites[i].Executed && scheduledWrites[i].Timestamp.Before(now) {
					pendingWrites = true
					break
				}
			}

			// Se não há mais escritas pendentes, encerra o processador
			if !pendingWrites && len(scheduledWrites) > 0 {
				// Limpar escritas antigas já executadas
				var activeWrites []ScheduledWrite
				for _, write := range scheduledWrites {
					// Manter apenas escritas não executadas ou recentes (últimas 24h)
					if !write.Executed || time.Since(write.Timestamp) < 24*time.Hour {
						activeWrites = append(activeWrites, write)
					}
				}
				scheduledWrites = activeWrites

				// Se não houver mais escritas pendentes após a limpeza, encerrar
				pendingWrites = false
				for i := range scheduledWrites {
					if !scheduledWrites[i].Executed {
						pendingWrites = true
						break
					}
				}

				if !pendingWrites {
					scheduleRunning = false
					scheduleMutex.Unlock()
					return
				}
			}

			// Processar escritas agendadas
			for i := range scheduledWrites {
				write := &scheduledWrites[i]

				// Pular escritas já executadas ou futuras
				if write.Executed || write.Timestamp.After(now) {
					continue
				}

				// Executar a escrita desagendada
				err := m.WriteTagByID(write.PLCID, write.TagID, write.Value)

				// Atualizar o status
				write.Executed = true
				write.Result = err

				// Log já é gerado pelo WriteTagByID
			}

			scheduleMutex.Unlock()
		}
	}
}

// GetScheduledWrite retorna informações sobre uma escrita agendada
func (m *Manager) GetScheduledWrite(writeID int) (*ScheduledWrite, error) {
	scheduleMutex.Lock()
	defer scheduleMutex.Unlock()

	if writeID < 0 || writeID >= len(scheduledWrites) {
		return nil, fmt.Errorf("ID de escrita agendada inválido: %d", writeID)
	}

	write := scheduledWrites[writeID]
	return &write, nil
}

// CancelScheduledWrite cancela uma escrita agendada que ainda não foi executada
func (m *Manager) CancelScheduledWrite(writeID int) error {
	scheduleMutex.Lock()
	defer scheduleMutex.Unlock()

	if writeID < 0 || writeID >= len(scheduledWrites) {
		return fmt.Errorf("ID de escrita agendada inválido: %d", writeID)
	}

	write := &scheduledWrites[writeID]

	if write.Executed {
		return fmt.Errorf("escrita já foi executada, não pode ser cancelada")
	}

	// Buscar tag e PLC para enriquecer o log
	tag, _ := m.db.GetTagByID(write.TagID)
	plc, _ := m.db.GetPLCByID(write.PLCID)

	var tagName, plcName string
	if tag != nil {
		tagName = tag.Name
	} else {
		tagName = fmt.Sprintf("Tag-%d", write.TagID)
	}

	if plc != nil {
		plcName = plc.Name
	} else {
		plcName = fmt.Sprintf("PLC-%d", write.PLCID)
	}

	// Marcar como executada com erro de cancelamento
	write.Executed = true
	write.Result = fmt.Errorf("escrita cancelada pelo usuário")

	// Registrar o cancelamento
	m.logger.InfoWithDetails("PLC Writer",
		fmt.Sprintf("Escrita agendada cancelada para tag %s", tagName),
		fmt.Sprintf("PLC: %s, ID do agendamento: %d, Valor: %v, Data/hora original: %s",
			plcName, writeID, write.Value, write.Timestamp.Format(time.RFC3339)))

	return nil
}
