package plc

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ConnectionPool gerencia um pool de conexões para PLCs
type ConnectionPool struct {
	connections map[string]*Client
	mutex       sync.RWMutex
	maxAge      time.Duration
	lastUsed    map[string]time.Time
	running     bool
	stopChan    chan struct{}
}

// NewConnectionPool cria um novo pool de conexões
func NewConnectionPool(maxAge time.Duration) *ConnectionPool {
	pool := &ConnectionPool{
		connections: make(map[string]*Client),
		lastUsed:    make(map[string]time.Time),
		maxAge:      maxAge,
		running:     true,
		stopChan:    make(chan struct{}),
	}

	// Inicia a rotina de limpeza
	go pool.cleanupRoutine()

	return pool
}

// generateKey cria uma chave única para cada configuração de PLC
func generateKey(ip string, rack, slot int) string {
	return fmt.Sprintf("%s:%d:%d", ip, rack, slot)
}

// generateKeyFromConfig cria uma chave a partir de ClientConfig
func generateKeyFromConfig(config ClientConfig) string {
	return fmt.Sprintf("%s:%d:%d", config.IPAddress, config.Rack, config.Slot)
}

// GetConnection obtém uma conexão existente ou cria uma nova
func (p *ConnectionPool) GetConnection(ip string, rack, slot int) (*Client, error) {
	key := generateKey(ip, rack, slot)

	// Tenta obter uma conexão existente
	p.mutex.RLock()
	client, exists := p.connections[key]
	if exists {
		lastUse := p.lastUsed[key]
		// Verifica se a conexão expirou
		if time.Since(lastUse) > p.maxAge {
			p.mutex.RUnlock()
			// Se expirou, remove e cria uma nova
			p.mutex.Lock()
			defer p.mutex.Unlock()

			// Verifica novamente após obter o lock exclusivo
			if client, exists = p.connections[key]; exists {
				if time.Since(p.lastUsed[key]) > p.maxAge {
					client.Close()
					delete(p.connections, key)
					delete(p.lastUsed, key)
					exists = false
				}
			}
		} else {
			// Atualiza o tempo de último uso
			p.mutex.RUnlock()
			p.mutex.Lock()
			p.lastUsed[key] = time.Now()
			p.mutex.Unlock()
			return client, nil
		}
	} else {
		p.mutex.RUnlock()
	}

	// Se não existe ou expirou, cria uma nova conexão
	newClient, err := NewClient(ip, rack, slot)
	if err != nil {
		return nil, err
	}

	// Armazena a nova conexão no pool
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.connections[key] = newClient
	p.lastUsed[key] = time.Now()

	return newClient, nil
}

// GetConnectionWithConfig obtém uma conexão usando uma configuração completa
func (p *ConnectionPool) GetConnectionWithConfig(config ClientConfig) (*Client, error) {
	key := generateKeyFromConfig(config)

	// Tenta obter uma conexão existente
	p.mutex.RLock()
	client, exists := p.connections[key]
	if exists {
		lastUse := p.lastUsed[key]
		// Verifica se a conexão expirou
		if time.Since(lastUse) > p.maxAge {
			p.mutex.RUnlock()
			// Se expirou, remove e cria uma nova
			p.mutex.Lock()
			defer p.mutex.Unlock()

			// Verifica novamente após obter o lock exclusivo
			if client, exists = p.connections[key]; exists {
				if time.Since(p.lastUsed[key]) > p.maxAge {
					client.Close()
					delete(p.connections, key)
					delete(p.lastUsed, key)
					exists = false
				}
			}
		} else {
			// Atualiza o tempo de último uso
			p.mutex.RUnlock()
			p.mutex.Lock()
			p.lastUsed[key] = time.Now()
			p.mutex.Unlock()
			return client, nil
		}
	} else {
		p.mutex.RUnlock()
	}

	// Se não existe ou expirou, cria uma nova conexão
	newClient, err := NewClientWithConfig(config)
	if err != nil {
		return nil, err
	}

	// Armazena a nova conexão no pool
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.connections[key] = newClient
	p.lastUsed[key] = time.Now()

	return newClient, nil
}

// Release notifica que uma conexão não é mais necessária
// Não fecha a conexão, apenas atualiza o tempo de último uso
func (p *ConnectionPool) Release(ip string, rack, slot int) {
	key := generateKey(ip, rack, slot)

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if _, exists := p.connections[key]; exists {
		p.lastUsed[key] = time.Now()
	}
}

// ReleaseWithConfig notifica que uma conexão não é mais necessária usando config
func (p *ConnectionPool) ReleaseWithConfig(config ClientConfig) {
	key := generateKeyFromConfig(config)

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if _, exists := p.connections[key]; exists {
		p.lastUsed[key] = time.Now()
	}
}

// Close fecha todas as conexões no pool
func (p *ConnectionPool) Close() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Sinaliza para a rotina de limpeza parar
	if p.running {
		p.running = false
		close(p.stopChan)
	}

	// Fecha todas as conexões
	for key, client := range p.connections {
		client.Close()
		delete(p.connections, key)
		delete(p.lastUsed, key)
	}

	log.Println("Pool de conexões PLC encerrado")
}

// cleanupRoutine verifica periodicamente conexões não utilizadas
func (p *ConnectionPool) cleanupRoutine() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return

		case <-ticker.C:
			p.mutex.Lock()

			now := time.Now()
			closedCount := 0

			for key, lastUse := range p.lastUsed {
				if now.Sub(lastUse) > p.maxAge {
					if client, exists := p.connections[key]; exists {
						client.Close()
						delete(p.connections, key)
						delete(p.lastUsed, key)
						closedCount++
					}
				}
			}

			if closedCount > 0 {
				log.Printf("Limpeza do pool de conexões concluída: fechadas %d conexões inativas. Conexões ativas: %d",
					closedCount, len(p.connections))
			}

			p.mutex.Unlock()
		}
	}
}

// GetActiveConnectionCount retorna o número de conexões ativas no pool
func (p *ConnectionPool) GetActiveConnectionCount() int {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return len(p.connections)
}

// MaxConnections verifica se o pool está com conexões demais
func (p *ConnectionPool) MaxConnections(maxAllowed int) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return len(p.connections) >= maxAllowed
}

// ConnectionDetails retorna detalhes de todas as conexões ativas
func (p *ConnectionPool) ConnectionDetails() []map[string]interface{} {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	details := make([]map[string]interface{}, 0, len(p.connections))

	for key, _ := range p.connections {
		parts := strings.Split(key, ":")
		if len(parts) != 3 {
			continue
		}

		var rack, slot int
		fmt.Sscanf(parts[1], "%d", &rack)
		fmt.Sscanf(parts[2], "%d", &slot)

		lastUsed := p.lastUsed[key]
		idleTime := time.Since(lastUsed)

		details = append(details, map[string]interface{}{
			"ip_address":   parts[0],
			"rack":         rack,
			"slot":         slot,
			"idle_seconds": int(idleTime.Seconds()),
			"last_used":    lastUsed.Format(time.RFC3339),
		})
	}

	return details
}
