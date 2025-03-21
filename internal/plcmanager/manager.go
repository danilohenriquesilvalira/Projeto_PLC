package plcmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"Projeto_PLC/internal/cache"
	"Projeto_PLC/internal/database"
	plclib "Projeto_PLC/internal/plc" // Renomeado para evitar conflitos
)

// Manager controla a gerência de PLCs e suas tags
type Manager struct {
	db               *database.DB
	cache            cache.Cache
	logger           *database.Logger
	plcPool          *plclib.ConnectionPool // Novo campo para pool de conexões
	plcCancels       map[int]context.CancelFunc
	mutex            sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	checkInterval    time.Duration
	watchdogInterval time.Duration  // Intervalo para o watchdog verificar conexões
	wg               sync.WaitGroup // Para encerramento gracioso
}

// NewManager cria uma nova instância do gerenciador de PLCs
func NewManager(ctx context.Context, db *database.DB, cacheProvider cache.Cache,
	logger *database.Logger, plcPool *plclib.ConnectionPool) *Manager {
	ctx, cancel := context.WithCancel(ctx)

	return &Manager{
		db:               db,
		cache:            cacheProvider,
		logger:           logger,
		plcPool:          plcPool,
		plcCancels:       make(map[int]context.CancelFunc),
		mutex:            sync.RWMutex{},
		ctx:              ctx,
		cancel:           cancel,
		checkInterval:    5 * time.Second,
		watchdogInterval: 30 * time.Second, // Intervalo para verificação de conexões
	}
}

// Start inicia o gerenciamento de PLCs
func (m *Manager) Start() error {
	// Verificação de segurança para parâmetros nulos
	if m.db == nil {
		return fmt.Errorf("database nulo para gerenciamento de PLCs")
	}

	if m.cache == nil {
		return fmt.Errorf("cache nulo para gerenciamento de PLCs")
	}

	if m.logger == nil {
		return fmt.Errorf("logger nulo para gerenciamento de PLCs")
	}

	log.Println("Iniciando gerenciador de PLCs")
	m.logger.Info("Gerenciador de PLCs", "Iniciando monitoramento de PLCs")

	// Inicia a goroutine principal para gerenciar os PLCs
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runPLCManager()
	}()

	// Iniciar o watchdog
	m.startWatchdog()

	return nil
}

// Stop para todas as goroutines de monitoramento
func (m *Manager) Stop() {
	log.Println("Parando gerenciador de PLCs")
	m.logger.Info("Gerenciador de PLCs", "Parando monitoramento")

	// Cancela o contexto principal, o que irá propagar para todos os sub-contextos
	m.cancel()

	// Aguarda que todas as goroutines terminem com timeout
	waitChan := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		log.Println("Gerenciador de PLCs parado com sucesso")
	case <-time.After(5 * time.Second):
		log.Println("Timeout ao aguardar término de goroutines do gerenciador de PLCs")
	}
}

// startWatchdog inicia o watchdog para monitorar e reiniciar conexões
func (m *Manager) startWatchdog() {
	log.Println("Iniciando watchdog para monitorar conexões PLC")
	ticker := time.NewTicker(m.watchdogInterval)
	defer ticker.Stop()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		for {
			select {
			case <-m.ctx.Done():
				log.Println("Watchdog encerrado")
				return

			case <-ticker.C:
				// Verificar conexões de todos os PLCs gerenciados
				m.checkAndRestartConnections()
			}
		}
	}()
}

// checkAndRestartConnections verifica e reinicia conexões
func (m *Manager) checkAndRestartConnections() {
	// Obtém a lista de PLCs do cache
	plcsJSON, err := m.cache.GetValue("config:plcs:all")
	if err != nil || plcsJSON == "" {
		log.Printf("Watchdog: Erro ao obter lista de PLCs do cache: %v", err)
		// Fallback para banco de dados
		plcs, dbErr := m.db.GetActivePLCs()
		if dbErr != nil {
			log.Printf("Watchdog: Erro ao obter PLCs do banco de dados: %v", dbErr)
			return
		}
		m.checkPLCConnections(plcs)
		return
	}

	// Deserializar os PLCs do JSON
	var plcs []database.PLC
	if err := json.Unmarshal([]byte(plcsJSON), &plcs); err != nil {
		log.Printf("Watchdog: Erro ao deserializar PLCs: %v", err)
		// Fallback para banco de dados
		plcs, dbErr := m.db.GetActivePLCs()
		if dbErr != nil {
			log.Printf("Watchdog: Erro ao obter PLCs do banco de dados: %v", dbErr)
			return
		}
		m.checkPLCConnections(plcs)
		return
	}

	// Verificar cada PLC
	m.checkPLCConnections(plcs)
}

