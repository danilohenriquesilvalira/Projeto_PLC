package configsync

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"Projeto_PLC/internal/cache"
	"Projeto_PLC/internal/database"
)

// DetailedLogging controla se logs detalhados devem ser exibidos
var DetailedLogging = false

const (
	// Chaves para controle de sincronização
	KeyConfigPLCsHash    = "config:sync:plcs:hash"
	KeyConfigPLCsTime    = "config:sync:plcs:time"
	KeyConfigTagsHash    = "config:sync:plc:%d:tags:hash"
	KeyConfigTagsTime    = "config:sync:plc:%d:tags:time"
	KeyConfigHealthCheck = "config:sync:health"
)

// ConfigSync mantém o Redis atualizado com dados do PostgreSQL
type ConfigSync struct {
	db                 *database.DB
	cache              cache.Cache
	interval           time.Duration
	ctx                context.Context
	cancel             context.CancelFunc
	logger             *database.Logger
	syncMutex          sync.Mutex // Protege contra execuções simultâneas
	lastSyncAttempt    time.Time  // Último momento em que tentamos sincronizar
	lastSuccessfulSync time.Time  // Último momento em que a sincronização foi bem-sucedida
	syncStats          SyncStats  // Estatísticas de sincronização
	consecutiveErrors  int        // Contador de erros consecutivos para recuo exponencial
}

// SyncStats mantém estatísticas sobre sincronizações
type SyncStats struct {
	TotalSyncs       int           // Número total de sincronizações
	SuccessfulSyncs  int           // Número de sincronizações bem-sucedidas
	FailedSyncs      int           // Número de sincronizações com falha
	TotalPLCsUpdated int           // Número total de PLCs atualizados
	TotalTagsUpdated int           // Número total de tags atualizadas
	AverageDuration  time.Duration // Duração média de sincronização
	LastDuration     time.Duration // Duração da última sincronização
	LastError        string        // Último erro de sincronização
}

// NewConfigSync cria um novo sincronizador de configuração
func NewConfigSync(ctx context.Context, db *database.DB, cache cache.Cache, interval time.Duration, logger *database.Logger) *ConfigSync {
	syncCtx, cancel := context.WithCancel(ctx)
	return &ConfigSync{
		db:                 db,
		cache:              cache,
		interval:           interval,
		ctx:                syncCtx,
		cancel:             cancel,
		logger:             logger,
		syncMutex:          sync.Mutex{},
		lastSyncAttempt:    time.Time{},
		lastSuccessfulSync: time.Time{},
		syncStats:          SyncStats{},
		consecutiveErrors:  0,
	}
}

// Start inicia a sincronização periódica
func (s *ConfigSync) Start() error {
	// Registra início apenas uma vez
	s.logger.InfoWithDetails("ConfigSync",
		"Iniciando sistema de sincronização de configurações",
		fmt.Sprintf("Intervalo configurado: %v", s.interval))

	// Verificar a saúde do cache imediatamente
	if err := s.healthCheck(); err != nil {
		s.logger.ErrorWithDetails("ConfigSync",
			"Falha na verificação inicial do cache",
			fmt.Sprintf("Erro: %v", err))
		return err
	}

	// Força a sincronização inicial completa
	if err := s.SyncAll(true); err != nil {
		s.logger.ErrorWithDetails("ConfigSync",
			"Erro na sincronização inicial",
			fmt.Sprintf("Erro: %v", err))
		return fmt.Errorf("erro na sincronização inicial: %w", err)
	}

	// Sincronização periódica
	go s.runSyncLoop()

	return nil
}

