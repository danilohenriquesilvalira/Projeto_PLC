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
	db        *database.DB
	cache     cache.Cache
	interval  time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	logger    *database.Logger
	syncMutex sync.Mutex // Protege contra execuções simultâneas
}

// NewConfigSync cria um novo sincronizador de configuração
func NewConfigSync(ctx context.Context, db *database.DB, cache cache.Cache, interval time.Duration, logger *database.Logger) *ConfigSync {
	syncCtx, cancel := context.WithCancel(ctx)
	return &ConfigSync{
		db:        db,
		cache:     cache,
		interval:  interval,
		ctx:       syncCtx,
		cancel:    cancel,
		logger:    logger,
		syncMutex: sync.Mutex{},
	}
}

// Start inicia a sincronização periódica
func (s *ConfigSync) Start() error {
	log.Println("Iniciando sincronizador de configuração")
	s.logger.Info("ConfigSync", "Iniciando sincronizador de configuração")

	// Verificar a saúde do cache imediatamente
	if err := s.healthCheck(); err != nil {
		return err
	}

	// Força a sincronização inicial completa
	if err := s.SyncAll(true); err != nil {
		s.logger.Error("ConfigSync", fmt.Sprintf("Erro na sincronização inicial: %v", err))
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
		log.Printf("ERRO: Cache não está disponível para escrita: %v", err)
		s.logger.Error("ConfigSync", fmt.Sprintf("Cache não está disponível para escrita: %v", err))
		return fmt.Errorf("cache não está disponível para escrita: %w", err)
	}

	// Teste de leitura
	readValue, err := s.cache.GetValue(testKey)
	if err != nil {
		log.Printf("ERRO: Cache não está disponível para leitura: %v", err)
		s.logger.Error("ConfigSync", fmt.Sprintf("Cache não está disponível para leitura: %v", err))
		return fmt.Errorf("cache não está disponível para leitura: %w", err)
	}

	if readValue != testValue {
		log.Printf("ERRO: Valor lido do cache não corresponde ao valor escrito. Esperado '%s', recebido '%s'",
			testValue, readValue)
		s.logger.Error("ConfigSync", "Inconsistência no cache: valores escritos e lidos não correspondem")
		return fmt.Errorf("inconsistência no cache: valores escritos e lidos não correspondem")
	}

	log.Println("Verificação de saúde do cache: OK")
	return nil
}