// checkPLCConnections verifica conexões para uma lista de PLCs
func (m *Manager) checkPLCConnections(plcs []database.PLC) {
	for _, plc := range plcs {
		if !plc.Active {
			continue
		}

		// Verificar se o PLC tem status 'offline'
		status, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d:status", plc.ID))
		if err == nil && status == "offline" {
			log.Printf("Watchdog: PLC %s (ID=%d) está offline, tentando reiniciar",
				plc.Name, plc.ID)

			// Forçar ping para verificar status real
			newStatus, err := m.ForcePingPLC(plc.ID)
			if err != nil {
				log.Printf("Watchdog: Erro ao verificar conexão com PLC %s: %v",
					plc.Name, err)
				continue
			}

			if newStatus == "online" {
				log.Printf("Watchdog: PLC %s (ID=%d) voltou a ficar online!",
					plc.Name, plc.ID)

				// Reiniciar monitoramento
				m.restartPLCMonitoring(plc.ID)
			}
		}
	}
}

// restartPLCMonitoring reinicia o monitoramento de um PLC específico
func (m *Manager) restartPLCMonitoring(plcID int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Verificar se temos um cancelFunc para este PLC
	if cancelFunc, exists := m.plcCancels[plcID]; exists {
		// Cancela a goroutine atual
		cancelFunc()
		delete(m.plcCancels, plcID)

		log.Printf("Cancelado monitoramento existente do PLC ID %d para reinício", plcID)
	}

	// Buscar PLC atualizado
	plcConfig, err := m.db.GetPLCByID(plcID)
	if err != nil {
		log.Printf("Erro ao buscar PLC ID %d para reinício: %v", plcID, err)
		m.logger.Error("Watchdog", fmt.Sprintf("Erro ao reiniciar PLC ID %d: %v", plcID, err))
		return
	}

	if !plcConfig.Active {
		log.Printf("PLC ID %d não está mais ativo, não reiniciará", plcID)
		return
	}

	// Iniciar uma nova goroutine para monitorar este PLC
	plcCtx, plcCancel := context.WithCancel(m.ctx)
	m.plcCancels[plcID] = plcCancel

	m.wg.Add(1)
	go func(ctx context.Context, config database.PLC) {
		defer m.wg.Done()
		m.runPLC(ctx, config)
	}(plcCtx, *plcConfig)

	log.Printf("Reiniciado monitoramento do PLC %s (ID: %d)",
		plcConfig.Name, plcConfig.ID)
	m.logger.Info("Watchdog", fmt.Sprintf("Reiniciado PLC %s (ID: %d)",
		plcConfig.Name, plcConfig.ID))
}

// runPLCManager é a goroutine principal que gerencia a lista de PLCs ativos
func (m *Manager) runPLCManager() {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	log.Println("Iniciando monitoramento de PLCs - procurando PLCs ativos")

	for {
		select {
		case <-m.ctx.Done():
			// Encerra todas as goroutines de PLC quando o contexto for cancelado
			m.mutex.Lock()
			for _, cancelFunc := range m.plcCancels {
				cancelFunc()
			}
			m.mutex.Unlock()
			return

		case <-ticker.C:
			// Usa uma função separada para evitar leaks de contexto
			m.checkAndManagePLCs()
		}
	}
}

