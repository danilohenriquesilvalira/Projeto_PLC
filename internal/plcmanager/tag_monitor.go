package plcmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"Projeto_PLC/internal/database"
	plclib "Projeto_PLC/internal/plc" // Usando o mesmo alias para consistência
)

// Variável global para controlar logs detalhados
var DetailedLogging = false

// TagConfig guarda os parâmetros para o monitoramento de uma tag
type TagConfig struct {
	ScanRate       time.Duration
	MonitorChanges bool
}

// TagRunner guarda o cancelamento da goroutine de coleta e a configuração aplicada
type TagRunner struct {
	cancel context.CancelFunc
	config TagConfig
}

// isNetworkError verifica se um erro é relacionado a problemas de rede
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "forcibly closed") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "uso de um arquivo fechado") ||
		strings.Contains(errStr, "Foi forçado o cancelamento") ||
		strings.Contains(errStr, "wsasend")
}

// minDuration retorna a menor das duas durações
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// SetDetailedLogging ativa ou desativa logs detalhados
func (m *Manager) SetDetailedLogging(enabled bool) {
	DetailedLogging = enabled
	if enabled {
		log.Println("Logs detalhados ATIVADOS - Mostrando todas as operações de leitura/escrita")
	} else {
		log.Println("Logs detalhados DESATIVADOS - Mostrando apenas erros e mudanças de valores")
	}
}

// managePLCTags gerencia as goroutines de coleta de tags de um PLC
func (m *Manager) managePLCTags(ctx context.Context, plcID int, plcName string, client *plclib.Client) error {
	// Mapa para armazenar as goroutines de monitoramento de tags
	tagRunners := make(map[int]TagRunner)

	// Ticker para verificação periódica de tags
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Canal para receber erros das goroutines de tags
	errChan := make(chan error, 10)

	// Contador para verificar alterações no número de tags
	var lastTagCount int

	// Registra início do monitoramento apenas uma vez
	m.logger.InfoWithDetails("Tag Monitor",
		fmt.Sprintf("Iniciando gerenciamento de tags para PLC %s", plcName),
		fmt.Sprintf("PLC ID: %d, Endereço IP: %s", plcID, client.GetConfig().IPAddress))

	// Desativar logs detalhados por padrão
	DetailedLogging = false

	for {
		select {
		case <-ctx.Done():
			// Encerra todas as goroutines de tags sem logar cada uma individualmente
			for tagID, runner := range tagRunners {
				runner.cancel()
				if DetailedLogging {
					log.Printf("Encerrado monitoramento da tag ID %d no PLC %s", tagID, plcName)
				}
			}

			// Registra o encerramento de todas as tags de uma vez
			m.logger.InfoWithDetails("Tag Monitor",
				fmt.Sprintf("Encerrado gerenciamento de tags do PLC %s", plcName),
				fmt.Sprintf("Foram finalizadas %d tags monitoradas", len(tagRunners)))

			return nil

		case err := <-errChan:
			// Propaga erros críticos para encerrar o monitoramento do PLC
			errDetails := fmt.Sprintf("PLC: %s (ID: %d), Erro: %v", plcName, plcID, err)
			m.logger.ErrorWithDetails("Tag Monitor", "Erro crítico no monitoramento de tags", errDetails)
			return err

		case <-ticker.C:
			// Carrega as tags do cache em vez do banco de dados
			tagsKey := fmt.Sprintf("config:plc:%d:tags", plcID)
			tagsJSON, err := m.cache.GetValue(tagsKey)

			if err != nil {
				// Erro real ao acessar o cache - usar banco de dados como fallback
				if DetailedLogging {
					log.Printf("Erro ao acessar cache para tags do PLC %s: %v", plcName, err)
				}

				// Usamos o banco de dados apenas em caso de erro no cache
				tags, dbErr := m.db.GetPLCTags(plcID)
				if dbErr != nil {
					m.logger.ErrorWithDetails("Tag Monitor",
						"Falha ao obter tags para monitoramento",
						fmt.Sprintf("PLC: %s (ID: %d), Erro: %v", plcName, plcID, dbErr))
					continue
				}
				m.processTags(ctx, plcID, plcName, client, tags, tagRunners, errChan)
			} else if tagsJSON == "" {
				// Cache acessível mas sem dados - usar banco de dados como fallback
				if DetailedLogging {
					log.Printf("Tags do PLC %s não encontradas no cache", plcName)
				}

				tags, dbErr := m.db.GetPLCTags(plcID)
				if dbErr != nil {
					m.logger.ErrorWithDetails("Tag Monitor",
						"Falha ao obter tags para monitoramento",
						fmt.Sprintf("PLC: %s (ID: %d), Erro: %v", plcName, plcID, dbErr))
					continue
				}
				m.processTags(ctx, plcID, plcName, client, tags, tagRunners, errChan)
			} else {
				// Cache tem dados, prosseguir com a desserialização
				var tags []database.Tag
				if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
					if DetailedLogging {
						log.Printf("Erro ao deserializar tags do cache para PLC %s: %v", plcName, err)
					}

					// Fallback para banco de dados
					tags, dbErr := m.db.GetPLCTags(plcID)
					if dbErr != nil {
						m.logger.ErrorWithDetails("Tag Monitor",
							"Falha ao obter tags após erro na deserialização",
							fmt.Sprintf("PLC: %s (ID: %d), Erro: %v", plcName, plcID, dbErr))
						continue
					}
					m.processTags(ctx, plcID, plcName, client, tags, tagRunners, errChan)
				} else {
					// Verifica mudanças na quantidade de tags - logar apenas se houver alteração
					if len(tags) != lastTagCount {
						logDetails := fmt.Sprintf("Quantidade anterior: %d, Nova quantidade: %d",
							lastTagCount, len(tags))

						m.logger.InfoWithDetails("Tag Monitor",
							fmt.Sprintf("Alteração na quantidade de tags do PLC %s", plcName),
							logDetails)

						lastTagCount = len(tags)
					}

					// Processa as tags do cache
					m.processTags(ctx, plcID, plcName, client, tags, tagRunners, errChan)
				}
			}
		}
	}
}

