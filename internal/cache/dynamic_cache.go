package cache

import (
	"context"
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
	ticker := time.NewTicker(10 * time.Second) // interval de 10s para testes; ajuste conforme necessário
	defer ticker.Stop()

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
				} else {
					log.Println("[DynamicCache] Health check do Redis: OK")
				}
			default:
				// Se estiver usando MemoryCache, tenta reconectar ao Redis.
				newRedis, err := NewRedisCache(dc.redisCfg.Host, dc.redisCfg.Port, dc.redisCfg.Password)
				if err == nil {
					log.Println("[DynamicCache] Reconectado ao Redis com sucesso. Alternando para RedisCache.")
					dc.mu.Lock()
					dc.active = newRedis
					dc.mu.Unlock()
				} else {
					log.Printf("[DynamicCache] Tentativa de reconexão ao Redis falhou: %v", err)
				}
			}
		}
	}
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

// GetValue delega para o cache ativo
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
