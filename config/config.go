package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
)

// AppConfig contém toda a configuração da aplicação
type AppConfig struct {
	Database      *DatabaseConfig
	PLCManager    *PLCManagerConfig
	DataCollector *DataCollectorConfig
	API           *APIConfig
	Security      *SecurityConfig
}

// PLCManagerConfig contém configurações para o gerenciador de PLCs
type PLCManagerConfig struct {
	CheckInterval     int `json:"check_interval_seconds"`
	ReconnectInterval int `json:"reconnect_interval_seconds"`
	StatusInterval    int `json:"status_interval_seconds"`
	TagReloadInterval int `json:"tag_reload_interval_seconds"`
	MinScanRateMS     int `json:"min_scan_rate_ms"`
}

// DataCollectorConfig contém configurações para o coletor de dados
type DataCollectorConfig struct {
	HistoryInterval     int  `json:"history_interval_seconds"`
	EnableDataRetention bool `json:"enable_data_retention"`
	RetentionDays       int  `json:"retention_days"`
	BatchSize           int  `json:"batch_size"`
	EnableCompression   bool `json:"enable_compression"`
	MaxWorkers          int  `json:"max_workers"`
}

// APIConfig contém configurações para a API REST
type APIConfig struct {
	Port            int    `json:"port"`
	Host            string `json:"host"`
	ReadTimeout     int    `json:"read_timeout_seconds"`
	WriteTimeout    int    `json:"write_timeout_seconds"`
	ShutdownTimeout int    `json:"shutdown_timeout_seconds"`
	EnableCORS      bool   `json:"enable_cors"`
}

// DefaultConfig retorna uma configuração padrão
func DefaultConfig() *AppConfig {
	return &AppConfig{
		Database: LoadDatabaseConfig(),
		PLCManager: &PLCManagerConfig{
			CheckInterval:     5,
			ReconnectInterval: 5,
			StatusInterval:    30,
			TagReloadInterval: 2,
			MinScanRateMS:     10,
		},
		DataCollector: &DataCollectorConfig{
			HistoryInterval:     300,
			EnableDataRetention: true,
			RetentionDays:       30,
			BatchSize:           1000,
			EnableCompression:   true,
			MaxWorkers:          5,
		},
		API: &APIConfig{
			Port:            8080,
			Host:            "0.0.0.0",
			ReadTimeout:     30,
			WriteTimeout:    30,
			ShutdownTimeout: 10,
			EnableCORS:      true,
		},
		Security: LoadSecurityConfig(),
	}
}

// LoadConfig carrega a configuração de um arquivo
func LoadConfig(configFile string) (*AppConfig, error) {
	// Usa configuração padrão
	config := DefaultConfig()

	// Se o arquivo não for especificado, retorna a configuração padrão
	if configFile == "" {
		return config, nil
	}

	// Verifica se o arquivo existe
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		// Cria o diretório se não existir
		dir := filepath.Dir(configFile)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("erro ao criar diretório de configuração: %w", err)
		}

		// Salva a configuração padrão
		if err := SaveConfig(configFile, config); err != nil {
			return nil, fmt.Errorf("erro ao salvar configuração padrão: %w", err)
		}

		log.Printf("Arquivo de configuração criado em: %s", configFile)
		return config, nil
	}

	// Lê o arquivo de configuração
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler arquivo de configuração: %w", err)
	}

	// Decodifica o JSON
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("erro ao decodificar configuração: %w", err)
	}

	return config, nil
}

// SaveConfig salva a configuração em um arquivo
func SaveConfig(configFile string, config *AppConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("erro ao codificar configuração: %w", err)
	}

	if err := ioutil.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("erro ao escrever arquivo de configuração: %w", err)
	}

	return nil
}
