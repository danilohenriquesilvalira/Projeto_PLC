package plcmanager

import (
	"Projeto_PLC/internal/plc"
	"sync"
	"time"
)

// TagReader encapsula a lógica de leitura de uma tag específica
type TagReader struct {
	plcClient  *plc.Client
	dbNumber   int
	byteOffset int
	bitOffset  int
	dataType   string
	scanRate   time.Duration
	lastRead   time.Time
	value      interface{}
	mutex      sync.RWMutex
}

// NewTagReader cria um novo leitor de tag
func NewTagReader(client *plc.Client, dbNumber, byteOffset, bitOffset int, dataType string, scanRate time.Duration) *TagReader {
	return &TagReader{
		plcClient:  client,
		dbNumber:   dbNumber,
		byteOffset: byteOffset,
		bitOffset:  bitOffset,
		dataType:   dataType,
		scanRate:   scanRate,
		lastRead:   time.Time{},
	}
}

// ReadTag realiza a leitura da tag do PLC
func (tr *TagReader) ReadTag() (interface{}, error) {
	// Verificar se é hora de ler (baseado no scanRate)
	tr.mutex.Lock()
	defer tr.mutex.Unlock()

	if time.Since(tr.lastRead) < tr.scanRate {
		return tr.value, nil // Retorna o último valor se ainda não expirou
	}

	// Realizar leitura do PLC
	value, err := tr.plcClient.ReadTag(tr.dbNumber, tr.byteOffset, tr.dataType, tr.bitOffset)
	if err == nil {
		tr.value = value
		tr.lastRead = time.Now()
	}

	return value, err
}

// SetScanRate atualiza a taxa de leitura
func (tr *TagReader) SetScanRate(rate time.Duration) {
	tr.mutex.Lock()
	defer tr.mutex.Unlock()
	tr.scanRate = rate
}

// GetScanRate retorna a taxa de leitura atual
func (tr *TagReader) GetScanRate() time.Duration {
	tr.mutex.RLock()
	defer tr.mutex.RUnlock()
	return tr.scanRate
}

// VolatilityTracker rastreia a volatilidade de valores de tags para
// possibilitar ajustes dinâmicos nas taxas de leitura
type VolatilityTracker struct {
	historySize       int
	valueHistory      []interface{}
	changeCount       int
	totalSamples      int
	lastChangeIndex   int
	mutex             sync.RWMutex
	stableThreshold   float64
	volatileThreshold float64
}

// NewVolatilityTracker cria um novo rastreador de volatilidade
func NewVolatilityTracker(historySize int) *VolatilityTracker {
	return &VolatilityTracker{
		historySize:       historySize,
		valueHistory:      make([]interface{}, 0, historySize),
		changeCount:       0,
		totalSamples:      0,
		lastChangeIndex:   0,
		stableThreshold:   0.1, // 10% de mudanças ou menos significa estável
		volatileThreshold: 0.5, // 50% ou mais de mudanças significa volátil
	}
}

// AddValue adiciona um novo valor ao histórico e atualiza métricas
func (vt *VolatilityTracker) AddValue(value interface{}) {
	vt.mutex.Lock()
	defer vt.mutex.Unlock()

	// Verifica se o valor mudou em relação ao último
	valueChanged := false
	if len(vt.valueHistory) > 0 {
		lastValue := vt.valueHistory[len(vt.valueHistory)-1]
		valueChanged = !CompareValues(lastValue, value)
	}

	// Incrementa contadores
	vt.totalSamples++
	if valueChanged {
		vt.changeCount++
		vt.lastChangeIndex = 0
	} else {
		vt.lastChangeIndex++
	}

	// Adiciona ao histórico, respeitando o tamanho máximo
	if len(vt.valueHistory) >= vt.historySize {
		// Remover o primeiro elemento (mais antigo)
		vt.valueHistory = vt.valueHistory[1:]
	}
	vt.valueHistory = append(vt.valueHistory, value)

	// Limita contadores ao tamanho do histórico para manter a relevância das métricas
	if vt.totalSamples > vt.historySize {
		vt.totalSamples = vt.historySize
	}
}

// ChangeRate retorna a taxa de mudança (entre 0.0 e 1.0)
func (vt *VolatilityTracker) ChangeRate() float64 {
	vt.mutex.RLock()
	defer vt.mutex.RUnlock()

	if vt.totalSamples == 0 {
		return 0.0
	}
	return float64(vt.changeCount) / float64(vt.totalSamples)
}

// TimeSinceLastChange retorna o número de leituras desde a última mudança
func (vt *VolatilityTracker) TimeSinceLastChange() int {
	vt.mutex.RLock()
	defer vt.mutex.RUnlock()
	return vt.lastChangeIndex
}

// IsStable retorna true se a tag tiver um comportamento estável (mudanças infrequentes)
func (vt *VolatilityTracker) IsStable() bool {
	// Precisa ter um mínimo de amostras para determinar estabilidade
	if vt.totalSamples < vt.historySize/2 {
		return false
	}
	return vt.ChangeRate() <= vt.stableThreshold
}