// processTags processa a lista de tags e gerencia as goroutines de monitoramento
func (m *Manager) processTags(ctx context.Context, plcID int, plcName string, client *plclib.Client,
	tags []database.Tag, tagRunners map[int]TagRunner, errChan chan error) {

	// Filtra apenas tags ativas
	activeTags := make(map[int]database.Tag)
	for _, tag := range tags {
		if tag.Active {
			activeTags[tag.ID] = tag
		}
	}

	// Reduzindo logs desnecessários
	if DetailedLogging {
		log.Printf("Tags ativas para PLC %s: %d", plcName, len(activeTags))
	}

	// Variáveis para rastrear alterações
	tagsAdded := 0
	tagsRemoved := 0
	tagsUpdated := 0

	// Inicia ou atualiza monitoramento de tags ativas
	for tagID, tag := range activeTags {
		// Verifica se a tag tem configuração válida
		if tag.DataType == "" {
			if DetailedLogging {
				log.Printf("Aviso: Tag %s (ID: %d) tem DataType vazio, ignorando", tag.Name, tag.ID)
			}

			m.logger.WarnWithDetails("Tag Monitor",
				"Tag com configuração inválida ignorada",
				fmt.Sprintf("Tag: %s (ID: %d), Problema: DataType vazio", tag.Name, tag.ID))
			continue
		}

		// Cria configuração de monitoramento
		newConfig := TagConfig{
			ScanRate:       time.Duration(tag.ScanRate) * time.Millisecond,
			MonitorChanges: tag.MonitorChanges,
		}

		// Verifica se a taxa de scan é razoável (mínimo de 10ms)
		if newConfig.ScanRate < 10*time.Millisecond {
			if DetailedLogging {
				log.Printf("Aviso: Tag %s (ID: %d) tem ScanRate muito baixo (%v), ajustando para 10ms",
					tag.Name, tag.ID, newConfig.ScanRate)
			}

			m.logger.WarnWithDetails("Tag Monitor",
				"ScanRate ajustado para valor mínimo",
				fmt.Sprintf("Tag: %s (ID: %d), Valor original: %v, Novo valor: 10ms",
					tag.Name, tag.ID, newConfig.ScanRate))

			newConfig.ScanRate = 10 * time.Millisecond
		}

		// Verifica se já existe um runner para esta tag
		runner, exists := tagRunners[tagID]

		if !exists {
			// Cria nova goroutine para esta tag
			childCtx, cancel := context.WithCancel(ctx)
			tagRunners[tagID] = TagRunner{cancel: cancel, config: newConfig}
			go m.runTag(childCtx, plcID, plcName, tag, client, newConfig, errChan)

			// Contabiliza para log agregado
			tagsAdded++

		} else if runner.config != newConfig {
			// Configuração alterada, reinicia a goroutine
			runner.cancel()
			childCtx, cancel := context.WithCancel(ctx)
			tagRunners[tagID] = TagRunner{cancel: cancel, config: newConfig}
			go m.runTag(childCtx, plcID, plcName, tag, client, newConfig, errChan)

			// Contabiliza para log agregado
			tagsUpdated++
		}
	}

	// Encerra monitoramento de tags inativas ou removidas
	for tagID, runner := range tagRunners {
		if _, exists := activeTags[tagID]; !exists {
			runner.cancel()
			delete(tagRunners, tagID)

			// Contabiliza para log agregado
			tagsRemoved++
		}
	}

	// Registra um resumo das alterações apenas se houver mudanças
	if tagsAdded > 0 || tagsRemoved > 0 || tagsUpdated > 0 {
		details := fmt.Sprintf("Adicionadas: %d, Removidas: %d, Reconfiguradas: %d, Total ativo: %d",
			tagsAdded, tagsRemoved, tagsUpdated, len(tagRunners))

		m.logger.InfoWithDetails("Tag Monitor",
			fmt.Sprintf("Atualização nas tags monitoradas do PLC %s", plcName),
			details)
	}
}

