package plc

import (
	"fmt"
	"sort"
)

// TagReadRequest representa uma solicitação para ler uma tag específica
type TagReadRequest struct {
	TagID      int
	DBNumber   int
	ByteOffset int
	BitOffset  int
	DataType   string
	Size       int // tamanho em bytes necessário para o tipo
}

// TagWriteRequest representa uma solicitação para escrever em uma tag
type TagWriteRequest struct {
	TagID      int
	DBNumber   int
	ByteOffset int
	BitOffset  int
	DataType   string
	Value      interface{}
}

// ReadGroup representa um grupo de leituras que podem ser agrupadas
type ReadGroup struct {
	DBNumber  int
	StartByte int
	EndByte   int
	ByteCount int
	Tags      []TagReadRequest
}

// WriteGroup representa um grupo de escritas que podem ser agrupadas
type WriteGroup struct {
	DBNumber  int
	StartByte int
	EndByte   int
	ByteCount int
	Tags      []TagWriteRequest
}

// BatchReader gerencia operações de leitura em lote
type BatchReader struct {
	client           *Client
	maxBatchSize     int // tamanho máximo de um único bloco de leitura
	minGroupDistance int // distância mínima para agrupar tags
}

// NewBatchReader cria um novo BatchReader
func NewBatchReader(client *Client, maxBatchSize int) *BatchReader {
	return &BatchReader{
		client:           client,
		maxBatchSize:     maxBatchSize,
		minGroupDistance: 10, // por padrão, agrupar tags com até 10 bytes de distância
	}
}

// determineSizeForType retorna o tamanho em bytes para um determinado tipo de dados
func determineSizeForType(dataType string) int {
	switch dataType {
	case "bool":
		return 1
	case "byte", "usint", "sint", "char", "uint8", "int8":
		return 1
	case "word", "uint", "int", "uint16", "int16":
		return 2
	case "dword", "udint", "dint", "uint32", "int32", "real", "float32":
		return 4
	case "lreal", "float64", "uint64", "int64":
		return 8
	case "string":
		return 256 // tamanho máximo para strings
	default:
		// Tipo desconhecido, assumir tamanho padrão
		return 4
	}
}

// optimizeReadGroups agrupa tags próximas para leitura mais eficiente
func (br *BatchReader) optimizeReadGroups(requests []TagReadRequest) []ReadGroup {
	if len(requests) == 0 {
		return nil
	}

	// Primeiro, organizar as solicitações por DBNumber e ByteOffset
	sort.SliceStable(requests, func(i, j int) bool {
		if requests[i].DBNumber != requests[j].DBNumber {
			return requests[i].DBNumber < requests[j].DBNumber
		}
		return requests[i].ByteOffset < requests[j].ByteOffset
	})

	// Agrupar as solicitações do mesmo DB
	dbGroups := make(map[int][]TagReadRequest)
	for _, req := range requests {
		dbGroups[req.DBNumber] = append(dbGroups[req.DBNumber], req)
	}

	var result []ReadGroup

	// Para cada DB, criar grupos otimizados
	for dbNumber, dbRequests := range dbGroups {
		var currentGroup ReadGroup
		currentGroup.DBNumber = dbNumber
		currentGroup.StartByte = -1 // Ainda não definido

		for i, req := range dbRequests {
			// Garantir que o tamanho seja definido
			if req.Size <= 0 {
				req.Size = determineSizeForType(req.DataType)
			}

			// Se for a primeira tag do grupo, inicializar o grupo
			if currentGroup.StartByte == -1 {
				currentGroup.StartByte = req.ByteOffset
				currentGroup.EndByte = req.ByteOffset + req.Size - 1
				currentGroup.Tags = append(currentGroup.Tags, req)
				continue
			}

			// Calcular o novo tamanho se incluirmos esta tag
			endByte := req.ByteOffset + req.Size - 1
			newGroupSize := endByte - currentGroup.StartByte + 1

			// Verificar se podemos adicionar esta tag ao grupo atual
			// Condições:
			// 1. A tag está próxima o suficiente das outras
			// 2. O tamanho total não excede o máximo permitido
			if (req.ByteOffset-(currentGroup.EndByte+1)) <= br.minGroupDistance &&
				newGroupSize <= br.maxBatchSize {
				// Atualizar o endpoint se necessário
				if endByte > currentGroup.EndByte {
					currentGroup.EndByte = endByte
				}
				currentGroup.Tags = append(currentGroup.Tags, req)
			} else {
				// Finalizar o grupo atual e iniciar um novo
				currentGroup.ByteCount = currentGroup.EndByte - currentGroup.StartByte + 1
				result = append(result, currentGroup)

				// Iniciar novo grupo
				currentGroup = ReadGroup{
					DBNumber:  dbNumber,
					StartByte: req.ByteOffset,
					EndByte:   endByte,
					Tags:      []TagReadRequest{req},
				}
			}

			// Se for a última tag, finalizar o grupo
			if i == len(dbRequests)-1 {
				currentGroup.ByteCount = currentGroup.EndByte - currentGroup.StartByte + 1
				result = append(result, currentGroup)
			}
		}
	}

	return result
}

