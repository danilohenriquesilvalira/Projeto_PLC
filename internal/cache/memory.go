package cache

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemoryCache implementa a interface Cache usando armazenamento em memória
type MemoryCache struct {
	data  map[string][]byte
	mutex sync.RWMutex
}

// NewMemoryCache cria uma nova instância do cache em memória
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		data: make(map[string][]byte),
	}
}

// Close "fecha" o cache em memória (operação sem efeito para esse tipo)
func (m *MemoryCache) Close() error {
	return nil
}

// SetTagValue armazena o valor de uma tag no cache em memória
func (m *MemoryCache) SetTagValue(plcID, tagID int, value interface{}) error {
	tagValue := TagValue{
		Value:     value,
		Timestamp: time.Now(),
		Quality:   100, // Valor padrão de qualidade (100 = boa)
	}

	data, err := json.Marshal(tagValue)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.data[key] = data
	return nil
}

// GetTagValue recupera o valor de uma tag do cache em memória
func (m *MemoryCache) GetTagValue(plcID, tagID int) (*TagValue, error) {
	key := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	data, exists := m.data[key]
	if !exists {
		return nil, nil // Chave não existe
	}

	var tagValue TagValue
	if err := json.Unmarshal(data, &tagValue); err != nil {
		return nil, err
	}

	return &tagValue, nil
}

// GetTagValueAsNumber retorna o valor numérico da tag
func (m *MemoryCache) GetTagValueAsNumber(plcID, tagID int) (float64, error) {
	tagValue, err := m.GetTagValue(plcID, tagID)
	if err != nil {
		return 0, err
	}
	if tagValue == nil {
		return 0, nil
	}

	// Converte o valor para float64, independentemente do tipo original
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

// GetRawValue recupera um valor bruto do cache pela chave
func (m *MemoryCache) GetRawValue(key string) (interface{}, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	data, exists := m.data[key]
	if !exists {
		return nil, nil // Chave não existe
	}

	// Tenta deserializar o JSON (já que sabemos que os valores são armazenados em JSON)
	var tagValue TagValue
	if err := json.Unmarshal(data, &tagValue); err != nil {
		// Se não conseguir deserializar, retorna o dado bruto como string
		return string(data), nil
	}

	// Retorna apenas o campo Value da estrutura TagValue
	return tagValue.Value, nil
}

// SetTagValueWithQuality armazena o valor de uma tag com qualidade personalizada
func (m *MemoryCache) SetTagValueWithQuality(plcID, tagID int, value interface{}, quality int) error {
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

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.data[key] = data
	return nil
}

// RegisterTagName registra o mapeamento entre nome da tag e seu ID
func (m *MemoryCache) RegisterTagName(plcID, tagID int, tagName string) error {
	tagKey := fmt.Sprintf("plc:%d:tagname:%s", plcID, tagName)

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.data[tagKey] = []byte(strconv.Itoa(tagID))
	return nil
}

// GetTagValueByName recupera o valor de uma tag pelo nome
func (m *MemoryCache) GetTagValueByName(plcID int, tagName string) (*TagValue, error) {
	// Primeiro busca o ID da tag pelo nome
	tagKey := fmt.Sprintf("plc:%d:tagname:%s", plcID, tagName)

	m.mutex.RLock()
	tagIDBytes, exists := m.data[tagKey]
	m.mutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("tag com nome '%s' não encontrada no cache", tagName)
	}

	tagID, err := strconv.Atoi(string(tagIDBytes))
	if err != nil {
		return nil, fmt.Errorf("ID de tag inválido no cache: %s", string(tagIDBytes))
	}

	// Agora busca o valor pelo ID
	return m.GetTagValue(plcID, tagID)
}

// PublishTagUpdate simula a publicação de uma atualização (sem efeito real no cache em memória)
func (m *MemoryCache) PublishTagUpdate(plcID, tagID int, value interface{}) error {
	// Não temos um sistema pub/sub real no cache em memória,
	// mas salvamos o valor para manter a consistência
	return m.SetTagValue(plcID, tagID, value)
}

// GetAllPLCTags retorna todos os valores de tags de um PLC
func (m *MemoryCache) GetAllPLCTags(plcID int) (map[int]*TagValue, error) {
	pattern := fmt.Sprintf("plc:%d:tag:", plcID)
	result := make(map[int]*TagValue)

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for key, data := range m.data {
		// Verifica se a chave corresponde ao padrão
		if !strings.HasPrefix(key, pattern) {
			continue
		}

		// Extrai o ID da tag da chave (formato: plc:1:tag:123)
		var tagID int
		if _, err := fmt.Sscanf(key, fmt.Sprintf("plc:%d:tag:%%d", plcID), &tagID); err != nil {
			continue // Ignora chave com formato inválido
		}

		var tagValue TagValue
		if err := json.Unmarshal(data, &tagValue); err != nil {
			continue // Ignora erro e continua
		}

		result[tagID] = &tagValue
	}

	return result, nil
}

// SetValue armazena uma string arbitrária no cache em memória
func (m *MemoryCache) SetValue(key string, value string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.data[key] = []byte(value)
	return nil
}

// GetValue recupera uma string arbitrária do cache em memória
func (m *MemoryCache) GetValue(key string) (string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	data, exists := m.data[key]
	if !exists {
		return "", nil // Chave não existe
	}

	return string(data), nil
}

// DeleteKey remove uma chave do cache em memória
func (m *MemoryCache) DeleteKey(key string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	delete(m.data, key)
	return nil
}

// ListKeys lista todas as chaves que correspondem a um padrão no cache em memória
func (m *MemoryCache) ListKeys(pattern string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	var keys []string

	// Implementação simples de correspondência com wildcard *
	// Se termina com *, faz correspondência parcial no início
	hasWildcard := len(pattern) > 0 && pattern[len(pattern)-1] == '*'
	prefix := pattern
	if hasWildcard {
		prefix = pattern[:len(pattern)-1]
	}

	for key := range m.data {
		if hasWildcard {
			if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
				keys = append(keys, key)
			}
		} else if key == pattern {
			keys = append(keys, key)
		}
	}

	return keys, nil
}