// runTag executa a coleta contínua de uma tag
func (m *Manager) runTag(ctx context.Context, plcID int, plcName string, tag database.Tag, client *plclib.Client, config TagConfig, errChan chan error) {
	// Verificação de segurança para evitar nil pointer dereference
	if client == nil {
		errMsg := fmt.Sprintf("Cliente PLC nulo para tag %s (ID: %d) no PLC %s (ID: %d)",
			tag.Name, tag.ID, plcName, plcID)

		m.logger.ErrorWithDetails("Tag Monitor", "Falha crítica de inicialização", errMsg)
		errChan <- fmt.Errorf(errMsg)
		return
	}

	if m.cache == nil {
		errMsg := fmt.Sprintf("Cache nulo para tag %s (ID: %d) no PLC %s (ID: %d)",
			tag.Name, tag.ID, plcName, plcID)

		m.logger.ErrorWithDetails("Tag Monitor", "Falha crítica de inicialização", errMsg)
		errChan <- fmt.Errorf(errMsg)
		return
	}

	// Converter ByteOffset de float64 para int
	byteOffset := int(tag.ByteOffset)
	bitOffset := tag.BitOffset

	// Registro do início de monitoramento apenas em modo detalhado
	if DetailedLogging {
		log.Printf("Iniciando coleta para tag %s (ID: %d) no PLC %s (DB: %d, ByteOffset: %d, BitOffset: %d, Tipo: %s)",
			tag.Name, tag.ID, plcName, tag.DBNumber, byteOffset, bitOffset, tag.DataType)
	}

	// Registra o nome da tag no cache para facilitar busca por nome
	if err := m.cache.RegisterTagName(plcID, tag.ID, tag.Name); err != nil {
		if DetailedLogging {
			log.Printf("Erro ao registrar nome da tag %s no cache: %v", tag.Name, err)
		}
		// Não é um erro crítico, continua mesmo assim
	}

	ticker := time.NewTicker(config.ScanRate)
	defer ticker.Stop()

	// Para rastrear mudanças
	var lastValue interface{}
	var lastErrorMessage string
	consecutiveErrors := 0
	maxConsecutiveErrors := 5 // Após 5 erros consecutivos, reporta erro crítico

	// Adicionar mecanismo de recuo exponencial
	baseDelay := config.ScanRate
	currentDelay := baseDelay
	maxDelay := 30 * time.Second // Máximo recuo

	// Reconfigurar ticker baseado em recuo exponencial
	resetTicker := func(delay time.Duration) {
		ticker.Stop()
		ticker = time.NewTicker(delay)
	}

	for {
		select {
		case <-ctx.Done():
			if DetailedLogging {
				log.Printf("Encerrando monitoramento da tag %s (%s)", tag.Name, plcName)
			}
			return

		case <-ticker.C:
			// Verificar novamente se o cliente ainda é válido antes de cada operação
			if client == nil {
				errMsg := fmt.Sprintf("Cliente PLC tornou-se nulo durante execução para tag %s (ID: %d) no PLC %s (ID: %d)",
					tag.Name, tag.ID, plcName, plcID)

				m.logger.ErrorWithDetails("Tag Monitor", "Falha crítica durante execução", errMsg)
				errChan <- fmt.Errorf(errMsg)
				return
			}

			// Verificar conexão com ping para tags críticas ou após erros
			if consecutiveErrors > 0 {
				if err := client.Ping(); err != nil {
					// Nova falha de ping, incrementar contador sem logar cada erro
					consecutiveErrors++

					// Aplicar recuo exponencial
					if currentDelay < maxDelay {
						currentDelay = minDuration(currentDelay*2, maxDelay)
						resetTicker(currentDelay)

						if DetailedLogging {
							log.Printf("Ajustando delay para %v após erro na tag %s", currentDelay, tag.Name)
						}
					}

					// Define qualidade baixa para o valor no cache para indicar problema
					if prevValue, _ := m.cache.GetTagValue(plcID, tag.ID); prevValue != nil {
						_ = m.cache.SetTagValueWithQuality(plcID, tag.ID, prevValue.Value, 0)
					}

					// Verificar se atingimos o limite de erros consecutivos - logar apenas na mudança de estado
					if consecutiveErrors == maxConsecutiveErrors {
						errMsg := fmt.Sprintf("Tag: %s (ID: %d), PLC: %s (ID: %d), Quantidade de erros: %d",
							tag.Name, tag.ID, plcName, plcID, consecutiveErrors)

						m.logger.ErrorWithDetails("Tag Monitor",
							"Limite de erros consecutivos atingido",
							errMsg)

						errChan <- fmt.Errorf("erros consecutivos na tag %s (ID: %d): %s",
							tag.Name, tag.ID, lastErrorMessage)
						return
					}

					continue
				}

				// Se o ping teve sucesso, resetar contador de erros e delay
				if DetailedLogging {
					log.Printf("Conexão restaurada para tag %s após %d erros", tag.Name, consecutiveErrors)
				}

				if consecutiveErrors >= 3 {
					// Registrar apenas se houve várias falhas seguidas
					m.logger.InfoWithDetails("Tag Monitor",
						"Conexão restaurada após falhas consecutivas",
						fmt.Sprintf("Tag: %s (ID: %d), PLC: %s, Quantidade de erros corrigidos: %d",
							tag.Name, tag.ID, plcName, consecutiveErrors))
				}

				consecutiveErrors = 0

				if currentDelay != baseDelay {
					currentDelay = baseDelay
					resetTicker(currentDelay)
				}
			}

			// Tentar ler o valor, com tratamento especial para erros de rede
			rawValue, err := client.ReadTag(tag.DBNumber, byteOffset, tag.DataType, bitOffset)
			if err != nil {
				// Armazena a mensagem de erro para referência
				lastErrorMessage = err.Error()

				if isNetworkError(err) {
					if consecutiveErrors == 0 {
						// Registra apenas o primeiro erro de uma sequência
						m.logger.ErrorWithDetails("Tag Monitor",
							"Erro de rede na leitura de tag",
							fmt.Sprintf("Tag: %s (ID: %d), PLC: %s, Erro: %v",
								tag.Name, tag.ID, plcName, err))
					}
				} else {
					// Erros não relacionados a rede podem ser mais relevantes
					m.logger.ErrorWithDetails("Tag Monitor",
						"Erro na leitura de tag",
						fmt.Sprintf("Tag: %s (ID: %d), PLC: %s, Erro: %v",
							tag.Name, tag.ID, plcName, err))
				}

				// Incrementar contador de erros consecutivos
				consecutiveErrors++

				// Aplicar recuo exponencial
				if currentDelay < maxDelay {
					currentDelay = minDuration(currentDelay*2, maxDelay)
					resetTicker(currentDelay)
				}

				// Define qualidade baixa para o valor no cache para indicar problema
				if prevValue, _ := m.cache.GetTagValue(plcID, tag.ID); prevValue != nil {
					_ = m.cache.SetTagValueWithQuality(plcID, tag.ID, prevValue.Value, 0)
				}

				// Verificar se é um erro crítico de rede que excedeu o limite
				if isNetworkError(err) && consecutiveErrors >= maxConsecutiveErrors {
					errMsg := fmt.Sprintf("Tag: %s (ID: %d), PLC: %s (ID: %d), Erro: %v",
						tag.Name, tag.ID, plcName, plcID, err)

					m.logger.ErrorWithDetails("Tag Monitor",
						"Falha crítica após múltiplos erros de rede",
						errMsg)

					errChan <- fmt.Errorf("erro crítico de rede na tag %s: %v", tag.Name, err)
					return
				}

				continue
			}

			// Leitura bem-sucedida, resetar contador de erros e delay
			if consecutiveErrors > 0 {
				if consecutiveErrors >= 3 {
					// Registrar apenas se houve várias falhas seguidas
					m.logger.InfoWithDetails("Tag Monitor",
						"Leitura normalizada após falhas consecutivas",
						fmt.Sprintf("Tag: %s (ID: %d), PLC: %s, Falhas corrigidas: %d",
							tag.Name, tag.ID, plcName, consecutiveErrors))
				}

				consecutiveErrors = 0

				if currentDelay != baseDelay {
					currentDelay = baseDelay
					resetTicker(currentDelay)
				}
			}

			// Verificação para evitar armazenar tagID como valor
			if intValue, ok := rawValue.(int); ok && intValue == tag.ID {
				errMsg := fmt.Sprintf("Tag %s (ID: %d) retornou valor igual ao seu ID (%d). Possível erro de leitura!",
					tag.Name, tag.ID, tag.ID)

				m.logger.ErrorWithDetails("Tag Monitor",
					"Valor suspeito detectado",
					errMsg)

				continue
			}

			// Verifica se o valor mudou
			valueChanged := !plclib.CompareValues(lastValue, rawValue)

			// Se configurado para monitorar apenas mudanças e o valor não mudou, não armazena
			if config.MonitorChanges {
				oldValue, err := m.cache.GetTagValue(plcID, tag.ID)
				// Verificar explicitamente se oldValue não é nil antes de comparar
				if err == nil && oldValue != nil && plclib.CompareValues(oldValue.Value, rawValue) {
					continue
				}
			}

			// Processar o valor de acordo com o tipo de dados
			switch tag.DataType {
			case "bool":
				// Garante que valores booleanos sejam tratados corretamente
				if boolVal, ok := rawValue.(bool); ok {
					if err := m.cache.SetTagValue(plcID, tag.ID, boolVal); err != nil {
						if DetailedLogging {
							log.Printf("Erro ao atualizar cache para tag %s no PLC %s: %v", tag.Name, plcName, err)
						}
					} else if valueChanged {
						// Log apenas quando o valor muda
						if DetailedLogging {
							log.Printf("%s - Tag %s (bool) atualizada: %v", plcName, tag.Name, boolVal)
						}

						// Publica apenas quando há mudança
						_ = m.cache.PublishTagUpdate(plcID, tag.ID, boolVal)

						// Registra mudanças significativas mas não todas
						if tag.Name != "" && strings.Contains(strings.ToLower(tag.Name), "status") {
							m.logger.InfoWithDetails("Tag Monitor",
								fmt.Sprintf("Alteração em tag de status: %s", tag.Name),
								fmt.Sprintf("PLC: %s, Novo valor: %v", plcName, boolVal))
						}
					}
				} else {
					errMsg := fmt.Sprintf("Tag: %s (ID: %d), Tipo esperado: bool, Tipo recebido: %T",
						tag.Name, tag.ID, rawValue)

					m.logger.ErrorWithDetails("Tag Monitor",
						"Tipo incorreto recebido para tag booleana",
						errMsg)
				}

			case "int", "word", "dint":
				// Normaliza valores inteiros
				var intValue int64
				switch v := rawValue.(type) {
				case int:
					intValue = int64(v)
				case int8:
					intValue = int64(v)
				case int16:
					intValue = int64(v)
				case int32:
					intValue = int64(v)
				case int64:
					intValue = v
				case uint8:
					intValue = int64(v)
				case uint16:
					intValue = int64(v)
				case uint32:
					intValue = int64(v)
				case uint64:
					intValue = int64(v)
				default:
					errMsg := fmt.Sprintf("Tag: %s (ID: %d), Tipo esperado: inteiro, Tipo recebido: %T",
						tag.Name, tag.ID, rawValue)

					m.logger.ErrorWithDetails("Tag Monitor",
						"Tipo incorreto recebido para tag inteira",
						errMsg)

					continue
				}

				if err := m.cache.SetTagValue(plcID, tag.ID, intValue); err != nil {
					if DetailedLogging {
						log.Printf("Erro ao atualizar cache para tag %s no PLC %s: %v", tag.Name, plcName, err)
					}
				} else if valueChanged {
					// Log apenas quando o valor muda
					if DetailedLogging {
						log.Printf("%s - Tag %s (%s) atualizada: %v", plcName, tag.Name, tag.DataType, intValue)
					}

					// Publica apenas quando há mudança
					_ = m.cache.PublishTagUpdate(plcID, tag.ID, intValue)
				}

			case "real":
				// Normaliza valores reais (ponto flutuante)
				var floatValue float64
				switch v := rawValue.(type) {
				case float32:
					floatValue = float64(v)
				case float64:
					floatValue = v
				case int:
					floatValue = float64(v)
				case int32:
					floatValue = float64(v)
				case int64:
					floatValue = float64(v)
				default:
					errMsg := fmt.Sprintf("Tag: %s (ID: %d), Tipo esperado: real, Tipo recebido: %T",
						tag.Name, tag.ID, rawValue)

					m.logger.ErrorWithDetails("Tag Monitor",
						"Tipo incorreto recebido para tag real",
						errMsg)

					continue
				}

				if err := m.cache.SetTagValue(plcID, tag.ID, floatValue); err != nil {
					if DetailedLogging {
						log.Printf("Erro ao atualizar cache para tag %s no PLC %s: %v", tag.Name, plcName, err)
					}
				} else if valueChanged {
					// Log apenas quando o valor muda
					if DetailedLogging {
						log.Printf("%s - Tag %s (real) atualizada: %v", plcName, tag.Name, floatValue)
					}

					// Publica apenas quando há mudança
					_ = m.cache.PublishTagUpdate(plcID, tag.ID, floatValue)
				}

			case "string":
				// Garantir que strings sejam tratadas corretamente
				var strValue string
				switch v := rawValue.(type) {
				case string:
					strValue = v
				case []byte:
					strValue = string(v)
				default:
					// Tenta converter para string, mesmo que não seja do tipo ideal
					strValue = fmt.Sprintf("%v", v)

					warnMsg := fmt.Sprintf("Tag: %s (ID: %d), Tipo esperado: string, Tipo recebido: %T",
						tag.Name, tag.ID, rawValue)

					m.logger.WarnWithDetails("Tag Monitor",
						"Tipo inesperado convertido para string",
						warnMsg)
				}

				if err := m.cache.SetTagValue(plcID, tag.ID, strValue); err != nil {
					if DetailedLogging {
						log.Printf("Erro ao atualizar cache para tag %s no PLC %s: %v", tag.Name, plcName, err)
					}
				} else if valueChanged {
					// Log apenas quando o valor muda
					if DetailedLogging {
						log.Printf("%s - Tag %s (string) atualizada: %v", plcName, tag.Name, strValue)
					}

					// Publica apenas quando há mudança
					_ = m.cache.PublishTagUpdate(plcID, tag.ID, strValue)
				}

			default:
				// Caso para outros tipos de dados ou não reconhecidos
				if err := m.cache.SetTagValue(plcID, tag.ID, rawValue); err != nil {
					if DetailedLogging {
						log.Printf("Erro ao atualizar cache para tag %s no PLC %s: %v", tag.Name, plcName, err)
					}
				} else if valueChanged {
					// Log apenas quando o valor muda
					if DetailedLogging {
						log.Printf("%s - Tag %s (%s) atualizada: %v", plcName, tag.Name, tag.DataType, rawValue)
					}

					// Publica apenas quando há mudança
					_ = m.cache.PublishTagUpdate(plcID, tag.ID, rawValue)
				}
			}

			// Se o valor mudou, atualiza a última referência
			if valueChanged {
				lastValue = rawValue
			}
		}
	}
}

