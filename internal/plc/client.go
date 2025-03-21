package plc

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/robinson/gos7"
)

// Client encapsula a conexão com o PLC e adiciona funcionalidade de reconexão
type Client struct {
	client       gos7.Client
	handler      *gos7.TCPClientHandler
	gateway      string
	useVLAN      bool
	config       ClientConfig // Armazena a configuração para reconexão
	mu           sync.Mutex   // Protege o acesso concorrente ao cliente
	lastConnErr  error        // Último erro de conexão
	lastConnTime time.Time    // Momento da última conexão bem-sucedida
	isConnected  bool         // Estado atual da conexão
}

// ClientConfig representa a configuração de conexão do PLC
type ClientConfig struct {
	IPAddress  string
	Rack       int
	Slot       int
	Timeout    time.Duration
	UseVLAN    bool
	Gateway    string
	SubnetMask string
	VLANID     int
}

// NewClient cria uma nova instância do cliente PLC com suporte a reconexão
func NewClient(ip string, rack, slot int) (*Client, error) {
	config := ClientConfig{
		IPAddress: ip,
		Rack:      rack,
		Slot:      slot,
		Timeout:   10 * time.Second,
	}
	return NewClientWithConfig(config)
}

// NewClientWithConfig cria um cliente com configurações avançadas e suporte a reconexão
func NewClientWithConfig(config ClientConfig) (*Client, error) {
	client := &Client{
		config:      config,
		isConnected: false,
	}

	// Configura o timeout padrão se não especificado
	if config.Timeout == 0 {
		client.config.Timeout = 10 * time.Second
	}

	// Tenta conectar inicialmente
	err := client.connect()
	if err != nil {
		return client, fmt.Errorf("falha ao conectar ao PLC: %w", err)
	}

	return client, nil
}

// connect estabelece a conexão com o PLC
func (c *Client) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Se já tiver um handler, fecha antes de criar um novo
	if c.handler != nil {
		c.handler.Close()
		c.handler = nil
		c.client = nil
	}

	// Criar novo handler
	handler := gos7.NewTCPClientHandler(c.config.IPAddress, c.config.Rack, c.config.Slot)
	handler.Timeout = c.config.Timeout

	// Tentar estabelecer conexão
	if err := handler.Connect(); err != nil {
		c.lastConnErr = err
		c.isConnected = false
		return fmt.Errorf("falha ao conectar ao PLC: %w", err)
	}

	// Atualiza o estado da conexão
	c.handler = handler
	c.client = gos7.NewClient(handler)
	c.lastConnTime = time.Now()
	c.isConnected = true
	c.lastConnErr = nil

	// Armazena outras configurações relevantes
	c.gateway = c.config.Gateway
	c.useVLAN = c.config.UseVLAN

	return nil
}

// Reconnect força uma reconexão com o PLC
func (c *Client) Reconnect() error {
	log.Printf("Forçando reconexão ao PLC %s", c.config.IPAddress)
	return c.connect()
}

// ensureConnected garante que a conexão esteja ativa antes de qualquer operação
func (c *Client) ensureConnected() error {
	// Primeiro uma verificação simples sem lock
	if c.isConnected && c.handler != nil && c.client != nil {
		return nil
	}

	// Se a conexão não está OK, tenta reconectar
	return c.Reconnect()
}

// Close fecha a conexão com o PLC
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.handler != nil {
		c.handler.Close()
		c.handler = nil
		c.client = nil
		c.isConnected = false
	}
}

// Ping testa a conectividade com o PLC
func (c *Client) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Se não temos um handler, a conexão já está inativa
	if c.handler == nil {
		return fmt.Errorf("conexão não inicializada")
	}

	// Teste 1: Verificar se o socket TCP ainda está vivo
	address := c.handler.Address
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = fmt.Sprintf("%s:102", address)
	}

	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		c.isConnected = false
		return fmt.Errorf("falha no ping TCP: %w", err)
	}
	conn.Close()

	// Se chegou aqui, a conexão parece OK
	c.isConnected = true
	return nil
}

// IsConnected retorna o estado atual da conexão
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isConnected
}

// GetConfig retorna a configuração atual do cliente
func (c *Client) GetConfig() ClientConfig {
	return c.config
}

// ReadTag lê um valor do PLC usando DBNumber, ByteOffset, e BitOffset opcional (para bool)
func (c *Client) ReadTag(dbNumber int, byteOffset int, dataType string, bitOffset int) (interface{}, error) {
	// Garante que a conexão está ativa antes de qualquer operação
	if err := c.ensureConnected(); err != nil {
		return nil, fmt.Errorf("erro de conexão: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

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
	err := c.client.AGReadDB(dbNumber, byteOffset, size, buf)

	// Verificar se tivemos um erro de conexão
	if err != nil {
		if isNetworkError(err) {
			c.isConnected = false
			return nil, fmt.Errorf("erro ao ler dados do PLC (DB%d.%d): %w", dbNumber, byteOffset, err)
		}
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

// WriteTag escreve um valor no PLC
func (c *Client) WriteTag(dbNumber int, byteOffset float64, dataType string, value interface{}) error {
	// Garante que a conexão está ativa antes de qualquer operação
	if err := c.ensureConnected(); err != nil {
		return fmt.Errorf("erro de conexão: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

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
	err := c.client.AGWriteDB(dbNumber, byteReal, len(buf), buf)
	if err != nil && isNetworkError(err) {
		c.isConnected = false
	}
	return err
}

// isNetworkError verifica se um erro é relacionado a rede
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
