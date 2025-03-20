// internal/plc/client.go
package plc

import (
	"fmt"
	"net"
	"time"

	"github.com/robinson/gos7"
)

// Client encapsula a conexão com o PLC
type Client struct {
	client  gos7.Client
	handler *gos7.TCPClientHandler
	gateway string
	useVLAN bool
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

// NewClient cria uma nova instância do cliente PLC
func NewClient(ip string, rack, slot int) (*Client, error) {
	handler := gos7.NewTCPClientHandler(ip, rack, slot)
	handler.Timeout = 10 * time.Second

	if err := handler.Connect(); err != nil {
		return nil, err
	}

	return &Client{
		client:  gos7.NewClient(handler),
		handler: handler,
	}, nil
}

// NewClientWithConfig cria um cliente com configurações avançadas
func NewClientWithConfig(config ClientConfig) (*Client, error) {
	handler := gos7.NewTCPClientHandler(config.IPAddress, config.Rack, config.Slot)

	// Configurar timeout
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	handler.Timeout = config.Timeout

	// Tentar estabelecer conexão
	if err := handler.Connect(); err != nil {
		return nil, fmt.Errorf("falha ao conectar ao PLC: %w", err)
	}

	return &Client{
		client:  gos7.NewClient(handler),
		handler: handler,
		gateway: config.Gateway,
		useVLAN: config.UseVLAN,
	}, nil
}

// Close fecha a conexão com o PLC
func (c *Client) Close() {
	if c.handler != nil {
		c.handler.Close()
	}
}

// Ping testa a conectividade com o PLC
func (c *Client) Ping() error {
	address := c.handler.Address
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = fmt.Sprintf("%s:102", address)
	}
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return fmt.Errorf("falha no ping TCP: %w", err)
	}
	conn.Close()
	return nil
}