// healthCheck verifica e registra a saúde do cache
func (s *ConfigSync) healthCheck() error {
	testKey := KeyConfigHealthCheck
	testValue := fmt.Sprintf("health:%d", time.Now().UnixNano())

	// Teste de escrita
	if err := s.cache.SetValue(testKey, testValue); err != nil {
		s.logger.ErrorWithDetails("ConfigSync",
			"Cache não está disponível para escrita",
			fmt.Sprintf("Erro: %v", err))
		return fmt.Errorf("cache não está disponível para escrita: %w", err)
	}

	// Teste de leitura
	readValue, err := s.cache.GetValue(testKey)
	if err != nil {
		s.logger.ErrorWithDetails("ConfigSync",
			"Cache não está disponível para leitura",
			fmt.Sprintf("Erro: %v", err))
		return fmt.Errorf("cache não está disponível para leitura: %w", err)
	}

	if readValue != testValue {
		errMsg := fmt.Sprintf("Esperado: '%s', Recebido: '%s'", testValue, readValue)
		s.logger.ErrorWithDetails("ConfigSync",
			"Inconsistência no cache: valores escritos e lidos não correspondem",
			errMsg)
		return fmt.Errorf("inconsistência no cache: valores escritos e lidos não correspondem")
	}

	// Só logar em detalhes a cada 1 hora para reduzir volume de logs
	if time.Since(s.lastSuccessfulSync) > time.Hour {
		s.logger.Debug("ConfigSync", "Verificação de saúde do cache: OK")
	}
	return nil
}

// Stop para a sincronização
func (s *ConfigSync) Stop() {
	s.logger.Info("ConfigSync", "Parando sincronizador de configuração")
	s.cancel()
	// Aguarda um curto período para garantir que a goroutine termine
	time.Sleep(100 * time.Millisecond)
}

// runSyncLoop executa a sincronização periódica
func (s *ConfigSync) runSyncLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	healthTicker := time.NewTicker(30 * time.Second)
	defer healthTicker.Stop()

	// Log detalhado do início do loop de sincronização
	if DetailedLogging {
		log.Printf("Sincronizador configurado para executar a cada %v", s.interval)
	}

	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("ConfigSync", "Sincronizador de configuração finalizado")
			return

		case <-healthTicker.C:
			// Verificação periódica da saúde do cache
			if err := s.healthCheck(); err != nil {
				// Incrementa o contador de erros consecutivos
				s.consecutiveErrors++

				// Registra o erro apenas após vários erros consecutivos para evitar spam
				if s.consecutiveErrors >= 3 {
					s.logger.ErrorWithDetails("ConfigSync",
						"Falhas persistentes na verificação de saúde do cache",
						fmt.Sprintf("Falhas consecutivas: %d, Último erro: %v",
							s.consecutiveErrors, err))
				}
			} else {
				// Resetar contador de erros se o health check for bem-sucedido
				s.consecutiveErrors = 0
			}

		case <-ticker.C:
			// Evitar execuções sobrepostas com mutex
			if !s.syncMutex.TryLock() {
				if DetailedLogging {
					log.Println("Sincronização já em andamento, pulando esta iteração")
				}
				continue
			}

			// Registra a tentativa de sincronização
			s.lastSyncAttempt = time.Now()

			// Registra o início apenas em modo detalhado para evitar spam
			if DetailedLogging {
				log.Println("Iniciando ciclo de sincronização inteligente")
			}

			// Aplicar lógica de recuo exponencial para erros persistentes
			if s.consecutiveErrors > 5 {
				// Pula algumas sincronizações após muitos erros para não sobrecarregar
				if s.consecutiveErrors < 10 && s.consecutiveErrors%2 != 0 {
					s.syncMutex.Unlock()
					continue
				} else if s.consecutiveErrors >= 10 && s.consecutiveErrors < 20 && s.consecutiveErrors%4 != 0 {
					s.syncMutex.Unlock()
					continue
				} else if s.consecutiveErrors >= 20 && s.consecutiveErrors%8 != 0 {
					s.syncMutex.Unlock()
					continue
				}
			}

			startTime := time.Now()

			// Sincronização normal (não forçada)
			if err := s.SyncAll(false); err != nil {
				// Incrementar contador de falhas
				s.consecutiveErrors++
				s.syncStats.FailedSyncs++
				s.syncStats.LastError = err.Error()

				// Registar o erro apenas em falhas significativas para reduzir spam
				if s.consecutiveErrors >= 3 || DetailedLogging {
					s.logger.ErrorWithDetails("ConfigSync",
						"Falha na sincronização periódica",
						fmt.Sprintf("Tentativa: %d, Erro: %v", s.syncStats.TotalSyncs, err))
				}
			} else {
				// Sucesso - resetar contador de erros e atualizar estatísticas
				s.syncStats.SuccessfulSyncs++
				s.syncStats.LastDuration = time.Since(startTime)
				s.syncStats.AverageDuration = (s.syncStats.AverageDuration*time.Duration(s.syncStats.SuccessfulSyncs-1) +
					s.syncStats.LastDuration) / time.Duration(s.syncStats.SuccessfulSyncs)
				s.lastSuccessfulSync = time.Now()
				s.consecutiveErrors = 0

				// Registrar sucesso apenas ocasionalmente para reduzir volume de logs
				if s.syncStats.SuccessfulSyncs%10 == 0 || DetailedLogging {
					s.logger.InfoWithDetails("ConfigSync",
						"Sincronização periódica bem-sucedida",
						fmt.Sprintf("Sincronização #%d, Duração: %v, PLCs atualizados: %d, Tags atualizadas: %d",
							s.syncStats.SuccessfulSyncs, s.syncStats.LastDuration,
							s.syncStats.TotalPLCsUpdated, s.syncStats.TotalTagsUpdated))
				}
			}

			// Incrementar contador geral de sincronizações
			s.syncStats.TotalSyncs++

			s.syncMutex.Unlock()
		}
	}
}

