package plc

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ReadTagImpl lê um valor do PLC usando DBNumber, ByteOffset, e BitOffset opcional (para bool)
// Esta é a implementação real que será chamada pelo método ReadTag em client.go
func (c *Client) ReadTagImpl(dbNumber int, byteOffset int, dataType string, bitOffset int) (interface{}, error) {
	var size int

	// Determinar o tamanho baseado no tipo de dado
	switch dataType {
	case "real":
		size = 4
	case "dint", "int32", "dword", "uint32":
		size = 4
	case "int", "int16", "word", "uint16":
		size = 2
	case "sint", "int8", "usint", "byte", "uint8", "bool":
		size = 1
	case "string":
		size = 256
	default:
		return nil, fmt.Errorf("tipo de dado não suportado: %s", dataType)
	}

	// Ler os bytes do PLC
	buf := make([]byte, size)
	if err := c.client.AGReadDB(dbNumber, byteOffset, size, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler dados do PLC (DB%d.%d): %w", dbNumber, byteOffset, err)
	}

	// Interpretar os bytes conforme o tipo de dado
	switch dataType {
	case "real":
		return math.Float32frombits(binary.BigEndian.Uint32(buf)), nil

	case "dint", "int32":
		return int32(binary.BigEndian.Uint32(buf)), nil

	case "dword", "uint32":
		return binary.BigEndian.Uint32(buf), nil

	case "int", "int16":
		return int16(binary.BigEndian.Uint16(buf)), nil

	case "word", "uint16":
		return binary.BigEndian.Uint16(buf), nil

	case "sint", "int8":
		return int8(buf[0]), nil

	case "usint", "byte", "uint8":
		return buf[0], nil

	case "bool":
		// Usa o bitOffset explicitamente para selecionar o bit correto
		if bitOffset >= 0 && bitOffset <= 7 {
			return ((buf[0] >> uint(bitOffset)) & 0x01) == 1, nil
		}
		// Caso contrário, assume primeiro bit
		return (buf[0] & 0x01) == 1, nil

	case "string":
		strLen := int(buf[1])
		if strLen > 254 {
			strLen = 254
		}
		return string(buf[2 : 2+strLen]), nil
	}

	return nil, fmt.Errorf("tipo de dado não implementado: %s", dataType)
}
