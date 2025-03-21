package common

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// CircuitBreakerState define os possíveis estados do circuit breaker
type CircuitBreakerState string

const (
	StateClosed   CircuitBreakerState = "CLOSED"    // Funcionamento normal
	StateOpen     CircuitBreakerState = "OPEN"      // Interrompido devido a falhas
	StateHalfOpen CircuitBreakerState = "HALF_OPEN" // Testando recuperação
)

var (
	ErrCircuitBreakerOpen    = errors.New("circuit breaker está aberto")
	ErrCircuitBreakerTimeout = errors.New("operação excedeu timeout do circuit breaker")
)

// CircuitBreaker implementa o padrão circuit breaker para evitar falhas em cascata
type CircuitBreaker struct {
	name             string
	failureThreshold int           // Número de falhas antes de abrir
	resetTimeout     time.Duration // Tempo de espera até testar novamente
	operationTimeout time.Duration // Timeout para operações individuais
	halfOpenMaxCalls int           // Número máximo de chamadas em estado half-open

	mutex             sync.RWMutex
	state             CircuitBreakerState
	failureCount      int
	lastStateChange   time.Time
	lastFailure       time.Time
	halfOpenCallCount int

	// Métricas e estatísticas
	totalCalls         int64
	successCalls       int64
	failedCalls        int64
	rejectedCalls      int64
	consecutiveSuccess int

	// Callbacks
	onStateChange func(name string, from, to CircuitBreakerState)
}

// CircuitBreakerOption define uma opção de configuração para o circuit breaker
type CircuitBreakerOption func(*CircuitBreaker)

// WithFailureThreshold configura o limite de falhas
func WithFailureThreshold(threshold int) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		cb.failureThreshold = threshold
	}
}

// WithResetTimeout configura o tempo de reset
func WithResetTimeout(timeout time.Duration) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		cb.resetTimeout = timeout
	}
}

// WithOperationTimeout configura o timeout de operação
func WithOperationTimeout(timeout time.Duration) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		cb.operationTimeout = timeout
	}
}

// WithHalfOpenMaxCalls configura o número máximo de chamadas em estado half-open
func WithHalfOpenMaxCalls(max int) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		cb.halfOpenMaxCalls = max
	}
}

// WithOnStateChange configura um callback para mudanças de estado
func WithOnStateChange(callback func(name string, from, to CircuitBreakerState)) CircuitBreakerOption {
	return func(cb *CircuitBreaker) {
		cb.onStateChange = callback
	}
}

// NewCircuitBreaker cria um novo circuit breaker
func NewCircuitBreaker(name string, options ...CircuitBreakerOption) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:               name,
		failureThreshold:   5,                // 5 falhas para abrir por padrão
		resetTimeout:       10 * time.Second, // 10 segundos para tentar novamente
		operationTimeout:   5 * time.Second,  // 5 segundos de timeout por operação
		halfOpenMaxCalls:   1,                // Apenas 1 chamada de teste em half-open
		state:              StateClosed,
		failureCount:       0,
		lastStateChange:    time.Now(),
		consecutiveSuccess: 0,
		onStateChange:      nil,
	}

	// Aplicar opções
	for _, option := range options {
		option(cb)
	}

	return cb
}

// changeState altera o estado do circuit breaker
func (cb *CircuitBreaker) changeState(newState CircuitBreakerState) {
	if cb.state != newState {
		oldState := cb.state
		cb.state = newState
		cb.lastStateChange = time.Now()

		// Se estamos fechando o circuito, resetar contadores
		if newState == StateClosed {
			cb.failureCount = 0
			cb.consecutiveSuccess = 0
		}

		// Se estamos meio-abrindo o circuito, resetar contadores de half-open
		if newState == StateHalfOpen {
			cb.halfOpenCallCount = 0
			cb.consecutiveSuccess = 0
		}

		// Notificar sobre a mudança de estado
		log.Printf("Circuit Breaker '%s': Estado alterado de %s para %s", cb.name, oldState, newState)

		// Chamar callback se definido
		if cb.onStateChange != nil {
			cb.onStateChange(cb.name, oldState, newState)
		}
	}
}

// Execute executa uma operação com proteção do circuit breaker
func (cb *CircuitBreaker) Execute(operation func() error) error {
	cb.mutex.Lock()

	cb.totalCalls++

	// Verificar estado atual
	switch cb.state {
	case StateOpen:
		// Se estamos abertos, verificar se já podemos testar novamente
		if time.Since(cb.lastStateChange) > cb.resetTimeout {
			cb.changeState(StateHalfOpen)
		} else {
			// Ainda aberto, rejeitar chamada
			cb.rejectedCalls++
			cb.mutex.Unlock()
			return ErrCircuitBreakerOpen
		}

	case StateHalfOpen:
		// Em half-open, permitir apenas um número limitado de chamadas
		if cb.halfOpenCallCount >= cb.halfOpenMaxCalls {
			cb.rejectedCalls++
			cb.mutex.Unlock()
			return ErrCircuitBreakerOpen
		}
		cb.halfOpenCallCount++
	}

	cb.mutex.Unlock()

	// Executar a operação com timeout
	resultChan := make(chan error, 1)

	go func() {
		result := operation()
		resultChan <- result
	}()

	// Aguardar resultado com timeout
	var err error
	select {
	case err = <-resultChan:
		// Operação concluída
	case <-time.After(cb.operationTimeout):
		err = ErrCircuitBreakerTimeout
	}

	// Atualizar estado com base no resultado
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if err != nil {
		// Falha
		cb.failedCalls++
		cb.failureCount++
		cb.consecutiveSuccess = 0
		cb.lastFailure = time.Now()

		// Verificar se devemos abrir o circuito
		if cb.state == StateClosed && cb.failureCount >= cb.failureThreshold {
			cb.changeState(StateOpen)
		} else if cb.state == StateHalfOpen {
			// Em half-open, qualquer falha reabre o circuito
			cb.changeState(StateOpen)
		}

		return err
	}

	// Sucesso
	cb.successCalls++
	cb.consecutiveSuccess++

	// Se estamos em half-open e tivemos sucesso, fechar o circuito
	if cb.state == StateHalfOpen && cb.consecutiveSuccess >= cb.halfOpenMaxCalls {
		cb.changeState(StateClosed)
	}

	return nil
}

// ForceOpen força o circuit breaker para estado aberto
func (cb *CircuitBreaker) ForceOpen() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.changeState(StateOpen)
}

// ForceClose força o circuit breaker para estado fechado
func (cb *CircuitBreaker) ForceClose() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.changeState(StateClosed)
}

// GetState retorna o estado atual do circuit breaker
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

// GetStats retorna estatísticas do circuit breaker
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	return map[string]interface{}{
		"name":                cb.name,
		"state":               string(cb.state),
		"failure_count":       cb.failureCount,
		"failure_threshold":   cb.failureThreshold,
		"total_calls":         cb.totalCalls,
		"success_calls":       cb.successCalls,
		"failed_calls":        cb.failedCalls,
		"rejected_calls":      cb.rejectedCalls,
		"consecutive_success": cb.consecutiveSuccess,
		"time_in_state":       time.Since(cb.lastStateChange).String(),
		"last_failure":        cb.lastFailure.Format(time.RFC3339),
	}
}

// String retorna uma representação string do circuit breaker
func (cb *CircuitBreaker) String() string {
	stats := cb.GetStats()
	return fmt.Sprintf("CircuitBreaker[%s] State=%s, Failures=%d/%d",
		stats["name"], stats["state"], stats["failure_count"], stats["failure_threshold"])
}