// GetTagValue obtém o valor atual de uma tag pelo nome
func (m *Manager) GetTagValue(plcID int, tagName string) (interface{}, error) {
	// Tenta primeiro buscar do cache pelo nome
	value, err := m.cache.GetTagValueByName(plcID, tagName)
	if err == nil && value != nil {
		return value.Value, nil
	}

	// Se não encontrou no cache, busca a tag no banco e tenta ler diretamente do PLC
	tag, err := m.db.GetTagByName(plcID, tagName)
	if err != nil {
		return nil, fmt.Errorf("tag '%s' não encontrada: %w", tagName, err)
	}

	// Busca o PLC para obter dados de conexão
	plcConfig, err := m.db.GetPLCByID(plcID)
	if err != nil {
		return nil, fmt.Errorf("PLC ID %d não encontrado: %w", plcID, err)
	}

	// Verifica se o PLC está online
	if plcConfig.Status != "online" {
		return nil, fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	}

	// Tenta usar o getOrCreatePLCClient para obter uma conexão
	client, err := getOrCreatePLCClient(m, plcID)
	if err != nil {
		return nil, fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, err)
	}
	defer client.Close()

	// Converter ByteOffset para inteiro
	byteOffset := int(tag.ByteOffset)
	bitOffset := tag.BitOffset

	// Ler o valor do PLC
	rawValue, err := client.ReadTag(tag.DBNumber, byteOffset, tag.DataType, bitOffset)
	if err != nil {
		m.logger.ErrorWithDetails("Tag Monitor",
			fmt.Sprintf("Erro ao ler tag %s diretamente do PLC", tagName),
			fmt.Sprintf("PLC: %s (ID: %d), Tag ID: %d, Erro: %v",
				plcConfig.Name, plcID, tag.ID, err))

		return nil, fmt.Errorf("erro ao ler a tag %s do PLC: %w", tagName, err)
	}

	// Atualiza o cache
	if err := m.cache.SetTagValue(plcID, tag.ID, rawValue); err != nil {
		if DetailedLogging {
			log.Printf("Aviso: Não foi possível atualizar o cache: %v", err)
		}
		// Não falha se apenas o cache falhar
	}

	// Registra o nome da tag no cache para buscas futuras
	_ = m.cache.RegisterTagName(plcID, tag.ID, tagName)

	return rawValue, nil
}

