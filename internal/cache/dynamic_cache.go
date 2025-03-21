package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// RedisConfig contém as configurações para conexão com o Redis.
type RedisConfig struct {
	Host     string
	Port     int
	Password string
}

// DynamicCache é um wrapper que implementa a interface Cache e
// alterna dinamicamente entre RedisCache e MemoryCache conforme o health check.
type DynamicCache struct {
	mu       sync.RWMutex
	active   Cache
	redisCfg RedisConfig
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewDynamicCache tenta conectar ao Redis e, se falhar, usa o cache em memória como fallback.
func NewDynamicCache(ctx context.Context, redisCfg RedisConfig) *DynamicCache {
	active, err := NewRedisCache(redisCfg.Host, redisCfg.Port, redisCfg.Password)
	if err != nil {
		log.Printf("[DynamicCache] Erro ao conectar ao Redis: %v. Usando cache em memória como fallback.", err)
		active = NewMemoryCache()
	} else {
		log.Println("[DynamicCache] Conectado ao Redis com sucesso.")
	}
	dctx, cancel := context.WithCancel(ctx)
	dc := &DynamicCache{
		active:   active,
		redisCfg: redisCfg,
		ctx:      dctx,
		cancel:   cancel,
	}
	go dc.healthCheckRoutine()
	return dc
}

// healthCheckRoutine verifica periodicamente a saúde do cache ativo.
// Se o Redis estiver ativo e o ping falhar, alterna para MemoryCache.
// Se estiver usando MemoryCache, tenta reconectar ao Redis.
func (dc *DynamicCache) healthCheckRoutine() {
	ticker := time.NewTicker(5 * time.Second) // Aumentar frequência para 5s (era 10s)
	defer ticker.Stop()

	recoveryAttempt := 0
	maxBackoff := 60 // Número máximo de tentativas antes de voltar ao intervalo inicial

	for {
		select {
		case <-dc.ctx.Done():
			log.Println("[DynamicCache] Health check routine finalizada.")
			return
		case <-ticker.C:
			dc.mu.RLock()
			current := dc.active
			dc.mu.RUnlock()

			switch cacheInstance := current.(type) {
			case *RedisCache:
				// Se o cache ativo é Redis, realiza o Ping.
				err := cacheInstance.Ping()
				if err != nil {
					log.Printf("[DynamicCache] Health check do Redis falhou: %v. Alternando para MemoryCache.", err)
					newMem := NewMemoryCache()
					dc.mu.Lock()
					dc.active = newMem
					dc.mu.Unlock()

					// Resetar tentativas quando caímos para memória
					recoveryAttempt = 0
				} else {
					log.Println("[DynamicCache] Health check do Redis: OK")
				}
			default:
				// Se estiver usando MemoryCache, tenta reconectar ao Redis
				// Com estratégia de recuo exponencial
				shouldAttempt := (recoveryAttempt == 0) ||
					(recoveryAttempt < 5) ||
					(recoveryAttempt%12 == 0) // a cada ~60s depois das primeiras 5 tentativas

				if shouldAttempt {
					log.Printf("[DynamicCache] Tentativa #%d de reconexão ao Redis", recoveryAttempt+1)

					newRedis, err := NewRedisCache(dc.redisCfg.Host, dc.redisCfg.Port, dc.redisCfg.Password)
					if err == nil {
						log.Println("[DynamicCache] Reconectado ao Redis com sucesso. Alternando para RedisCache.")
						dc.mu.Lock()
						dc.active = newRedis
						dc.mu.Unlock()

						// Resetar contagem de tentativas após sucesso
						recoveryAttempt = 0
					} else {
						log.Printf("[DynamicCache] Tentativa de reconexão ao Redis falhou: %v", err)
						recoveryAttempt++

						// Limitar o número máximo de tentativas
						if recoveryAttempt >= maxBackoff {
							recoveryAttempt = 0
							log.Printf("[DynamicCache] Resetando contagem de tentativas após %d falhas", maxBackoff)
						}
					}
				} else {
					recoveryAttempt++
					if recoveryAttempt >= maxBackoff {
						recoveryAttempt = 0
					}
				}
			}
		}
	}
}

// CheckHealth verificar a saúde atual do cache
func (dc *DynamicCache) CheckHealth() (string, error) {
	dc.mu.RLock()
	current := dc.active
	dc.mu.RUnlock()

	switch cacheInstance := current.(type) {
	case *RedisCache:
		if err := cacheInstance.Ping(); err != nil {
			return "Redis (Erro)", err
		}
		return "Redis (OK)", nil

	case *MemoryCache:
		// MemoryCache não tem um mecanismo de falha interna
		return "Memory", nil

	default:
		return "Unknown", fmt.Errorf("tipo de cache desconhecido")
	}
}

// ForceRedisReconnect força uma reconexão ao Redis
func (dc *DynamicCache) ForceRedisReconnect() error {
	dc.mu.RLock()
	current := dc.active
	isRedis := current != nil && fmt.Sprintf("%T", current) == "*cache.RedisCache"
	dc.mu.RUnlock()

	// Se já estamos usando Redis, tenta fazer ping
	if isRedis {
		if redisCache, ok := current.(*RedisCache); ok {
			if err := redisCache.Ping(); err == nil {
				// Redis já está conectado e saudável
				return nil
			}
		}
	}

	// Tenta reconectar ao Redis
	newRedis, err := NewRedisCache(dc.redisCfg.Host, dc.redisCfg.Port, dc.redisCfg.Password)
	if err != nil {
		return fmt.Errorf("falha ao reconectar ao Redis: %w", err)
	}

	dc.mu.Lock()
	oldCache := dc.active
	dc.active = newRedis
	dc.mu.Unlock()

	log.Println("[DynamicCache] Forçada reconexão ao Redis com sucesso")

	// Tenta migrar dados importantes do cache anterior, se for memória
	if oldCache != nil && fmt.Sprintf("%T", oldCache) == "*cache.MemoryCache" {
		go dc.migrateCriticalData(oldCache)
	}

	return nil
}

// migrateCriticalData migra dados críticos entre caches
func (dc *DynamicCache) migrateCriticalData(oldCache Cache) {
	// Aqui você pode implementar a migração de dados críticos
	// do cache anterior (geralmente memória) para o novo (Redis)

	// Exemplos de dados críticos podem incluir:
	// 1. Configurações de PLCs
	// 2. Estados de conexão
	// 3. Valores mais recentes de tags importantes

	log.Println("[DynamicCache] Iniciando migração de dados críticos")

	// No mínimo, precisamos migrar as configurações de PLCs e seus status
	plcsJSON, err := oldCache.GetValue("config:plcs:all")
	if err == nil && plcsJSON != "" {
		if err := dc.active.SetValue("config:plcs:all", plcsJSON); err != nil {
			log.Printf("[DynamicCache] Erro ao migrar lista de PLCs: %v", err)
		} else {
			log.Println("[DynamicCache] Lista de PLCs migrada com sucesso")

			// Tentar migrar também status de cada PLC
			var plcs []struct {
				ID int `json:"id"`
			}

			if err := json.Unmarshal([]byte(plcsJSON), &plcs); err == nil {
				for _, plc := range plcs {
					// Migrar status
					status, err := oldCache.GetValue(fmt.Sprintf("config:plc:%d:status", plc.ID))
					if err == nil && status != "" {
						_ = dc.active.SetValue(fmt.Sprintf("config:plc:%d:status", plc.ID), status)
					}

					// Migrar configuração do PLC
					plcConfig, err := oldCache.GetValue(fmt.Sprintf("config:plc:%d", plc.ID))
					if err == nil && plcConfig != "" {
						_ = dc.active.SetValue(fmt.Sprintf("config:plc:%d", plc.ID), plcConfig)
					}

					// Migrar lista de tags do PLC
					plcTags, err := oldCache.GetValue(fmt.Sprintf("config:plc:%d:tags", plc.ID))
					if err == nil && plcTags != "" {
						_ = dc.active.SetValue(fmt.Sprintf("config:plc:%d:tags", plc.ID), plcTags)
					}
				}
			}
		}
	}

	log.Println("[DynamicCache] Migração de dados críticos concluída")
}

// Close cancela o health check e fecha o cache ativo.
func (dc *DynamicCache) Close() error {
	dc.cancel()
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.active.Close()
}

// Métodos abaixo delegam as operações à instância ativa de Cache.

func (dc *DynamicCache) SetTagValue(plcID, tagID int, value interface{}) error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.SetTagValue(plcID, tagID, value)
}

