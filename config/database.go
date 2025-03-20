package config

import (
	"Projeto_PLC/internal/database"
	"fmt"
)

// DatabaseConfig contém todas as configurações de banco de dados
type DatabaseConfig struct {
	PostgreSQL  database.DBConfig
	PLCConfig   database.DBConfig
	TimescaleDB database.DBConfig
	Redis       struct {
		Host     string
		Port     int
		Password string
		DB       int
	}
}

// LoadDatabaseConfig carrega as configurações de banco de dados
func LoadDatabaseConfig() *DatabaseConfig {
	return &DatabaseConfig{
		PostgreSQL: database.DBConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "danilo",
			Password: "Danilo@34333528",
			Database: "permanent_data",
			SSLMode:  "disable",
		},
		PLCConfig: database.DBConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "danilo",
			Password: "Danilo@34333528",
			Database: "plc_config",
			SSLMode:  "disable",
		},
		TimescaleDB: database.DBConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "danilo",
			Password: "Danilo@34333528",
			Database: "timeseries_data",
			SSLMode:  "disable",
		},
		Redis: struct {
			Host     string
			Port     int
			Password string
			DB       int
		}{
			Host:     "localhost",
			Port:     6379,
			Password: "",
			DB:       0,
		},
	}
}

// GetPostgreSQLConnectionString retorna a string de conexão formatada para o PostgreSQL
func (c *DatabaseConfig) GetPostgreSQLConnectionString() string {
	return formatConnectionString(c.PostgreSQL)
}

// GetPLCConfigConnectionString retorna a string de conexão formatada para o banco de PLC
func (c *DatabaseConfig) GetPLCConfigConnectionString() string {
	return formatConnectionString(c.PLCConfig)
}

// GetTimescaleDBConnectionString retorna a string de conexão formatada para o TimescaleDB
func (c *DatabaseConfig) GetTimescaleDBConnectionString() string {
	return formatConnectionString(c.TimescaleDB)
}

// Função auxiliar para formatar string de conexão PostgreSQL
func formatConnectionString(config database.DBConfig) string {
	return "host=" + config.Host +
		" port=" + fmt.Sprintf("%d", config.Port) +
		" user=" + config.User +
		" password=" + config.Password +
		" dbname=" + config.Database +
		" sslmode=" + config.SSLMode
}