// checkAndManagePLCs verifica e gerencia os PLCs ativos
func (m *Manager) checkAndManagePLCs() {
	// Tenta obter PLCs do cache
	plcsJSON, err := m.cache.GetValue("config:plcs:all")
	if err != nil || plcsJSON == "" {
		// Fallback para banco de dados em caso de erro no cache
		m.logger.Error("PLC Manager", "Erro ao carregar PLCs do cache, usando banco de dados")
		log.Printf("Erro ao carregar PLCs do cache: %v, usando banco de dados", err)

		// Usa diretamente o banco de dados
		plcs, dbErr := m.db.GetActivePLCs()
		if dbErr != nil {
			m.logger.Error("PLC Manager", "Erro ao carregar PLCs do banco: "+dbErr.Error())
			log.Printf("Erro ao carregar PLCs do banco: %v", dbErr)
			return
		}

		log.Printf("Encontrados %d PLCs ativos (do banco de dados)", len(plcs))

		// Continua o processamento com os PLCs do banco
		m.processPLCs(plcs)
		return
	}

	// Deserializa PLCs do cache
	var plcs []database.PLC
	if err := json.Unmarshal([]byte(plcsJSON), &plcs); err != nil {
		m.logger.Error("PLC Manager", "Erro ao deserializar PLCs do cache: "+err.Error())
		log.Printf("Erro ao deserializar PLCs do cache: %v, usando banco de dados", err)

		// Fallback para banco de dados
		plcs, dbErr := m.db.GetActivePLCs()
		if dbErr != nil {
			m.logger.Error("PLC Manager", "Erro ao carregar PLCs do banco: "+dbErr.Error())
			log.Printf("Erro ao carregar PLCs do banco: %v", dbErr)
			return
		}

		log.Printf("Encontrados %d PLCs ativos (do banco de dados - fallback)", len(plcs))

		// Continua o processamento com os PLCs do banco
		m.processPLCs(plcs)
		return
	}

	log.Printf("Encontrados %d PLCs ativos (do cache)", len(plcs))

	// Processa os PLCs obtidos do cache
	m.processPLCs(plcs)
}

// processPLCs processa a lista de PLCs ativos
func (m *Manager) processPLCs(plcs []database.PLC) {
	// Mapeia PLCs ativos por ID para verificação rápida
	activePLCs := make(map[int]database.PLC)
	for _, plc := range plcs {
		activePLCs[plc.ID] = plc
	}

	// Remove PLCs inativos
	m.mutex.Lock()
	for plcID, cancelFunc := range m.plcCancels {
		if _, exists := activePLCs[plcID]; !exists {
			cancelFunc()
			delete(m.plcCancels, plcID)
			log.Printf("Removendo monitoramento do PLC ID %d - não está mais ativo", plcID)
			m.logger.Info("PLC removido", fmt.Sprintf("PLC ID: %d - inativo", plcID))
		}
	}

	// Adiciona novos PLCs ou atualiza configurações de PLCs existentes
	for id, plcConfig := range activePLCs {
		// Validações básicas
		if plcConfig.IPAddress == "" {
			m.logger.Error("PLC com endereço IP vazio", fmt.Sprintf("PLC ID: %d, Nome: %s", plcConfig.ID, plcConfig.Name))
			log.Printf("ERRO: PLC ID %d tem endereço IP vazio", plcConfig.ID)
			continue
		}

		// Verifica se o PLC já está sendo monitorado
		cancelFunc, exists := m.plcCancels[id]

		// Verifica se é necessário reiniciar o PLC (mudança de configuração)
		needsRestart := false
		if exists {
			// Aqui deveríamos comparar com a configuração atual
			// Em um sistema mais robusto, deveríamos ter um cache das configurações
			// Por enquanto, assume que não precisa reiniciar
		}

		// Inicia ou reinicia o monitoramento do PLC
		if !exists || needsRestart {
			if exists && needsRestart {
				cancelFunc() // Cancela a goroutine anterior
				log.Printf("Reiniciando monitoramento do PLC %s (ID: %d) devido a mudança de configuração", plcConfig.Name, plcConfig.ID)
				m.logger.Info("Reiniciando PLC", fmt.Sprintf("PLC: %s (ID: %d)", plcConfig.Name, plcConfig.ID))
			}

			// Cria um novo contexto para este PLC
			plcCtx, plcCancel := context.WithCancel(m.ctx)
			m.plcCancels[id] = plcCancel

			// Inicia uma goroutine para monitorar este PLC
			m.wg.Add(1)
			go func(ctx context.Context, config database.PLC) {
				defer m.wg.Done()
				m.runPLC(ctx, config)
			}(plcCtx, plcConfig)

			if !exists {
				log.Printf("Iniciando monitoramento do PLC %s (ID: %d, IP: %s)", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress)
				m.logger.Info("PLC adicionado", fmt.Sprintf("PLC: %s (ID: %d, IP: %s)", plcConfig.Name, plcConfig.ID, plcConfig.IPAddress))
			}
		}
	}
	m.mutex.Unlock()
}

