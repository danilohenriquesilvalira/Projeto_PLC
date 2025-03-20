//status_monitor.go

package plcmanager

import (
	"context"
	"fmt"
	"log"
	"time"

	"Projeto_PLC/internal/database"
	plclib "Projeto_PLC/internal/plc"
)

// updatePLCStatus monitora periodicamente o status de conexão do PLC
func (m *Manager) updatePLCStatus(ctx context.Context, plcID int, client *plclib.Client) error {
	ticker := time.NewTicker(10 * time.Second) // Reduzido para 10s para testes
	defer ticker.Stop()

	failCount := 0
	threshold := 3
	var lastStatus string

	// Inicia com um ping imediato para verificar o status inicial
	initialStatus := "unknown"
	if err := client.Ping(); err != nil {
		initialStatus = "offline"
		log.Printf("PLC ID %d: Ping inicial falhou: %v", plcID, err)
		failCount++
	} else {
		initialStatus = "online"
		log.Printf("PLC ID %d: Ping inicial bem-sucedido", plcID)
	}

	// Atualiza o status inicial
	status := database.PLCStatus{
		PLCID:      plcID,
		Status:     initialStatus,
		LastUpdate: time.Now(),
	}

	if err := m.db.UpdatePLCStatus(status); err != nil {
		log.Printf("Erro ao atualizar status inicial do PLC ID %d: %v", plcID, err)
		m.logger.Error("Erro ao atualizar status do PLC", fmt.Sprintf("PLC ID %d: %v", plcID, err))
	} else {
		log.Printf("Status inicial do PLC ID %d definido como: %s", plcID, initialStatus)
		m.logger.Info("Status do PLC atualizado", fmt.Sprintf("PLC ID %d: %s", plcID, initialStatus))
		lastStatus = initialStatus
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("Encerrando atualização de status do PLC %d", plcID)
			return nil

		case <-ticker.C:
			log.Printf("Verificando status do PLC ID %d", plcID)
			status := database.PLCStatus{
				PLCID:      plcID,
				LastUpdate: time.Now(),
			}

			if err := client.Ping(); err != nil {
				status.Status = "offline"
				failCount++
				log.Printf("Ping falhou para PLC ID %d: %v (falha %d/%d)", plcID, err, failCount, threshold)
				m.logger.Error("Ping falhou para PLC", fmt.Sprintf("PLC ID %d: %v", plcID, err))
			} else {
				status.Status = "online"
				failCount = 0
				log.Printf("Ping bem-sucedido para PLC ID %d", plcID)
			}

			if err := m.db.UpdatePLCStatus(status); err != nil {
				log.Printf("Erro ao atualizar status do PLC ID %d: %v", plcID, err)
				m.logger.Error("Erro ao atualizar status do PLC", fmt.Sprintf("PLC ID %d: %v", plcID, err))
			} else if status.Status != lastStatus {
				log.Printf("Status do PLC ID %d atualizado: %s -> %s", plcID, lastStatus, status.Status)
				m.logger.Info("Status do PLC atualizado", fmt.Sprintf("PLC ID %d: %s", plcID, status.Status))
				lastStatus = status.Status
			}

			if failCount >= threshold {
				err := fmt.Errorf("muitas falhas consecutivas de ping (%d) para o PLC ID %d", failCount, plcID)
				log.Printf("ERRO CRÍTICO: %v", err)
				return err
			}
		}
	}
}

// GetPLCStatus retorna o status atual de um PLC
func (m *Manager) GetPLCStatus(plcID int) (string, error) {
	plc, err := m.db.GetPLCByID(plcID)
	if err != nil {
		return "", fmt.Errorf("erro ao obter PLC: %w", err)
	}

	if plc.Status == "" {
		return "unknown", nil
	}

	return plc.Status, nil
}

// GetAllPLCStatus retorna o status de todos os PLCs ativos
func (m *Manager) GetAllPLCStatus() (map[int]string, error) {
	plcs, err := m.db.GetActivePLCs()
	if err != nil {
		return nil, fmt.Errorf("erro ao obter PLCs ativos: %w", err)
	}

	status := make(map[int]string)
	for _, plc := range plcs {
		status[plc.ID] = plc.Status
		if plc.Status == "" {
			status[plc.ID] = "unknown"
		}
	}

	return status, nil
}

// ForcePingPLC força uma verificação de ping imediata em um PLC
func (m *Manager) ForcePingPLC(plcID int) (string, error) {
	// Busca o PLC no banco de dados
	plcConfig, err := m.db.GetPLCByID(plcID)
	if err != nil {
		return "", fmt.Errorf("erro ao buscar dados do PLC: %w", err)
	}

	// Cria uma conexão temporária para testar
	var client *plclib.Client

	if plcConfig.UseVLAN && plcConfig.Gateway != "" {
		config := plclib.ClientConfig{
			IPAddress:  plcConfig.IPAddress,
			Rack:       plcConfig.Rack,
			Slot:       plcConfig.Slot,
			Timeout:    5 * time.Second, // Timeout mais curto para ping direto
			UseVLAN:    true,
			Gateway:    plcConfig.Gateway,
			SubnetMask: plcConfig.SubnetMask,
			VLANID:     plcConfig.VLANID,
		}

		client, err = plclib.NewClientWithConfig(config)
	} else {
		client, err = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
	}

	if err != nil {
		// Falha na conexão, atualiza status
		status := database.PLCStatus{
			PLCID:      plcID,
			Status:     "offline",
			LastUpdate: time.Now(),
		}

		_ = m.db.UpdatePLCStatus(status)
		return "offline", fmt.Errorf("erro ao conectar ao PLC: %w", err)
	}
	defer client.Close()

	// Tenta fazer ping
	if err := client.Ping(); err != nil {
		// Ping falhou
		status := database.PLCStatus{
			PLCID:      plcID,
			Status:     "offline",
			LastUpdate: time.Now(),
		}

		_ = m.db.UpdatePLCStatus(status)
		return "offline", fmt.Errorf("ping falhou: %w", err)
	}

	// Ping bem-sucedido
	status := database.PLCStatus{
		PLCID:      plcID,
		Status:     "online",
		LastUpdate: time.Now(),
	}

	if err := m.db.UpdatePLCStatus(status); err != nil {
		m.logger.Error("Erro ao atualizar status do PLC", fmt.Sprintf("PLC ID %d: %v", plcID, err))
	}

	return "online", nil
}
