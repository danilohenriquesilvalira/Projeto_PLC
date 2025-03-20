package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// TagValue estrutura para armazenar o valor de uma tag no Redis.
type TagValue struct {
	Value     interface{} `json:"value"`
	Timestamp time.Time   `json:"timestamp"`
	Quality   int         `json:"quality"`
}

// RedisCache encapsula a conexão e operações com o Redis.
type RedisCache struct {
	client *redis.Client
	ctx    context.Context
}

// NewRedisCache cria uma nova instância do cache Redis.
// Retorna a interface Cache em vez do tipo concreto.
func NewRedisCache(host string, port int, password string) (Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       0,
	})

	ctx := context.Background()

	// Testa a conexão.
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("erro ao conectar ao Redis: %w", err)
	}

	return &RedisCache{
		client: client,
		ctx:    ctx,
	}, nil
}

// Ping realiza um health check no Redis, retornando erro se a conexão não estiver saudável.
func (r *RedisCache) Ping() error {
	return r.client.Ping(r.ctx).Err()
}

// Close fecha a conexão com o Redis.
func (r *RedisCache) Close() error {
	return r.client.Close()
}

// SetTagValue armazena o valor de uma tag no Redis.
func (r *RedisCache) SetTagValue(plcID, tagID int, value interface{}) error {
	tagValue := TagValue{
		Value:     value,
		Timestamp: time.Now(),
		Quality:   100, // Valor padrão de qualidade (100 = boa).
	}

	data, err := json.Marshal(tagValue)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)
	return r.client.Set(r.ctx, key, data, 0).Err()
}

// GetTagValue recupera o valor de uma tag do Redis.
func (r *RedisCache) GetTagValue(plcID, tagID int) (*TagValue, error) {
	key := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)

	data, err := r.client.Get(r.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Chave não existe.
		}
		return nil, err
	}

	var tagValue TagValue
	if err := json.Unmarshal([]byte(data), &tagValue); err != nil {
		return nil, err
	}

	return &tagValue, nil
}

// GetTagValueAsNumber retorna o valor numérico da tag.
func (r *RedisCache) GetTagValueAsNumber(plcID, tagID int) (float64, error) {
	key := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)

	data, err := r.client.Get(r.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return 0, nil // Chave não existe.
		}
		return 0, err
	}

	var tagValue TagValue
	if err := json.Unmarshal([]byte(data), &tagValue); err != nil {
		return 0, err
	}

	// Converte o valor para float64, independentemente do tipo original.
	switch v := tagValue.Value.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	case json.Number:
		return v.Float64()
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("tipo não suportado: %T", tagValue.Value)
	}
}

// GetRawValue recupera um valor bruto do Redis pela chave.
func (r *RedisCache) GetRawValue(key string) (interface{}, error) {
	data, err := r.client.Get(r.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Chave não existe.
		}
		return nil, err
	}

	// Tenta deserializar o JSON.
	var tagValue TagValue
	if err := json.Unmarshal([]byte(data), &tagValue); err != nil {
		// Se não conseguir deserializar, retorna o dado bruto.
		return data, nil
	}

	// Retorna apenas o campo Value da estrutura TagValue.
	return tagValue.Value, nil
}

// SetTagValueWithQuality armazena o valor de uma tag no Redis com qualidade personalizada.
func (r *RedisCache) SetTagValueWithQuality(plcID, tagID int, value interface{}, quality int) error {
	tagValue := TagValue{
		Value:     value,
		Timestamp: time.Now(),
		Quality:   quality,
	}

	data, err := json.Marshal(tagValue)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)
	return r.client.Set(r.ctx, key, data, 0).Err()
}

// RegisterTagName registra o mapeamento entre nome da tag e seu ID.
func (r *RedisCache) RegisterTagName(plcID, tagID int, tagName string) error {
	tagKey := fmt.Sprintf("plc:%d:tagname:%s", plcID, tagName)
	return r.client.Set(r.ctx, tagKey, tagID, 0).Err()
}

// GetTagValueByName recupera o valor de uma tag pelo nome.
func (r *RedisCache) GetTagValueByName(plcID int, tagName string) (*TagValue, error) {
	// Primeiro busca o ID da tag pelo nome.
	tagKey := fmt.Sprintf("plc:%d:tagname:%s", plcID, tagName)

	// Busca o mapeamento nome -> ID.
	tagIDStr, err := r.client.Get(r.ctx, tagKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("tag com nome '%s' não encontrada no cache", tagName)
		}
		return nil, fmt.Errorf("erro ao buscar ID da tag: %w", err)
	}

	tagID, err := strconv.Atoi(tagIDStr)
	if err != nil {
		return nil, fmt.Errorf("ID de tag inválido no cache: %s", tagIDStr)
	}

	// Agora busca o valor pelo ID.
	return r.GetTagValue(plcID, tagID)
}

// PublishTagUpdate publica uma atualização de tag no canal PubSub.
func (r *RedisCache) PublishTagUpdate(plcID, tagID int, value interface{}) error {
	message := map[string]interface{}{
		"plc_id":    plcID,
		"tag_id":    tagID,
		"value":     value,
		"timestamp": time.Now(),
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("erro ao serializar mensagem: %w", err)
	}

	channel := fmt.Sprintf("tag_updates:%d", plcID)
	return r.client.Publish(r.ctx, channel, data).Err()
}

// GetAllPLCTags retorna todos os valores de tags de um PLC.
func (r *RedisCache) GetAllPLCTags(plcID int) (map[int]*TagValue, error) {
	pattern := fmt.Sprintf("plc:%d:tag:*", plcID)
	keys, err := r.client.Keys(r.ctx, pattern).Result()
	if err != nil {
		return nil, fmt.Errorf("erro ao buscar chaves de tags: %w", err)
	}

	result := make(map[int]*TagValue)
	for _, key := range keys {
		// Extrai o ID da tag da chave (formato: plc:1:tag:123).
		var tagID int
		if _, err := fmt.Sscanf(key, fmt.Sprintf("plc:%d:tag:%%d", plcID), &tagID); err != nil {
			continue // Ignora chaves com formato inválido.
		}

		tagValue, err := r.GetTagValue(plcID, tagID)
		if err != nil {
			continue // Ignora erros e continua.
		}

		result[tagID] = tagValue
	}

	return result, nil
}

// SetValue armazena uma string arbitrária no Redis
func (r *RedisCache) SetValue(key string, value string) error {
	return r.client.Set(r.ctx, key, value, 0).Err()
}

// GetValue recupera uma string arbitrária do Redis
func (r *RedisCache) GetValue(key string) (string, error) {
	val, err := r.client.Get(r.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil // Chave não existe
		}
		return "", err
	}
	return val, nil
}

// DeleteKey remove uma chave do Redis
func (r *RedisCache) DeleteKey(key string) error {
	return r.client.Del(r.ctx, key).Err()
}

// ListKeys lista todas as chaves que correspondem a um padrão no Redis
func (r *RedisCache) ListKeys(pattern string) ([]string, error) {
	return r.client.Keys(r.ctx, pattern).Result()
}