// SyncAll sincroniza todos os dados de configuração
func (s *ConfigSync) SyncAll(forceSync bool) error {
	// Verifica a saúde do cache antes de prosseguir
	if err := s.healthCheck(); err != nil {
		return fmt.Errorf("falha na verificação de saúde antes da sincronização: %w", err)
	}

	// Usa contexto com timeout específico
	ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
	defer cancel()

	// Variáveis para rastreamento de atualizações
	var plcsUpdated, tagsUpdated int
	startTime := time.Now()

	// Sincroniza PLCs com verificação de alterações
	plcsCount, err := s.SyncPLCs(ctx, forceSync)
	if err != nil {
		return fmt.Errorf("erro ao sincronizar PLCs: %w", err)
	}
	plcsUpdated = plcsCount

	// Sincroniza Tags com verificação de alterações
	tagsCount, err := s.SyncTags(ctx, forceSync)
	if err != nil {
		return fmt.Errorf("erro ao sincronizar Tags: %w", err)
	}
	tagsUpdated = tagsCount

	// Atualiza estatísticas
	s.syncStats.TotalPLCsUpdated += plcsUpdated
	s.syncStats.TotalTagsUpdated += tagsUpdated

	// Registra conclusão apenas se houve mudanças ou em modo detalhado
	if plcsUpdated > 0 || tagsUpdated > 0 || DetailedLogging {
		s.logger.InfoWithDetails("ConfigSync",
			"Sincronização concluída com atualizações",
			fmt.Sprintf("PLCs atualizados: %d, Tags atualizadas: %d, Tempo: %v",
				plcsUpdated, tagsUpdated, time.Since(startTime)))
	}

	return nil
}

// calculateHash calcula um hash MD5 dos dados serializados
func calculateHash(data interface{}) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	hash := md5.Sum(jsonData)
	return hex.EncodeToString(hash[:]), nil
}