// Stop para a sincronização
func (s *ConfigSync) Stop() {
	log.Println("Parando sincronizador de configuração")
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

	log.Printf("Sincronizador configurado para executar a cada %v", s.interval)

	for {
		select {
		case <-s.ctx.Done():
			log.Println("Sincronizador de configuração finalizado")
			return

		case <-healthTicker.C:
			// Verificação periódica da saúde do cache
			if err := s.healthCheck(); err != nil {
				log.Printf("Falha na verificação de saúde do cache: %v", err)
				s.logger.Error("ConfigSync", fmt.Sprintf("Falha na verificação de saúde do cache: %v", err))
			}

		case <-ticker.C:
			// Evitar execuções sobrepostas com mutex
			if !s.syncMutex.TryLock() {
				log.Println("Sincronização já em andamento, pulando esta iteração")
				continue
			}

			log.Println("Iniciando ciclo de sincronização inteligente")
			// Sincronização normal (não forçada)
			if err := s.SyncAll(false); err != nil {
				log.Printf("Erro na sincronização: %v", err)
				s.logger.Error("ConfigSync", fmt.Sprintf("Erro na sincronização: %v", err))
			}

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

	// Sincroniza PLCs com verificação de alterações
	if err := s.SyncPLCs(ctx, forceSync); err != nil {
		return fmt.Errorf("erro ao sincronizar PLCs: %w", err)
	}

	// Sincroniza Tags com verificação de alterações
	if err := s.SyncTags(ctx, forceSync); err != nil {
		return fmt.Errorf("erro ao sincronizar Tags: %w", err)
	}

	log.Println("Sincronização de configuração concluída com sucesso")
	s.logger.Info("ConfigSync", "Sincronização concluída com sucesso")
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
func (s *ConfigSync) SyncPLCs(ctx context.Context, forceSync bool) error {
	log.Println("Sincronizando PLCs...")

	// Primeiro obtém os dados do banco
	dbWithTimeout := s.db.WithTimeout(20 * time.Second)
	plcs, err := dbWithTimeout.GetActivePLCs()
	if err != nil {
		return fmt.Errorf("erro ao obter PLCs ativos: %w", err)
	}

	// Verifica se há PLCs para sincronizar
	if len(plcs) == 0 {
		log.Println("Nenhum PLC ativo encontrado no banco de dados")
		s.logger.Warn("ConfigSync", "Nenhum PLC ativo encontrado no banco de dados")
		return nil
	}

	// Calcula o hash dos dados dos PLCs para detectar mudanças
	currentHash, err := calculateHash(plcs)
	if err != nil {
		return fmt.Errorf("erro ao calcular hash dos PLCs: %w", err)
	}

	// Verifica se houve mudanças comparando com o hash armazenado
	storedHash, _ := s.cache.GetValue(KeyConfigPLCsHash)
	dataChanged := forceSync || (storedHash != currentHash)

	if !dataChanged {
		log.Println("Dados de PLCs não mudaram desde a última sincronização")
		return nil
	}

	log.Printf("Detectadas alterações nos dados de PLCs (forceSync: %v, hashDiff: %v)",
		forceSync, storedHash != currentHash)

	// Prossegue com a sincronização já que os dados mudaram
	allPlcsKey := "config:plcs:all"
	plcsJSON, err := json.Marshal(plcs)
	if err != nil {
		return fmt.Errorf("erro ao serializar PLCs: %w", err)
	}

	// Armazena em uma transação para garantir atomicidade
	if err := s.safeStoreValue(allPlcsKey, string(plcsJSON)); err != nil {
		return fmt.Errorf("erro ao armazenar lista de PLCs: %w", err)
	}

	log.Printf("Dados de %d PLCs sincronizados no cache com chave '%s'", len(plcs), allPlcsKey)

	// Armazena cada PLC individualmente e rastreia PLCs processados
	processedIDs := make(map[int]bool)
	for _, plc := range plcs {
		plcJSON, err := json.Marshal(plc)
		if err != nil {
			log.Printf("Erro ao serializar PLC %d: %v", plc.ID, err)
			continue
		}

		// Chave para cada PLC individual
		plcKey := fmt.Sprintf("config:plc:%d", plc.ID)
		if err := s.safeStoreValue(plcKey, string(plcJSON)); err != nil {
			log.Printf("Erro ao armazenar PLC %d: %v", plc.ID, err)
			continue
		}

		log.Printf("PLC ID %d (%s) sincronizado com sucesso", plc.ID, plc.Name)
		processedIDs[plc.ID] = true

		// Registra o status do PLC se disponível
		if plc.Status != "" {
			statusKey := fmt.Sprintf("config:plc:%d:status", plc.ID)
			if err := s.cache.SetValue(statusKey, plc.Status); err == nil {
				log.Printf("Status do PLC ID %d (%s) sincronizado: %s", plc.ID, plc.Name, plc.Status)
			}
		}
	}

	// Armazena o hash atual para comparação futura
	if err := s.cache.SetValue(KeyConfigPLCsHash, currentHash); err != nil {
		log.Printf("Aviso: Não foi possível armazenar hash de PLCs: %v", err)
	}

	// Armazena o timestamp da última sincronização
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	if err := s.cache.SetValue(KeyConfigPLCsTime, timestamp); err != nil {
		log.Printf("Aviso: Não foi possível armazenar timestamp de PLCs: %v", err)
	}

	log.Printf("Sincronizados %d PLCs com sucesso", len(plcs))
	return nil
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
func (s *ConfigSync) SyncTags(ctx context.Context, forceSync bool) error {
	log.Println("Sincronizando Tags...")

	// Obtém todos os PLCs
	dbWithTimeout := s.db.WithTimeout(30 * time.Second)
	plcs, err := dbWithTimeout.GetActivePLCs()
	if err != nil {
		return fmt.Errorf("erro ao obter PLCs ativos para sincronização de tags: %w", err)
	}

	// Se não há PLCs, não há tags para sincronizar
	if len(plcs) == 0 {
		log.Println("Nenhum PLC ativo encontrado para sincronizar tags")
		return nil
	}

	totalTags := 0
	syncsPerformed := 0

	// Para cada PLC, sincroniza suas tags
	for _, plc := range plcs {
		// Verifica se devemos sincronizar este PLC específico
		shouldSync, err := s.shouldSyncTagsForPLC(plc.ID, forceSync)
		if err != nil {
			log.Printf("Erro ao verificar necessidade de sincronização para PLC %d: %v", plc.ID, err)
			// Em caso de erro, continuamos com a sincronização por segurança
			shouldSync = true
		}

		if !shouldSync {
			log.Printf("Tags do PLC %s (ID: %d) estão atualizadas, pulando sincronização", plc.Name, plc.ID)
			continue
		}

		// Início da sincronização para este PLC
		log.Printf("====== Sincronizando tags do PLC %s (ID: %d)... ======", plc.Name, plc.ID)

		// Obtém as tags com um timeout específico
		dbForTags := s.db.WithTimeout(30 * time.Second)
		tags, err := dbForTags.GetPLCTags(plc.ID)
		if err != nil {
			log.Printf("Erro ao obter tags do PLC %d: %v", plc.ID, err)
			s.logger.Error("ConfigSync", fmt.Sprintf("Erro ao obter tags do PLC %d: %v", plc.ID, err))
			continue
		}

		log.Printf("Obtidas %d tags do banco de dados para o PLC %s", len(tags), plc.Name)

		// Filtra apenas tags ativas
		var activeTags []database.Tag
		for _, tag := range tags {
			if tag.Active {
				activeTags = append(activeTags, tag)
			}
		}

		log.Printf("Filtradas %d tags ativas para o PLC %s", len(activeTags), plc.Name)

		// Calcula o hash das tags para detectar mudanças
		currentHash, err := calculateHash(activeTags)
		if err != nil {
			log.Printf("Erro ao calcular hash das tags do PLC %d: %v", plc.ID, err)
			// Em caso de erro, prosseguimos com a sincronização
		} else {
			// Armazena o hash para verificações futuras
			hashKey := fmt.Sprintf(KeyConfigTagsHash, plc.ID)
			if err := s.cache.SetValue(hashKey, currentHash); err != nil {
				log.Printf("Aviso: Não foi possível armazenar hash de tags do PLC %d: %v", plc.ID, err)
			}
		}

		// Chave para todas as tags de um PLC
		tagsKey := fmt.Sprintf("config:plc:%d:tags", plc.ID)

		// Mesmo se não houver tags ativas, armazenamos um array vazio
		if len(activeTags) == 0 {
			if err := s.safeStoreValue(tagsKey, "[]"); err != nil {
				log.Printf("Erro ao armazenar array vazio para PLC %d: %v", plc.ID, err)
				continue
			}
			log.Printf("Array vazio de tags armazenado para PLC %d", plc.ID)
			continue
		}

		// Serializa as tags ativas
		tagsJSON, err := json.Marshal(activeTags)
		if err != nil {
			log.Printf("Erro ao serializar tags do PLC %d: %v", plc.ID, err)
			continue
		}

		// Armazena a lista completa de tags com método seguro
		if err := s.safeStoreValue(tagsKey, string(tagsJSON)); err != nil {
			log.Printf("Erro fatal ao armazenar tags do PLC %d: %v", plc.ID, err)
			continue
		}

		log.Printf("Lista completa de %d tags sincronizada para PLC %d", len(activeTags), plc.ID)
		syncsPerformed++

		// Armazena cada tag individualmente para acesso rápido
		for _, tag := range activeTags {
			tagJSON, err := json.Marshal(tag)
			if err != nil {
				log.Printf("Erro ao serializar tag %d: %v", tag.ID, err)
				continue
			}

			// Chave para tag específica
			tagKey := fmt.Sprintf("config:plc:%d:tag:%d", plc.ID, tag.ID)
			if err := s.cache.SetValue(tagKey, string(tagJSON)); err != nil {
				log.Printf("Erro ao armazenar tag %d: %v", tag.ID, err)
				continue
			}

			// Chave para busca por nome
			nameKey := fmt.Sprintf("config:plc:%d:tagname:%s", plc.ID, tag.Name)
			if err := s.cache.SetValue(nameKey, fmt.Sprintf("%d", tag.ID)); err != nil {
				log.Printf("Erro ao armazenar mapeamento de nome da tag %s: %v", tag.Name, err)
				continue
			}
		}

		// Registra o tempo da última sincronização bem-sucedida
		timeKey := fmt.Sprintf(KeyConfigTagsTime, plc.ID)
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		if err := s.cache.SetValue(timeKey, timestamp); err != nil {
			log.Printf("Aviso: Não foi possível armazenar timestamp de tags do PLC %d: %v", plc.ID, err)
		}

		log.Printf("Sincronizadas %d tags para o PLC %s (ID: %d)", len(activeTags), plc.Name, plc.ID)
		totalTags += len(activeTags)
	}

	if syncsPerformed == 0 {
		log.Println("Não foi necessário sincronizar tags para nenhum PLC - todos estão atualizados")
	} else {
		log.Printf("Sincronização completa: %d tags em %d PLCs", totalTags, syncsPerformed)
	}

	return nil
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

	// Obtém timestamp da última sincronização de PLCs
	plcTimeStr, _ := s.cache.GetValue(KeyConfigPLCsTime)
	if plcTimeStr != "" {
		plcTime, err := strconv.ParseInt(plcTimeStr, 10, 64)
		if err == nil {
			status["plcs_last_sync"] = time.Unix(plcTime, 0).String()
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
					"last_sync":   time.Unix(timestamp, 0).String(),
					"seconds_ago": time.Now().Unix() - timestamp,
				}
			}
		}
	}
	status["tags_sync"] = tagsStatus

	return status
}