func (dc *DynamicCache) GetTagValue(plcID, tagID int) (*TagValue, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.GetTagValue(plcID, tagID)
}

func (dc *DynamicCache) GetTagValueAsNumber(plcID, tagID int) (float64, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.GetTagValueAsNumber(plcID, tagID)
}

func (dc *DynamicCache) GetRawValue(key string) (interface{}, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.GetRawValue(key)
}

func (dc *DynamicCache) SetTagValueWithQuality(plcID, tagID int, value interface{}, quality int) error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.SetTagValueWithQuality(plcID, tagID, value, quality)
}

func (dc *DynamicCache) RegisterTagName(plcID, tagID int, tagName string) error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.RegisterTagName(plcID, tagID, tagName)
}

func (dc *DynamicCache) GetTagValueByName(plcID int, tagName string) (*TagValue, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.GetTagValueByName(plcID, tagName)
}

func (dc *DynamicCache) PublishTagUpdate(plcID, tagID int, value interface{}) error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.PublishTagUpdate(plcID, tagID, value)
}

func (dc *DynamicCache) GetAllPLCTags(plcID int) (map[int]*TagValue, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.GetAllPLCTags(plcID)
}

// SetValue delega para o cache ativo
func (dc *DynamicCache) SetValue(key string, value string) error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.SetValue(key, value)
}

// GetValue recupera uma string arbitrária do cache ativo
func (dc *DynamicCache) GetValue(key string) (string, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.GetValue(key)
}

// DeleteKey delega para o cache ativo
func (dc *DynamicCache) DeleteKey(key string) error {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.DeleteKey(key)
}

// ListKeys delega para o cache ativo
func (dc *DynamicCache) ListKeys(pattern string) ([]string, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return dc.active.ListKeys(pattern)
}