// GetAllPLCTags retorna todos os valores atuais das tags de um PLC
func (m *Manager) GetAllPLCTags(plcID int) (map[string]interface{}, error) {
	// Busca todos os valores do cache
	tagValues, err := m.cache.GetAllPLCTags(plcID)
	if err != nil {
		m.logger.ErrorWithDetails("Tag Monitor",
			"Erro ao obter valores de todas as tags",
			fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))

		return nil, fmt.Errorf("erro ao buscar valores das tags: %w", err)
	}

	// Busca todas as tags do PLC para vincular IDs aos nomes
	tagsJSON, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d:tags", plcID))
	var tags []database.Tag

	if err != nil || tagsJSON == "" {
		// Fallback para o banco de dados
		tags, err = m.db.GetPLCTags(plcID)
		if err != nil {
			m.logger.ErrorWithDetails("Tag Monitor",
				"Erro ao obter metadados das tags",
				fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))

			return nil, fmt.Errorf("erro ao buscar tags do PLC: %w", err)
		}
	} else {
		// Deserializa as tags do cache
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			// Fallback para o banco de dados
			tags, err = m.db.GetPLCTags(plcID)
			if err != nil {
				m.logger.ErrorWithDetails("Tag Monitor",
					"Erro ao deserializar e obter metadados das tags",
					fmt.Sprintf("PLC ID: %d, Erro de deserialização: %v, Erro de banco: %v",
						plcID, err, err))

				return nil, fmt.Errorf("erro ao buscar tags do PLC: %w", err)
			}
		}
	}

	// Mapeia IDs para nomes de tags
	idToName := make(map[int]string)
	for _, tag := range tags {
		idToName[tag.ID] = tag.Name
	}

	// Constrói o mapa com nomes das tags e valores
	result := make(map[string]interface{})
	var valuesFixed int

	for tagID, tagValue := range tagValues {
		if name, ok := idToName[tagID]; ok {
			// Verifica se não está retornando o ID como valor
			if intValue, ok := tagValue.Value.(int); ok && intValue == tagID {
				if DetailedLogging {
					log.Printf("ALERTA: Tag %s (ID: %d) tem valor armazenado igual ao seu ID!",
						name, tagID)
				}

				m.logger.WarnWithDetails("Tag Monitor",
					"Valor suspeito detectado",
					fmt.Sprintf("Tag: %s (ID: %d) tem valor igual ao seu ID", name, tagID))

				// Tenta reler a tag diretamente do PLC para corrigir o valor
				tag, tagErr := m.db.GetTagByID(tagID)
				if tagErr == nil {
					// Tenta ler diretamente do PLC para corrigir
					if client, err := getOrCreatePLCClient(m, plcID); err == nil && client != nil {
						defer client.Close()

						// Converter ByteOffset para inteiro e passar BitOffset
						byteOffset := int(tag.ByteOffset)
						correctedValue, readErr := client.ReadTag(tag.DBNumber, byteOffset, tag.DataType, tag.BitOffset)

						if readErr == nil {
							if DetailedLogging {
								log.Printf("Corrigindo valor da tag %s: leitura direta resultou em %v",
									name, correctedValue)
							}

							m.logger.InfoWithDetails("Tag Monitor",
								"Valor suspeito corrigido com leitura direta",
								fmt.Sprintf("Tag: %s (ID: %d), Novo valor: %v", name, tagID, correctedValue))

							// Atualiza no Redis
							_ = m.cache.SetTagValue(plcID, tagID, correctedValue)

							// Use o valor corrigido
							result[name] = correctedValue
							valuesFixed++
							continue
						}
					}
				}

				if DetailedLogging {
					log.Printf("Não foi possível corrigir o valor da tag %s, usando valor suspeito", name)
				}
			}

			// Caso normal - valor parece correto
			result[name] = tagValue.Value

			// Aproveita para registrar o nome da tag no cache se ainda não estiver
			_ = m.cache.RegisterTagName(plcID, tagID, name)
		}
	}

	// Log apenas se foram corrigidos valores
	if valuesFixed > 0 {
		m.logger.InfoWithDetails("Tag Monitor",
			"Valores suspeitos corrigidos durante leitura",
			fmt.Sprintf("PLC ID: %d, Valores corrigidos: %d de %d",
				plcID, valuesFixed, len(tagValues)))
	}

	return result, nil
}

