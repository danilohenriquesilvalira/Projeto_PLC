//config_repository.go

package database

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetSystemConfig recupera uma configuração do sistema pelo identificador
func (d *DB) GetSystemConfig(key string) (*SystemConfig, error) {
	var config SystemConfig
	err := d.Get(&config, "SELECT * FROM system_config WHERE key = $1", key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("configuração '%s' não encontrada", key)
		}
		return nil, fmt.Errorf("erro ao consultar configuração: %w", err)
	}

	return &config, nil
}

// GetAllSystemConfig recupera todas as configurações do sistema
func (d *DB) GetAllSystemConfig() ([]SystemConfig, error) {
	var configs []SystemConfig
	err := d.Select(&configs, "SELECT * FROM system_config ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar configurações: %w", err)
	}

	return configs, nil
}

// SetSystemConfig define ou atualiza uma configuração do sistema
func (d *DB) SetSystemConfig(key, value, description string) error {
	query := `
		INSERT INTO system_config (key, value, description)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = $2, description = $3, updated_at = NOW()
	`

	_, err := d.Exec(query, key, value, description)
	if err != nil {
		return fmt.Errorf("erro ao definir configuração: %w", err)
	}

	return nil
}

// DeleteSystemConfig remove uma configuração do sistema
func (d *DB) DeleteSystemConfig(key string) error {
	_, err := d.Exec("DELETE FROM system_config WHERE key = $1", key)
	if err != nil {
		return fmt.Errorf("erro ao deletar configuração: %w", err)
	}

	return nil
}

// GetNetworkConfig recupera uma configuração de rede pelo ID
func (d *DB) GetNetworkConfig(id int) (*NetworkConfig, error) {
	var config NetworkConfig
	err := d.Get(&config, "SELECT * FROM network_config WHERE id = $1", id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("configuração de rede ID %d não encontrada", id)
		}
		return nil, fmt.Errorf("erro ao consultar configuração de rede: %w", err)
	}

	return &config, nil
}

// GetNetworkConfigByName recupera uma configuração de rede pelo nome
func (d *DB) GetNetworkConfigByName(name string) (*NetworkConfig, error) {
	var config NetworkConfig
	err := d.Get(&config, "SELECT * FROM network_config WHERE name = $1", name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("configuração de rede '%s' não encontrada", name)
		}
		return nil, fmt.Errorf("erro ao consultar configuração de rede: %w", err)
	}

	return &config, nil
}

// GetAllNetworkConfigs recupera todas as configurações de rede
func (d *DB) GetAllNetworkConfigs() ([]NetworkConfig, error) {
	var configs []NetworkConfig
	err := d.Select(&configs, "SELECT * FROM network_config ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar configurações de rede: %w", err)
	}

	return configs, nil
}

// CreateNetworkConfig insere uma nova configuração de rede
func (d *DB) CreateNetworkConfig(config NetworkConfig) (*NetworkConfig, error) {
	query := `
		INSERT INTO network_config (
			name, type, subnet, gateway, vlan_id, dns_servers, description
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		) RETURNING id, created_at, updated_at
	`

	row := d.QueryRow(query,
		config.Name, config.Type, config.Subnet, config.Gateway,
		config.VLANID, config.DNSServers, config.Description)

	err := row.Scan(&config.ID, &config.CreatedAt, &config.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("erro ao inserir configuração de rede: %w", err)
	}

	return &config, nil
}

// UpdateNetworkConfig atualiza uma configuração de rede
func (d *DB) UpdateNetworkConfig(config NetworkConfig) error {
	query := `
		UPDATE network_config SET 
			name = $1, 
			type = $2, 
			subnet = $3, 
			gateway = $4, 
			vlan_id = $5, 
			dns_servers = $6, 
			description = $7
		WHERE id = $8
	`

	_, err := d.Exec(query,
		config.Name, config.Type, config.Subnet, config.Gateway,
		config.VLANID, config.DNSServers, config.Description, config.ID)

	if err != nil {
		return fmt.Errorf("erro ao atualizar configuração de rede: %w", err)
	}

	return nil
}

// DeleteNetworkConfig remove uma configuração de rede
func (d *DB) DeleteNetworkConfig(id int) error {
	_, err := d.Exec("DELETE FROM network_config WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("erro ao deletar configuração de rede: %w", err)
	}

	return nil
}