// SyncPLCs sincroniza PLCs do PostgreSQL para o Redis
func (s *ConfigSync) SyncPLCs(ctx context.Context, forceSync bool) (int, error) {
	if DetailedLogging {
		log.Println("Sincronizando PLCs...")
	}

	// Primeiro obtém os dados do banco
	dbWithTimeout := s.db.WithTimeout(20 * time.Second)
	plcs, err := dbWithTimeout.GetActivePLCs()
	if err != nil {
		return 0, fmt.Errorf("erro ao obter PLCs ativos: %w", err)
	}

	// Verifica se há PLCs para sincronizar
	if len(plcs) == 0 {
		s.logger.Debug("ConfigSync", "Nenhum PLC ativo encontrado no banco de dados")
		return 0, nil
	}

	// Calcula o hash dos dados dos PLCs para detectar mudanças
	currentHash, err := calculateHash(plcs)
	if err != nil {
		return 0, fmt.Errorf("erro ao calcular hash dos PLCs: %w", err)
	}

	// Verifica se houve mudanças comparando com o hash armazenado
	storedHash, _ := s.cache.GetValue(KeyConfigPLCsHash)
	dataChanged := forceSync || (storedHash != currentHash)

	if !dataChanged {
		// Nenhuma mudança detectada
		if DetailedLogging {
			log.Println("Dados de PLCs não mudaram desde a última sincronização")
		}
		return 0, nil
	}

	if DetailedLogging {
		log.Printf("Detectadas alterações nos dados de PLCs (forceSync: %v, hashDiff: %v)",
			forceSync, storedHash != currentHash)
	}

	// Prossegue com a sincronização já que os dados mudaram
	allPlcsKey := "config:plcs:all"
	plcsJSON, err := json.Marshal(plcs)
	if err != nil {
		return 0, fmt.Errorf("erro ao serializar PLCs: %w", err)
	}

	// Armazena em uma transação para garantir atomicidade
	if err := s.safeStoreValue(allPlcsKey, string(plcsJSON)); err != nil {
		return 0, fmt.Errorf("erro ao armazenar lista de PLCs: %w", err)
	}

	if DetailedLogging {
		log.Printf("Dados de %d PLCs sincronizados no cache com chave '%s'", len(plcs), allPlcsKey)
	}

	// Armazena cada PLC individualmente e rastreia PLCs processados
	processedIDs := make(map[int]bool)
	for _, plc := range plcs {
		plcJSON, err := json.Marshal(plc)
		if err != nil {
			if DetailedLogging {
				log.Printf("Erro ao serializar PLC %d: %v", plc.ID, err)
			}
			continue
		}

		// Chave para cada PLC individual
		plcKey := fmt.Sprintf("config:plc:%d", plc.ID)
		if err := s.safeStoreValue(plcKey, string(plcJSON)); err != nil {
			if DetailedLogging {
				log.Printf("Erro ao armazenar PLC %d: %v", plc.ID, err)
			}
			continue
		}

		if DetailedLogging {
			log.Printf("PLC ID %d (%s) sincronizado com sucesso", plc.ID, plc.Name)
		}

		processedIDs[plc.ID] = true

		// Registra o status do PLC se disponível
		if plc.Status != "" {
			statusKey := fmt.Sprintf("config:plc:%d:status", plc.ID)
			if err := s.cache.SetValue(statusKey, plc.Status); err == nil {
				if DetailedLogging {
					log.Printf("Status do PLC ID %d (%s) sincronizado: %s", plc.ID, plc.Name, plc.Status)
				}
			}
		}
	}

	// Armazena o hash atual para comparação futura
	if err := s.cache.SetValue(KeyConfigPLCsHash, currentHash); err != nil {
		if DetailedLogging {
			log.Printf("Aviso: Não foi possível armazenar hash de PLCs: %v", err)
		}
	}

	// Armazena o timestamp da última sincronização
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	if err := s.cache.SetValue(KeyConfigPLCsTime, timestamp); err != nil {
		if DetailedLogging {
			log.Printf("Aviso: Não foi possível armazenar timestamp de PLCs: %v", err)
		}
	}

	// Registra a conclusão da sincronização
	s.logger.InfoWithDetails("ConfigSync",
		"Dados de PLCs sincronizados com sucesso",
		fmt.Sprintf("PLCs atualizados: %d", len(plcs)))

	return len(plcs), nil
}

