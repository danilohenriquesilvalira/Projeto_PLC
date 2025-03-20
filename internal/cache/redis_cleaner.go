package cache

import (
	"context"
	"log"
	"time"
)

// RedisCleaner gerencia limpeza periódica de chaves não essenciais
type RedisCleaner struct {
	redis    *RedisCache
	ctx      context.Context
	interval time.Duration
}

// NewRedisCleaner cria um novo limpador de cache
func NewRedisCleaner(ctx context.Context, redis *RedisCache, interval time.Duration) *RedisCleaner {
	return &RedisCleaner{
		redis:    redis,
		ctx:      ctx,
		interval: interval,
	}
}

// Start inicia o processo de limpeza periódica
func (rc *RedisCleaner) Start() {
	log.Printf("Iniciando limpeza periódica de cache Redis (intervalo: %v)", rc.interval)

	go rc.runCleanupLoop()
}

// runCleanupLoop executa o loop de limpeza
func (rc *RedisCleaner) RunCleanup() error {
	// Limpar tags de runtime (valores atuais)
	keys, err := rc.redis.client.Keys(rc.redis.ctx, "plc:*:tag:*").Result()
	if err != nil {
		return err
	}

	if len(keys) > 0 {
		// Deletar em lotes para não bloquear o Redis
		const batchSize = 1000
		for i := 0; i < len(keys); i += batchSize {
			end := i + batchSize
			if end > len(keys) {
				end = len(keys)
			}
			batch := keys[i:end]

			if len(batch) > 0 {
				if err := rc.redis.client.Del(rc.redis.ctx, batch...).Err(); err != nil {
					log.Printf("Erro ao remover lote de chaves: %v", err)
				}
			}
		}
		log.Printf("Limpeza de cache concluída: removidas %d chaves", len(keys))
	}

	return nil
}

// runCleanupLoop executa o loop de limpeza periódica
func (rc *RedisCleaner) runCleanupLoop() {
	ticker := time.NewTicker(rc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-rc.ctx.Done():
			log.Println("Encerrando limpeza periódica de cache Redis")
			return

		case <-ticker.C:
			log.Println("Executando limpeza periódica de cache Redis...")
			if err := rc.RunCleanup(); err != nil {
				log.Printf("Erro na limpeza de cache: %v", err)
			}
		}
	}
}
