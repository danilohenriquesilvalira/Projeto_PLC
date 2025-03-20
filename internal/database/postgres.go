package database

import (
	"context"
	"database/sql" // Adicionei import para sql.Result
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// DBConfig contém as configurações de conexão ao PostgreSQL
type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

// DB encapsula a conexão com o PostgreSQL
type DB struct {
	db      *sqlx.DB
	ctx     context.Context
	timeout time.Duration
}

// NewDB cria uma nova conexão com o PostgreSQL
func NewDB(config DBConfig) (*DB, error) {
	// Constrói a string de conexão
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.Database, config.SSLMode)

	// Conecta ao banco usando sqlx
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao PostgreSQL: %w", err)
	}

	// Configuração de conexão
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Define um timeout padrão de 5 segundos
	timeout := 5 * time.Second

	return &DB{
		db:      db,
		ctx:     context.Background(),
		timeout: timeout,
	}, nil
}

// Close fecha a conexão com o banco
func (d *DB) Close() error {
	return d.db.Close()
}

// WithTimeout retorna uma nova instância de DB com timeout personalizado
func (d *DB) WithTimeout(timeout time.Duration) *DB {
	return &DB{
		db:      d.db,
		ctx:     d.ctx,
		timeout: timeout,
	}
}

// BeginTx inicia uma transação no banco de dados
func (d *DB) BeginTx() (*sqlx.Tx, error) {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()

	return d.db.BeginTxx(ctx, nil)
}

// Exec executa uma query SQL que não retorna resultados
func (d *DB) Exec(query string, args ...interface{}) (result sql.Result, err error) {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()

	return d.db.ExecContext(ctx, query, args...)
}

// Query executa uma query SQL que retorna linhas de resultados
func (d *DB) Query(query string, args ...interface{}) (*sqlx.Rows, error) {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()

	return d.db.QueryxContext(ctx, query, args...)
}

// QueryRow executa uma query SQL que deve retornar no máximo uma linha
func (d *DB) QueryRow(query string, args ...interface{}) *sqlx.Row {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()

	return d.db.QueryRowxContext(ctx, query, args...)
}

// Select executa uma query e preenche a slice dest com os resultados
func (d *DB) Select(dest interface{}, query string, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()

	return d.db.SelectContext(ctx, dest, query, args...)
}

// Get executa uma query e preenche dest com o resultado
func (d *DB) Get(dest interface{}, query string, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(d.ctx, d.timeout)
	defer cancel()

	return d.db.GetContext(ctx, dest, query, args...)
}

// GetDB retorna a conexão subjacente para casos de emergência
func (d *DB) GetDB() *sqlx.DB {
	return d.db
}