// safeStoreValue armazena um valor no cache de forma segura, com verificação
func (s *ConfigSync) safeStoreValue(key string, value string) error {
	// Usa uma chave temporária primeiro para verificar se o armazenamento funciona
	tempKey := fmt.Sprintf("%s:tmp:%d", key, time.Now().UnixNano())

	// Tenta armazenar na chave temporária
	if err := s.cache.SetValue(tempKey, value); err != nil {
		return fmt.Errorf("erro ao armazenar em chave temporária: %w", err)
	}

	// Verifica se foi armazenado corretamente
	readValue, err := s.cache.GetValue(tempKey)
	if err != nil {
		_ = s.cache.DeleteKey(tempKey) // tenta limpar
		return fmt.Errorf("erro ao ler chave temporária: %w", err)
	}

	// Verifica se o valor lido corresponde ao valor escrito
	if readValue != value {
		_ = s.cache.DeleteKey(tempKey) // tenta limpar
		return fmt.Errorf("valor lido não corresponde ao valor escrito")
	}

	// Agora é seguro escrever na chave real
	if err := s.cache.SetValue(key, value); err != nil {
		_ = s.cache.DeleteKey(tempKey) // tenta limpar
		return fmt.Errorf("erro ao armazenar na chave real: %w", err)
	}

	// Limpeza final
	_ = s.cache.DeleteKey(tempKey)
	return nil
}