// ReadMultipleTags lê várias tags em uma única operação quando possível
func (br *BatchReader) ReadMultipleTags(requests []TagReadRequest) (map[int]interface{}, error) {
	if len(requests) == 0 {
		return nil, fmt.Errorf("nenhuma tag para ler")
	}

	// Certificar-se de que todos os tamanhos estão definidos
	for i := range requests {
		if requests[i].Size <= 0 {
			requests[i].Size = determineSizeForType(requests[i].DataType)
		}
	}

	// Otimizar as leituras
	groups := br.optimizeReadGroups(requests)

	// Resultados para cada tag ID
	results := make(map[int]interface{})
	errors := make(map[int]error)

	// Processar cada grupo
	for _, group := range groups {
		// Ler o bloco de dados em uma única operação
		data := make([]byte, group.ByteCount)
		err := br.client.client.AGReadDB(group.DBNumber, group.StartByte, group.ByteCount, data)

		if err != nil {
			// Falha na leitura do bloco inteiro
			for _, tagReq := range group.Tags {
				errors[tagReq.TagID] = fmt.Errorf("falha ao ler bloco: %w", err)
			}
			continue
		}

		// Extrair os valores de cada tag do bloco lido
		for _, tagReq := range group.Tags {
			// Calcular o offset dentro do bloco
			localOffset := tagReq.ByteOffset - group.StartByte

			// Verificar se temos bytes suficientes
			if localOffset < 0 || localOffset+tagReq.Size > len(data) {
				errors[tagReq.TagID] = fmt.Errorf("offset inválido para tag ID %d", tagReq.TagID)
				continue
			}

			// Extrair e converter o valor com base no tipo
			value, err := extractValue(data[localOffset:], tagReq.DataType, tagReq.BitOffset)
			if err != nil {
				errors[tagReq.TagID] = fmt.Errorf("erro ao extrair valor da tag ID %d: %w", tagReq.TagID, err)
			} else {
				results[tagReq.TagID] = value
			}
		}
	}

	// Se todas as tags falharam, retornar erro
	if len(errors) == len(requests) && len(errors) > 0 {
		// Pegar o primeiro erro como representativo
		for _, err := range errors {
			return results, fmt.Errorf("falha ao ler todas as tags: %w", err)
		}
	}

	return results, nil
}

// extrair o valor com base no tipo de dados
func extractValue(data []byte, dataType string, bitOffset int) (interface{}, error) {
	// Reutilizar a lógica existente para extrair valores de bytes brutos
	// Este é um esboço, você deve implementar a extração real com base nos tipos

	switch dataType {
	case "bool":
		if bitOffset >= 0 && bitOffset <= 7 {
			return ((data[0] >> uint(bitOffset)) & 0x01) == 1, nil
		}
		return (data[0] & 0x01) == 1, nil

	case "byte", "usint", "uint8":
		return data[0], nil

	case "sint", "int8":
		return int8(data[0]), nil

	case "word", "uint16":
		if len(data) < 2 {
			return nil, fmt.Errorf("dados insuficientes para word/uint16")
		}
		return (uint16(data[0]) << 8) | uint16(data[1]), nil

	case "int", "int16":
		if len(data) < 2 {
			return nil, fmt.Errorf("dados insuficientes para int/int16")
		}
		val := (uint16(data[0]) << 8) | uint16(data[1])
		return int16(val), nil

	// Adicionar mais tipos conforme necessário

	default:
		return nil, fmt.Errorf("tipo de dados não suportado para extração: %s", dataType)
	}
}

// BatchWriter gerencia operações de escrita em lote
type BatchWriter struct {
	client           *Client
	maxBatchSize     int
	minGroupDistance int
}

// NewBatchWriter cria um novo BatchWriter
func NewBatchWriter(client *Client, maxBatchSize int) *BatchWriter {
	return &BatchWriter{
		client:           client,
		maxBatchSize:     maxBatchSize,
		minGroupDistance: 10,
	}
}

// WriteMultipleTags escreve várias tags em uma única operação quando possível
func (bw *BatchWriter) WriteMultipleTags(requests []TagWriteRequest) (map[int]error, error) {
	if len(requests) == 0 {
		return nil, fmt.Errorf("nenhuma tag para escrever")
	}

	// Implementação simplificada: escrever uma a uma
	// Uma implementação completa agruparia escritas próximas

	results := make(map[int]error)

	for _, req := range requests {
		// Calcula o byte offset com a parte fracional para bit offset se necessário
		byteOffset := float64(req.ByteOffset)
		if req.BitOffset > 0 {
			byteOffset = float64(req.ByteOffset) + float64(req.BitOffset)/10.0
		}

		// Use o método WriteTag diretamente do client
		err := bw.client.WriteTag(req.DBNumber, byteOffset, req.DataType, req.Value)
		results[req.TagID] = err
	}

	return results, nil
}
