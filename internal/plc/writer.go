package plc

import (
	"encoding/binary"
	"fmt"
	"math"
)

// WriteTag escreve um valor no PLC
func (c *Client) WriteTag(dbNumber int, byteOffset float64, dataType string, value interface{}) error {
	var buf []byte

	// Extrair byte real e bit offset da posição
	byteReal := int(math.Floor(byteOffset))
	bitPart := byteOffset - float64(byteReal)

	// Calcula bit offset se necessário
	var bitOffset int
	if bitPart > 0 {
		bitOffset = int(math.Round(bitPart * 10))
	}

	switch dataType {
	case "real":
		buf = make([]byte, 4)
		var val float32

		switch v := value.(type) {
		case float32:
			val = v
		case float64:
			val = float32(v)
		case int:
			val = float32(v)
		case int64:
			val = float32(v)
		default:
			return fmt.Errorf("valor deve ser compatível com float32, recebido: %T", value)
		}

		binary.BigEndian.PutUint32(buf, math.Float32bits(val))

	case "dint", "int32":
		buf = make([]byte, 4)
		var val int32

		switch v := value.(type) {
		case int32:
			val = v
		case int:
			val = int32(v)
		case int64:
			val = int32(v)
		case float32:
			val = int32(v)
		case float64:
			val = int32(v)
		default:
			return fmt.Errorf("valor deve ser compatível com int32, recebido: %T", value)
		}

		binary.BigEndian.PutUint32(buf, uint32(val))

	case "dword", "uint32":
		buf = make([]byte, 4)
		var val uint32

		switch v := value.(type) {
		case uint32:
			val = v
		case uint:
			val = uint32(v)
		case int:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint32")
			}
			val = uint32(v)
		case float64:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint32")
			}
			val = uint32(v)
		default:
			return fmt.Errorf("valor deve ser compatível com uint32, recebido: %T", value)
		}

		binary.BigEndian.PutUint32(buf, val)

	case "int", "int16":
		buf = make([]byte, 2)
		var val int16

		switch v := value.(type) {
		case int16:
			val = v
		case int:
			val = int16(v)
		case float32:
			val = int16(v)
		case float64:
			val = int16(v)
		default:
			return fmt.Errorf("valor deve ser compatível com int16, recebido: %T", value)
		}

		binary.BigEndian.PutUint16(buf, uint16(val))

	case "word", "uint16":
		buf = make([]byte, 2)
		var val uint16

		switch v := value.(type) {
		case uint16:
			val = v
		case int:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint16")
			}
			val = uint16(v)
		case float64:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint16")
			}
			val = uint16(v)
		default:
			return fmt.Errorf("valor deve ser compatível com uint16, recebido: %T", value)
		}

		binary.BigEndian.PutUint16(buf, val)

	case "sint", "int8":
		buf = make([]byte, 1)
		var val int8

		switch v := value.(type) {
		case int8:
			val = v
		case int:
			val = int8(v)
		case float64:
			val = int8(v)
		default:
			return fmt.Errorf("valor deve ser compatível com int8, recebido: %T", value)
		}

		buf[0] = byte(val)

	case "usint", "byte", "uint8":
		buf = make([]byte, 1)
		var val uint8

		switch v := value.(type) {
		case uint8:
			val = v
		case int:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint8")
			}
			val = uint8(v)
		case float64:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint8")
			}
			val = uint8(v)
		default:
			return fmt.Errorf("valor deve ser compatível com uint8, recebido: %T", value)
		}

		buf[0] = val

	case "bool":
		buf = make([]byte, 1)

		// Primeiro ler o byte atual para preservar os outros bits
		if err := c.client.AGReadDB(dbNumber, byteReal, 1, buf); err != nil {
			return fmt.Errorf("erro ao ler byte atual para escrita de bit: %w", err)
		}

		var val bool

		switch v := value.(type) {
		case bool:
			val = v
		case int:
			val = v != 0
		case float64:
			val = v != 0
		case string:
			val = v == "true" || v == "1" || v == "yes" || v == "sim"
		default:
			return fmt.Errorf("valor deve ser convertível para bool, recebido: %T", value)
		}

		// Se temos uma posição de bit específica
		if bitOffset >= 0 && bitOffset <= 7 {
			if val {
				buf[0] |= (1 << uint(bitOffset)) // set bit
			} else {
				buf[0] &= ^(1 << uint(bitOffset)) // clear bit
			}
		} else {
			// Caso contrário, assume o primeiro bit
			if val {
				buf[0] |= 0x01 // set bit 0
			} else {
				buf[0] &= 0xFE // clear bit 0
			}
		}

	case "string":
		var str string

		switch v := value.(type) {
		case string:
			str = v
		default:
			// Se não for string, converte para string
			str = fmt.Sprint(value)
		}

		if len(str) > 254 {
			str = str[:254]
		}

		buf = make([]byte, len(str)+2)
		buf[0] = 254 // max length
		buf[1] = byte(len(str))
		copy(buf[2:], str)

	default:
		return fmt.Errorf("tipo de dado não suportado: %s", dataType)
	}

	// Escrever os bytes no PLC
	return c.client.AGWriteDB(dbNumber, byteReal, len(buf), buf)
}