// TestReadTag função para diagnosticar leituras diretas do PLC
func (m *Manager) TestReadTag(plcID int, tagID int) (interface{}, error) {
	// Buscar o PLC
	plcJSON, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d", plcID))
	var plcConfig *database.PLC

	if err != nil || plcJSON == "" {
		// Fallback para o banco de dados
		plcConfig, err = m.db.GetPLCByID(plcID)
		if err != nil {
			m.logger.ErrorWithDetails("Tag Monitor",
				"Erro ao obter configuração do PLC para teste",
				fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))

			return nil, fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
		}
	} else {
		// Deserializa o PLC do cache
		var plc database.PLC
		if err := json.Unmarshal([]byte(plcJSON), &plc); err != nil {
			// Fallback para o banco de dados
			plcConfig, err = m.db.GetPLCByID(plcID)
			if err != nil {
				m.logger.ErrorWithDetails("Tag Monitor",
					"Erro ao deserializar e obter configuração do PLC",
					fmt.Sprintf("PLC ID: %d, Erro: %v", plcID, err))

				return nil, fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
			}
		} else {
			plcConfig = &plc
		}
	}

	// Verificar se o PLC está online
	status, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d:status", plcID))
	if (err != nil || status == "") && plcConfig.Status != "online" {
		m.logger.WarnWithDetails("Tag Monitor",
			"Tentativa de teste de leitura em PLC offline",
			fmt.Sprintf("PLC: %s (ID: %d), Status: offline", plcConfig.Name, plcID))

		return nil, fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	} else if status != "online" && status != "" {
		m.logger.WarnWithDetails("Tag Monitor",
			"Tentativa de teste de leitura em PLC não disponível",
			fmt.Sprintf("PLC: %s (ID: %d), Status: %s", plcConfig.Name, plcID, status))

		return nil, fmt.Errorf("PLC %s (ID=%d) está %s", plcConfig.Name, plcConfig.ID, status)
	}

	// Buscar a tag
	tagJSON, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d:tag:%d", plcID, tagID))
	var tag *database.Tag

	if err != nil || tagJSON == "" {
		// Fallback para o banco de dados
		tag, err = m.db.GetTagByID(tagID)
		if err != nil {
			m.logger.ErrorWithDetails("Tag Monitor",
				"Erro ao obter configuração da tag para teste",
				fmt.Sprintf("Tag ID: %d, Erro: %v", tagID, err))

			return nil, fmt.Errorf("erro ao buscar tag ID %d: %w", tagID, err)
		}
	} else {
		// Deserializa a tag do cache
		var t database.Tag
		if err := json.Unmarshal([]byte(tagJSON), &t); err != nil {
			// Fallback para o banco de dados
			tag, err = m.db.GetTagByID(tagID)
			if err != nil {
				m.logger.ErrorWithDetails("Tag Monitor",
					"Erro ao deserializar e obter configuração da tag",
					fmt.Sprintf("Tag ID: %d, Erro: %v", tagID, err))

				return nil, fmt.Errorf("erro ao buscar tag ID %d: %w", tagID, err)
			}
		} else {
			tag = &t
		}
	}

	// Verificar se a tag pertence ao PLC correto
	if tag.PLCID != plcID {
		m.logger.WarnWithDetails("Tag Monitor",
			"Tag não pertence ao PLC especificado",
			fmt.Sprintf("Tag ID: %d, PLC solicitado: %d, PLC real: %d",
				tagID, plcID, tag.PLCID))

		return nil, fmt.Errorf("tag ID %d não pertence ao PLC ID %d", tagID, plcID)
	}

	// Usar getOrCreatePLCClient para obter uma conexão
	client, err := getOrCreatePLCClient(m, plcID)
	if err != nil {
		m.logger.ErrorWithDetails("Tag Monitor",
			"Erro ao criar conexão para teste de leitura",
			fmt.Sprintf("PLC: %s (ID: %d), Erro: %v", plcConfig.Name, plcID, err))

		return nil, fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, err)
	}
	defer client.Close()

	// Converter ByteOffset para inteiro
	byteOffset := int(tag.ByteOffset)
	bitOffset := tag.BitOffset

	// Log detalhado antes da leitura
	m.logger.InfoWithDetails("Tag Monitor",
		"Iniciando teste de leitura direta",
		fmt.Sprintf("Tag: %s (ID: %d), PLC: %s (ID: %d), DB: %d, ByteOffset: %d, BitOffset: %d, DataType: %s",
			tag.Name, tag.ID, plcConfig.Name, plcID, tag.DBNumber, byteOffset, bitOffset, tag.DataType))

	// Passar BitOffset para a função ReadTag
	rawValue, err := client.ReadTag(tag.DBNumber, byteOffset, tag.DataType, bitOffset)
	if err != nil {
		m.logger.ErrorWithDetails("Tag Monitor",
			"Erro no teste de leitura direta",
			fmt.Sprintf("Tag: %s (ID: %d), PLC: %s, Erro: %v",
				tag.Name, tag.ID, plcConfig.Name, err))

		return nil, fmt.Errorf("erro ao ler a tag %s do PLC: %w", tag.Name, err)
	}

	// Log detalhado após a leitura
	m.logger.InfoWithDetails("Tag Monitor",
		"Teste de leitura direta bem-sucedido",
		fmt.Sprintf("Tag: %s (ID: %d), PLC: %s, Valor: %v (Tipo: %T)",
			tag.Name, tag.ID, plcConfig.Name, rawValue, rawValue))

	return rawValue, nil
}