// isCriticalError verifica se o erro indica perda de conexão (crítico).
func isCriticalError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "conexão") ||
		strings.Contains(lower, "connection") ||
		strings.Contains(lower, "forçado") ||
		strings.Contains(lower, "cancelado") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "refused") ||
		strings.Contains(lower, "recusada") ||
		strings.Contains(lower, "reset")
}

// runPLC inicia e mantém a conexão com um PLC específico
func (m *Manager) runPLC(ctx context.Context, plcConfig database.PLC) {
	retryTicker := time.NewTicker(5 * time.Second)
	defer retryTicker.Stop()

	// Verifica o status do PLC apenas a cada X iterações
	checkCounter := 0

	for {
		select {
		case <-ctx.Done():
			log.Printf("Encerrando monitoramento do PLC %s", plcConfig.Name)
			return

		default:
			// Verifica o status do PLC a cada 12 iterações (aprox. 60 segundos com retryTicker de 5s)
			checkCounter++
			if checkCounter >= 12 {
				checkCounter = 0

				// Criando o contexto com timeout adequado
				checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)

				// Função para verificar o status do PLC
				func() {
					defer checkCancel() // Garante que o contexto seja cancelado

					// Primeiro verifica no cache
					plcJSON, cacheErr := m.cache.GetValue(fmt.Sprintf("config:plc:%d", plcConfig.ID))
					if cacheErr == nil && plcJSON != "" {
						var plc database.PLC
						if err := json.Unmarshal([]byte(plcJSON), &plc); err == nil {
							if !plc.Active {
								m.logger.Info("PLC não mais ativo", fmt.Sprintf("PLC ID %d", plcConfig.ID))
								log.Printf("PLC ID %d não está mais ativo (info do cache)", plcConfig.ID)
								return // Encerra a função anônima
							}
							// Atualiza a configuração do PLC com os dados mais recentes
							plcConfig = plc
						}
					} else {
						// Verifica se o contexto já foi cancelado antes de acessar o DB
						if checkCtx.Err() != nil {
							return
						}

						// Se falhou o cache, verifica no banco de dados
						plc, err := m.db.GetPLCByID(plcConfig.ID)

						if err != nil {
							m.logger.Error("Erro ao verificar status do PLC", fmt.Sprintf("PLC ID %d: %v", plcConfig.ID, err))
							log.Printf("Erro ao verificar status do PLC ID %d: %v", plcConfig.ID, err)
							// Continua com a configuração atual
						} else if plc != nil && !plc.Active {
							m.logger.Info("PLC não mais ativo", fmt.Sprintf("PLC ID %d", plcConfig.ID))
							log.Printf("PLC ID %d não está mais ativo no banco de dados", plcConfig.ID)
							return // Encerra a função anônima
						} else if plc != nil {
							// Atualiza a configuração do PLC com os dados mais recentes
							plcConfig = *plc
						}
					}
				}()
			}

			// Criando contexto para timeout ao tentar conectar-se
			// CORREÇÃO: Usando o _ para ignorar o contexto já que não o usamos diretamente
			_, connCancel := context.WithTimeout(ctx, 10*time.Second)
			defer connCancel() // Garantimos que será cancelado

			// Configura a conexão com o PLC usando o pool
			var client *plclib.Client
			var connectErr error

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

				log.Printf("Tentando conectar ao PLC %s (ID: %d) com VLAN...", plcConfig.Name, plcConfig.ID)

				// Usa o pool de conexões se disponível
				if m.plcPool != nil {
					client, connectErr = m.plcPool.GetConnectionWithConfig(config)
				} else {
					client, connectErr = plclib.NewClientWithConfig(config)
				}
			} else {
				// Configuração básica sem VLAN
				log.Printf("Tentando conectar ao PLC %s (ID: %d, IP: %s, Rack: %d, Slot: %d)...",
					plcConfig.Name, plcConfig.ID, plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)

				// Usa o pool de conexões se disponível
				if m.plcPool != nil {
					client, connectErr = m.plcPool.GetConnection(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
				} else {
					client, connectErr = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
				}
			}

			// Limpeza do contexto de conexão (já não precisa pelo defer)
			// connCancel()

			if connectErr != nil {
				log.Printf("Erro ao conectar ao PLC %s: %v", plcConfig.Name, connectErr)
				m.logger.Error("Erro ao conectar ao PLC", fmt.Sprintf("%s: %v", plcConfig.Name, connectErr))

				// Atualiza status para offline
				offlineStatus := database.PLCStatus{
					PLCID:      plcConfig.ID,
					Status:     "offline",
					LastUpdate: time.Now(),
				}

				// Atualiza o status no cache
				if err := m.cache.SetValue(fmt.Sprintf("config:plc:%d:status", plcConfig.ID), "offline"); err != nil {
					log.Printf("Erro ao atualizar status no cache para PLC ID %d: %v", plcConfig.ID, err)
				}

				// Atualiza o status no banco de dados
				if err := m.db.UpdatePLCStatus(offlineStatus); err != nil {
					log.Printf("Erro ao atualizar status offline para PLC ID %d: %v", plcConfig.ID, err)
					m.logger.Error("Erro ao atualizar status offline", fmt.Sprintf("PLC ID %d: %v", plcConfig.ID, err))
				} else {
					log.Printf("Status do PLC ID %d atualizado para offline devido a falha de conexão", plcConfig.ID)
				}

				// Aguarda antes de tentar novamente
				select {
				case <-retryTicker.C:
					log.Printf("Tentando reconectar ao PLC ID %d...", plcConfig.ID)
					continue
				case <-ctx.Done():
					return
				}
			}

			// Verifica se o cliente é válido antes de prosseguir
			if client == nil {
				log.Printf("Cliente PLC criado é nulo para PLC %s", plcConfig.Name)
				m.logger.Error("Cliente PLC criado é nulo", fmt.Sprintf("PLC: %s", plcConfig.Name))

				select {
				case <-retryTicker.C:
					continue
				case <-ctx.Done():
					return
				}
			}

			log.Printf("Conectado ao PLC: %s (%s)", plcConfig.Name, plcConfig.IPAddress)
			m.logger.Info("Conectado ao PLC", plcConfig.Name)

			// Atualiza o status no cache
			if err := m.cache.SetValue(fmt.Sprintf("config:plc:%d:status", plcConfig.ID), "online"); err != nil {
				log.Printf("Erro ao atualizar status no cache para PLC ID %d: %v", plcConfig.ID, err)
			}

			// Cria um contexto para esta conexão
			clientCtx, clientCancel := context.WithCancel(ctx)
			errChan := make(chan error, 2)

			// Usamos waitgroup para garantir que todas as goroutines sejam encerradas
			var clientWg sync.WaitGroup
			clientWg.Add(2)

			// Inicia goroutine para monitorar o status do PLC
			go func() {
				defer clientWg.Done()
				log.Printf("Iniciando monitoramento de status para PLC ID %d", plcConfig.ID)
				if err := m.updatePLCStatus(clientCtx, plcConfig.ID, client); err != nil {
					log.Printf("Erro no monitoramento de status do PLC ID %d: %v", plcConfig.ID, err)
					select {
					case errChan <- err:
					default:
						log.Printf("Canal de erros cheio, descartando erro: %v", err)
					}
				}
			}()

			// Inicia goroutine para gerenciar as tags do PLC
			go func() {
				defer clientWg.Done()
				log.Printf("Iniciando gerenciamento de tags para PLC ID %d", plcConfig.ID)
				if err := m.managePLCTags(clientCtx, plcConfig.ID, plcConfig.Name, client); err != nil {
					log.Printf("Erro no gerenciamento de tags do PLC ID %d: %v", plcConfig.ID, err)
					select {
					case errChan <- fmt.Errorf("erro no gerenciamento de tags: %v", err):
					default:
						log.Printf("Canal de erros cheio, descartando erro: %v", err)
					}
				}
			}()

			// Aguarda por erro ou cancelamento
			select {
			case err := <-errChan:
				clientCancel()
				log.Printf("Erro crítico no PLC %s: %v", plcConfig.Name, err)
				m.logger.Error("Erro crítico no PLC", fmt.Sprintf("%s: %v", plcConfig.Name, err))

				// Aguarda as goroutines terminarem com timeout
				waitDone := make(chan struct{})
				go func() {
					clientWg.Wait()
					close(waitDone)
				}()

				select {
				case <-waitDone:
					// Goroutines terminaram normalmente
				case <-time.After(2 * time.Second):
					log.Printf("Timeout aguardando goroutines do PLC %s terminarem", plcConfig.Name)
				}

				// Se estamos usando o pool, libera a conexão
				if m.plcPool != nil {
					if plcConfig.UseVLAN && plcConfig.Gateway != "" {
						config := plclib.ClientConfig{
							IPAddress: plcConfig.IPAddress,
							Rack:      plcConfig.Rack,
							Slot:      plcConfig.Slot,
						}
						m.plcPool.ReleaseWithConfig(config)
					} else {
						m.plcPool.Release(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
					}
				} else {
					// Se não estamos usando pool, fechamos a conexão
					client.Close()
				}

			case <-clientCtx.Done():
				clientCancel()
				// Aguarda goroutines terminarem
				clientWg.Wait()

				// Se estamos usando o pool, libera a conexão
				if m.plcPool != nil {
					if plcConfig.UseVLAN && plcConfig.Gateway != "" {
						config := plclib.ClientConfig{
							IPAddress: plcConfig.IPAddress,
							Rack:      plcConfig.Rack,
							Slot:      plcConfig.Slot,
						}
						m.plcPool.ReleaseWithConfig(config)
					} else {
						m.plcPool.Release(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
					}
				} else {
					// Se não estamos usando pool, fechamos a conexão
					client.Close()
				}
				return

			case <-ctx.Done():
				// Caso o contexto pai seja cancelado
				clientCancel()
				// Aguarda goroutines terminarem
				clientWg.Wait()

				// Se estamos usando o pool, libera a conexão
				if m.plcPool != nil {
					if plcConfig.UseVLAN && plcConfig.Gateway != "" {
						config := plclib.ClientConfig{
							IPAddress: plcConfig.IPAddress,
							Rack:      plcConfig.Rack,
							Slot:      plcConfig.Slot,
						}
						m.plcPool.ReleaseWithConfig(config)
					} else {
						m.plcPool.Release(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
					}
				} else {
					// Se não estamos usando pool, fechamos a conexão
					client.Close()
				}
				return
			}

			// Aguarda antes de tentar reconectar
			select {
			case <-retryTicker.C:
				log.Printf("Tentando reconectar ao PLC ID %d após erro...", plcConfig.ID)
				// Continua e tenta reconectar
			case <-ctx.Done():
				return
			}
		}
	}
}

// GetPLCStatusCount retorna um resumo com contagem de PLCs por status
func (m *Manager) GetPLCStatusCount() map[string]int {
	status := make(map[string]int)

	// Tenta obter do cache
	plcsJSON, err := m.cache.GetValue("config:plcs:all")
	if err != nil || plcsJSON == "" {
		// Fallback para banco
		plcs, dbErr := m.db.GetActivePLCs()
		if dbErr != nil {
			log.Printf("Erro ao obter PLCs para contagem de status: %v", dbErr)
			return status
		}

		for _, plc := range plcs {
			status[plc.Status]++
		}
		return status
	}

	// Do cache
	var plcs []database.PLC
	if err := json.Unmarshal([]byte(plcsJSON), &plcs); err != nil {
		log.Printf("Erro ao deserializar PLCs para contagem de status: %v", err)
		return status
	}

	for _, plc := range plcs {
		status[plc.Status]++
	}

	return status
}
