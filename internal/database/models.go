// models.go

package database

import (
	"database/sql"
	"time"
)

// PLC representa um controlador lógico programável
type PLC struct {
	ID          int       `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	IPAddress   string    `db:"ip_address" json:"ip_address"`
	Rack        int       `db:"rack" json:"rack"`
	Slot        int       `db:"slot" json:"slot"`
	Description string    `db:"description" json:"description"`
	Active      bool      `db:"active" json:"active"`
	UseVLAN     bool      `db:"use_vlan" json:"use_vlan"`
	Gateway     string    `db:"gateway" json:"gateway"`
	SubnetMask  string    `db:"subnet_mask" json:"subnet_mask"`
	VLANID      int       `db:"vlan_id" json:"vlan_id"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
	Status      string    `json:"status"`    // Campo virtual, preenchido após consulta
	TagCount    int       `json:"tag_count"` // Campo virtual para contagem de tags
}

// Tag representa uma tag de dados do PLC
type Tag struct {
	ID             int            `db:"id" json:"id"`
	PLCID          int            `db:"plc_id" json:"plc_id"`
	Name           string         `db:"name" json:"name"`
	Description    sql.NullString `db:"description" json:"description"`
	DBNumber       int            `db:"db_number" json:"db_number"`
	ByteOffset     float64        `db:"byte_offset" json:"byte_offset"`
	BitOffset      int            `db:"bit_offset" json:"bit_offset"`
	DataType       string         `db:"data_type" json:"data_type"`
	ScanRate       int            `db:"scan_rate" json:"scan_rate"`
	Active         bool           `db:"active" json:"active"`
	MonitorChanges bool           `db:"monitor_changes" json:"monitor_changes"`
	CanWrite       bool           `db:"can_write" json:"can_write"`
	AddressFormat  string         `db:"address_format" json:"address_format"`
	CreatedAt      time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at" json:"updated_at"`
}

// PLCStatus representa o status de conexão de um PLC
type PLCStatus struct {
	PLCID      int       `db:"plc_id"`
	Status     string    `db:"status"`
	LastUpdate time.Time `db:"last_update"`
}

// TagHistory representa um registro histórico de valor de tag
type TagHistory struct {
	Time    time.Time   `db:"time"`
	PLCID   int         `db:"plc_id"`
	TagID   int         `db:"tag_id"`
	Value   interface{} `db:"value"`
	Quality int         `db:"quality"`
}

// User representa um usuário do sistema
type User struct {
	ID           int       `db:"id" json:"id"`
	Username     string    `db:"username" json:"username"`
	PasswordHash string    `db:"password_hash" json:"-"` // Não expor em JSON
	Email        string    `db:"email" json:"email"`
	FullName     string    `db:"full_name" json:"full_name"`
	Role         string    `db:"role" json:"role"`
	Active       bool      `db:"active" json:"active"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

// UserPermission representa permissões de um usuário para um PLC
type UserPermission struct {
	UserID    int  `db:"user_id" json:"user_id"`
	PLCID     int  `db:"plc_id" json:"plc_id"`
	CanRead   bool `db:"can_read" json:"can_read"`
	CanWrite  bool `db:"can_write" json:"can_write"`
	CanConfig bool `db:"can_config" json:"can_config"`
}

// SystemLog representa um registro de log do sistema
type SystemLog struct {
	ID        int       `db:"id" json:"id"`
	Level     string    `db:"level" json:"level"`
	Source    string    `db:"source" json:"source"`
	Message   string    `db:"message" json:"message"`
	Details   string    `db:"details" json:"details"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// NetworkConfig representa uma configuração de rede
type NetworkConfig struct {
	ID          int       `db:"id" json:"id"`
	Name        string    `db:"name" json:"name"`
	Type        string    `db:"type" json:"type"`
	Subnet      string    `db:"subnet" json:"subnet"`
	Gateway     string    `db:"gateway" json:"gateway"`
	VLANID      int       `db:"vlan_id" json:"vlan_id"`
	DNSServers  string    `db:"dns_servers" json:"dns_servers"`
	Description string    `db:"description" json:"description"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// SystemConfig representa uma configuração do sistema
type SystemConfig struct {
	Key         string    `db:"key" json:"key"`
	Value       string    `db:"value" json:"value"`
	Description string    `db:"description" json:"description"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// LogLevel define os níveis de log
type LogLevel string

const (
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
	LogDebug LogLevel = "debug"
)