// getOrCreatePLCClient função auxiliar para criar um cliente PLC para correções
func getOrCreatePLCClient(m *Manager, plcID int) (*plclib.Client, error) {
	// Busca o PLC
	plcJSON, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d", plcID))
	var plcConfig *database.PLC

	if err != nil || plcJSON == "" {
		// Fallback para o banco de dados
		plcConfig, err = m.db.GetPLCByID(plcID)
		if err != nil {
			return nil, fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
		}
	} else {
		// Deserializa o PLC do cache
		var plc database.PLC
		if err := json.Unmarshal([]byte(plcJSON), &plc); err != nil {
			// Fallback para o banco de dados
			plcConfig, err = m.db.GetPLCByID(plcID)
			if err != nil {
				return nil, fmt.Errorf("erro ao buscar PLC ID %d: %w", plcID, err)
			}
		} else {
			plcConfig = &plc
		}
	}

	// Verifica se o PLC está online
	status, err := m.cache.GetValue(fmt.Sprintf("config:plc:%d:status", plcID))
	if (err != nil || status == "") && plcConfig.Status != "online" {
		return nil, fmt.Errorf("PLC %s (ID=%d) está offline", plcConfig.Name, plcConfig.ID)
	} else if status != "online" && status != "" {
		return nil, fmt.Errorf("PLC %s (ID=%d) está %s", plcConfig.Name, plcConfig.ID, status)
	}

	// Usa o pool para obter uma conexão
	var client *plclib.Client
	var connectErr error

	if m.plcPool != nil {
		if plcConfig.UseVLAN && plcConfig.Gateway != "" {
			config := plclib.ClientConfig{
				IPAddress:  plcConfig.IPAddress,
				Rack:       plcConfig.Rack,
				Slot:       plcConfig.Slot,
				Timeout:    5 * time.Second,
				UseVLAN:    true,
				Gateway:    plcConfig.Gateway,
				SubnetMask: plcConfig.SubnetMask,
				VLANID:     plcConfig.VLANID,
			}
			client, connectErr = m.plcPool.GetConnectionWithConfig(config)
		} else {
			client, connectErr = m.plcPool.GetConnection(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
		}
	} else {
		// Fallback para criação direta
		if plcConfig.UseVLAN && plcConfig.Gateway != "" {
			config := plclib.ClientConfig{
				IPAddress:  plcConfig.IPAddress,
				Rack:       plcConfig.Rack,
				Slot:       plcConfig.Slot,
				Timeout:    5 * time.Second,
				UseVLAN:    true,
				Gateway:    plcConfig.Gateway,
				SubnetMask: plcConfig.SubnetMask,
				VLANID:     plcConfig.VLANID,
			}
			client, connectErr = plclib.NewClientWithConfig(config)
		} else {
			client, connectErr = plclib.NewClient(plcConfig.IPAddress, plcConfig.Rack, plcConfig.Slot)
		}
	}

	if connectErr != nil {
		return nil, fmt.Errorf("erro ao conectar ao PLC %s: %w", plcConfig.Name, connectErr)
	}

	return client, nil
}