// SyncTags sincroniza as tags do PostgreSQL para o Redis
func (s *ConfigSync) SyncTags(ctx context.Context, forceSync bool) (int, error) {
	if DetailedLogging {
		log.Println("Sincronizando Tags...")
	}

	// Obtém todos os PLCs
	dbWithTimeout := s.db.WithTimeout(30 * time.Second)
	plcs, err := dbWithTimeout.GetActivePLCs()
	if err != nil {
		return 0, fmt.Errorf("erro ao obter PLCs ativos para sincronização de tags: %w", err)
	}

	// Se não há PLCs, não há tags para sincronizar
	if len(plcs) == 0 {
		if DetailedLogging {
			log.Println("Nenhum PLC ativo encontrado para sincronizar tags")
		}
		return 0, nil
	}

	totalTags := 0
	syncsPerformed := 0

	// Para cada PLC, sincroniza suas tags
	for _, plc := range plcs {
		// Verifica se devemos sincronizar este PLC específico
		shouldSync, err := s.shouldSyncTagsForPLC(plc.ID, forceSync)
		if err != nil {
			if DetailedLogging {
				log.Printf("Erro ao verificar necessidade de sincronização para PLC %d: %v", plc.ID, err)
			}
			// Em caso de erro, continuamos com a sincronização por segurança
			shouldSync = true
		}

		if !shouldSync {
			if DetailedLogging {
				log.Printf("Tags do PLC %s (ID: %d) estão atualizadas, pulando sincronização", plc.Name, plc.ID)
			}
			continue
		}

		// Início da sincronização para este PLC
		if DetailedLogging {
			log.Printf("====== Sincronizando tags do PLC %s (ID: %d)... ======", plc.Name, plc.ID)
		}

		// Obtém as tags com um timeout específico
		dbForTags := s.db.WithTimeout(30 * time.Second)
		tags, err := dbForTags.GetPLCTags(plc.ID)
		if err != nil {
			s.logger.ErrorWithDetails("ConfigSync",
				fmt.Sprintf("Erro ao obter tags do PLC %s", plc.Name),
				fmt.Sprintf("PLC ID: %d, Erro: %v", plc.ID, err))
			continue
		}

		if DetailedLogging {
			log.Printf("Obtidas %d tags do banco de dados para o PLC %s", len(tags), plc.Name)
		}

		// Filtra apenas tags ativas
		var activeTags []database.Tag
		for _, tag := range tags {
			if tag.Active {
				activeTags = append(activeTags, tag)
			}
		}

		if DetailedLogging {
			log.Printf("Filtradas %d tags ativas para o PLC %s", len(activeTags), plc.Name)
		}

		// Calcula o hash das tags para detectar mudanças
		currentHash, err := calculateHash(activeTags)
		if err != nil {
			if DetailedLogging {
				log.Printf("Erro ao calcular hash das tags do PLC %d: %v", plc.ID, err)
			}
			// Em caso de erro, prosseguimos com a sincronização
		} else {
			// Armazena o hash para verificações futuras
			hashKey := fmt.Sprintf(KeyConfigTagsHash, plc.ID)
			if err := s.cache.SetValue(hashKey, currentHash); err != nil {
				if DetailedLogging {
					log.Printf("Aviso: Não foi possível armazenar hash de tags do PLC %d: %v", plc.ID, err)
				}
			}
		}

		// Chave para todas as tags de um PLC
		tagsKey := fmt.Sprintf("config:plc:%d:tags", plc.ID)

		// Mesmo se não houver tags ativas, armazenamos um array vazio
		if len(activeTags) == 0 {
			if err := s.safeStoreValue(tagsKey, "[]"); err != nil {
				if DetailedLogging {
					log.Printf("Erro ao armazenar array vazio para PLC %d: %v", plc.ID, err)
				}
				continue
			}
			if DetailedLogging {
				log.Printf("Array vazio de tags armazenado para PLC %d", plc.ID)
			}
			continue
		}

		// Serializa as tags ativas
		tagsJSON, err := json.Marshal(activeTags)
		if err != nil {
			if DetailedLogging {
				log.Printf("Erro ao serializar tags do PLC %d: %v", plc.ID, err)
			}
			continue
		}

		// Armazena a lista completa de tags com método seguro
		if err := s.safeStoreValue(tagsKey, string(tagsJSON)); err != nil {
			if DetailedLogging {
				log.Printf("Erro fatal ao armazenar tags do PLC %d: %v", plc.ID, err)
			}
			continue
		}

		if DetailedLogging {
			log.Printf("Lista completa de %d tags sincronizada para PLC %d", len(activeTags), plc.ID)
		}
		syncsPerformed++

		// Armazena cada tag individualmente para acesso rápido
		for _, tag := range activeTags {
			tagJSON, err := json.Marshal(tag)
			if err != nil {
				if DetailedLogging {
					log.Printf("Erro ao serializar tag %d: %v", tag.ID, err)
				}
				continue
			}

			// Chave para tag específica
			tagKey := fmt.Sprintf("config:plc:%d:tag:%d", plc.ID, tag.ID)
			if err := s.cache.SetValue(tagKey, string(tagJSON)); err != nil {
				if DetailedLogging {
					log.Printf("Erro ao armazenar tag %d: %v", tag.ID, err)
				}
				continue
			}

			// Chave para busca por nome
			nameKey := fmt.Sprintf("config:plc:%d:tagname:%s", plc.ID, tag.Name)
			if err := s.cache.SetValue(nameKey, fmt.Sprintf("%d", tag.ID)); err != nil {
				if DetailedLogging {
					log.Printf("Erro ao armazenar mapeamento de nome da tag %s: %v", tag.Name, err)
				}
				continue
			}
		}

		// Registra o tempo da última sincronização bem-sucedida
		timeKey := fmt.Sprintf(KeyConfigTagsTime, plc.ID)
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		if err := s.cache.SetValue(timeKey, timestamp); err != nil {
			if DetailedLogging {
				log.Printf("Aviso: Não foi possível armazenar timestamp de tags do PLC %d: %v", plc.ID, err)
			}
		}

		if DetailedLogging {
			log.Printf("Sincronizadas %d tags para o PLC %s (ID: %d)", len(activeTags), plc.Name, plc.ID)
		}
		totalTags += len(activeTags)
	}

	// Log final resumido
	if syncsPerformed == 0 {
		if DetailedLogging {
			log.Println("Não foi necessário sincronizar tags para nenhum PLC - todos estão atualizados")
		}
		return 0, nil
	} else {
		// Registra a sincronização apenas se houve atualizações
		s.logger.InfoWithDetails("ConfigSync",
			"Tags sincronizadas com sucesso",
			fmt.Sprintf("Total: %d tags em %d PLCs", totalTags, syncsPerformed))

		return totalTags, nil
	}
}