// IsVolatile retorna true se a tag tiver um comportamento volátil (mudanças frequentes)
func (vt *VolatilityTracker) IsVolatile() bool {
	// Precisa ter um mínimo de amostras para determinar volatilidade
	if vt.totalSamples < vt.historySize/2 {
		return false
	}
	return vt.ChangeRate() >= vt.volatileThreshold
}

// CompareValues compara dois valores para determinar se são iguais
// Reutilizando a função do pacote plc ou criando uma versão aqui
func CompareValues(old, new interface{}) bool {
	// Se ambos forem nil, são iguais
	if old == nil && new == nil {
		return true
	}
	// Se apenas um for nil, são diferentes
	if old == nil || new == nil {
		return false
	}

	// Comparação direta para tipos simples
	return old == new
}

// AdaptiveTagReader adiciona comportamento adaptativo ao leitor de tags padrão
type AdaptiveTagReader struct {
	baseReader       *TagReader // O leitor de tags original
	volatility       *VolatilityTracker
	minScanRate      time.Duration
	maxScanRate      time.Duration
	currentScanRate  time.Duration
	originalScanRate time.Duration
	lastAdjustment   time.Time
	adjustInterval   time.Duration
	enabled          bool
}

// NewAdaptiveTagReader cria um novo leitor adaptativo
func NewAdaptiveTagReader(baseReader *TagReader, minRate, maxRate time.Duration) *AdaptiveTagReader {
	return &AdaptiveTagReader{
		baseReader:       baseReader,
		volatility:       NewVolatilityTracker(20), // monitora últimas 20 leituras
		minScanRate:      minRate,
		maxScanRate:      maxRate,
		currentScanRate:  baseReader.scanRate,
		originalScanRate: baseReader.scanRate,
		lastAdjustment:   time.Now(),
		adjustInterval:   time.Second * 30, // ajusta taxa a cada 30 segundos no máximo
		enabled:          true,
	}
}

// ReadTag realiza leitura com taxa adaptativa
func (ar *AdaptiveTagReader) ReadTag() (interface{}, error) {
	// Usar o leitor de base para a leitura real
	value, err := ar.baseReader.ReadTag()

	// Atualizar o rastreador de volatilidade
	if err == nil {
		ar.volatility.AddValue(value)

		// Verificar se é hora de ajustar a taxa de leitura
		if ar.enabled && time.Since(ar.lastAdjustment) > ar.adjustInterval {
			ar.adjustScanRate()
			ar.lastAdjustment = time.Now()
		}
	}

	return value, err
}

// adjustScanRate ajusta a taxa de leitura com base na volatilidade
func (ar *AdaptiveTagReader) adjustScanRate() {
	// Se a tag for estável (poucas mudanças), reduzir frequência
	if ar.volatility.IsStable() {
		// Aumentar o intervalo (reduzir frequência) até o máximo permitido
		newRate := ar.currentScanRate * 2
		if newRate > ar.maxScanRate {
			newRate = ar.maxScanRate
		}

		if newRate != ar.currentScanRate {
			ar.currentScanRate = newRate
			ar.baseReader.SetScanRate(newRate)
		}
	} else if ar.volatility.IsVolatile() {
		// Se a tag for volátil (muitas mudanças), aumentar frequência
		newRate := ar.currentScanRate / 2
		if newRate < ar.minScanRate {
			newRate = ar.minScanRate
		}

		if newRate != ar.currentScanRate {
			ar.currentScanRate = newRate
			ar.baseReader.SetScanRate(newRate)
		}
	} else {
		// Caso intermediário, voltar para a taxa original
		if ar.currentScanRate != ar.originalScanRate {
			ar.currentScanRate = ar.originalScanRate
			ar.baseReader.SetScanRate(ar.originalScanRate)
		}
	}
}

// Enable ativa o comportamento adaptativo
func (ar *AdaptiveTagReader) Enable() {
	ar.enabled = true
}

// Disable desativa o comportamento adaptativo e restaura a taxa original
func (ar *AdaptiveTagReader) Disable() {
	ar.enabled = false
	ar.currentScanRate = ar.originalScanRate
	ar.baseReader.SetScanRate(ar.originalScanRate)
}

// GetCurrentRate retorna a taxa de leitura atual
func (ar *AdaptiveTagReader) GetCurrentRate() time.Duration {
	return ar.currentScanRate
}

// GetStatistics retorna estatísticas sobre o comportamento adaptativo
func (ar *AdaptiveTagReader) GetStatistics() map[string]interface{} {
	return map[string]interface{}{
		"change_rate":            ar.volatility.ChangeRate(),
		"time_since_last_change": ar.volatility.TimeSinceLastChange(),
		"is_stable":              ar.volatility.IsStable(),
		"is_volatile":            ar.volatility.IsVolatile(),
		"original_scan_rate_ms":  ar.originalScanRate.Milliseconds(),
		"current_scan_rate_ms":   ar.currentScanRate.Milliseconds(),
		"min_scan_rate_ms":       ar.minScanRate.Milliseconds(),
		"max_scan_rate_ms":       ar.maxScanRate.Milliseconds(),
	}
}