// shouldSyncTagsForPLC verifica se as tags de um PLC precisam ser sincronizadas
// baseado no hash armazenado ou tempo da última sincronização
func (s *ConfigSync) shouldSyncTagsForPLC(plcID int, forceSync bool) (bool, error) {
	if forceSync {
		return true, nil
	}

	// Verifica se existe um timestamp de sincronização recente
	timeKey := fmt.Sprintf(KeyConfigTagsTime, plcID)
	timestampStr, err := s.cache.GetValue(timeKey)
	if err != nil || timestampStr == "" {
		// Se não há timestamp ou erro na leitura, sincronize
		return true, nil
	}

	// Converte o timestamp para comparação
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return true, err
	}

	// Se a última sincronização foi muito recente (menos de 5 segundos), pule
	// isso evita sincronizações frequentes demais se o intervalo for muito pequeno
	if time.Now().Unix()-timestamp < 5 {
		return false, nil
	}

	// Obtém as tags atuais do banco para calcular o hash
	dbForTags := s.db.WithTimeout(20 * time.Second)
	tags, err := dbForTags.GetPLCTags(plcID)
	if err != nil {
		return true, err
	}

	// Filtra apenas tags ativas
	var activeTags []database.Tag
	for _, tag := range tags {
		if tag.Active {
			activeTags = append(activeTags, tag)
		}
	}

	// Calcula o hash atual
	currentHash, err := calculateHash(activeTags)
	if err != nil {
		return true, err
	}

	// Compara com o hash armazenado
	hashKey := fmt.Sprintf(KeyConfigTagsHash, plcID)
	storedHash, err := s.cache.GetValue(hashKey)
	if err != nil || storedHash == "" {
		// Se não conseguir ler o hash, sincronize para segurança
		return true, nil
	}

	// Se os hashes forem diferentes, precisa sincronizar
	return storedHash != currentHash, nil
}

// GetSyncStatus retorna o status da sincronização
func (s *ConfigSync) GetSyncStatus() map[string]interface{} {
	status := make(map[string]interface{})

	// Verifica se o cache está saudável
	cacheHealth := "OK"
	if err := s.healthCheck(); err != nil {
		cacheHealth = fmt.Sprintf("Falha: %v", err)
	}
	status["cache_health"] = cacheHealth

	// Status atualização
	status["last_sync_attempt"] = s.lastSyncAttempt.Format(time.RFC3339)
	if !s.lastSuccessfulSync.IsZero() {
		status["last_successful_sync"] = s.lastSuccessfulSync.Format(time.RFC3339)
		status["seconds_since_last_sync"] = time.Since(s.lastSuccessfulSync).Seconds()
	}

	// Estatísticas
	status["total_syncs"] = s.syncStats.TotalSyncs
	status["successful_syncs"] = s.syncStats.SuccessfulSyncs
	status["failed_syncs"] = s.syncStats.FailedSyncs
	status["average_duration_ms"] = s.syncStats.AverageDuration.Milliseconds()
	status["last_duration_ms"] = s.syncStats.LastDuration.Milliseconds()
	status["consecutive_errors"] = s.consecutiveErrors

	if s.syncStats.LastError != "" {
		status["last_error"] = s.syncStats.LastError
	}

	// Obtém timestamp da última sincronização de PLCs
	plcTimeStr, _ := s.cache.GetValue(KeyConfigPLCsTime)
	if plcTimeStr != "" {
		plcTime, err := strconv.ParseInt(plcTimeStr, 10, 64)
		if err == nil {
			status["plcs_last_sync"] = time.Unix(plcTime, 0).Format(time.RFC3339)
			status["plcs_seconds_ago"] = time.Now().Unix() - plcTime
		}
	}

	// Obtém lista de PLCs para verificar sincronização de tags
	tagsStatus := make(map[string]interface{})
	plcs, _ := s.db.GetActivePLCs()
	for _, plc := range plcs {
		timeKey := fmt.Sprintf(KeyConfigTagsTime, plc.ID)
		timestampStr, _ := s.cache.GetValue(timeKey)
		if timestampStr != "" {
			timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
			if err == nil {
				tagsStatus[plc.Name] = map[string]interface{}{
					"last_sync":   time.Unix(timestamp, 0).Format(time.RFC3339),
					"seconds_ago": time.Now().Unix() - timestamp,
				}
			}
		}
	}
	status["tags_sync"] = tagsStatus

	return status
}

// SetDetailedLogging permite ativar ou desativar logs detalhados
func SetDetailedLogging(enabled bool) {
	DetailedLogging = enabled
	if enabled {
		log.Println("ConfigSync: Logs detalhados ATIVADOS")
	} else {
		log.Println("ConfigSync: Logs detalhados DESATIVADOS")
	}
}
