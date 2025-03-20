// user_repository.go

package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetUserByID retorna um usuário pelo ID
func (d *DB) GetUserByID(id int) (*User, error) {
	var user User
	err := d.Get(&user, "SELECT * FROM users WHERE id = $1", id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("usuário com ID %d não encontrado", id)
		}
		return nil, fmt.Errorf("erro ao consultar usuário: %w", err)
	}

	return &user, nil
}

// GetUserByUsername retorna um usuário pelo nome de usuário
func (d *DB) GetUserByUsername(username string) (*User, error) {
	var user User
	err := d.Get(&user, "SELECT * FROM users WHERE username = $1", username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("usuário '%s' não encontrado", username)
		}
		return nil, fmt.Errorf("erro ao consultar usuário: %w", err)
	}

	return &user, nil
}

// GetUserByEmail retorna um usuário pelo email
func (d *DB) GetUserByEmail(email string) (*User, error) {
	var user User
	err := d.Get(&user, "SELECT * FROM users WHERE email = $1", email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("usuário com email '%s' não encontrado", email)
		}
		return nil, fmt.Errorf("erro ao consultar usuário: %w", err)
	}

	return &user, nil
}

// GetAllUsers retorna todos os usuários
func (d *DB) GetAllUsers() ([]User, error) {
	var users []User
	err := d.Select(&users, "SELECT * FROM users ORDER BY username")
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar usuários: %w", err)
	}

	return users, nil
}

// CreateUser insere um novo usuário
func (d *DB) CreateUser(user User) (*User, error) {
	query := `
		INSERT INTO users (
			username, password_hash, email, full_name, role, active
		) VALUES (
			$1, $2, $3, $4, $5, $6
		) RETURNING id, created_at, updated_at
	`

	row := d.QueryRow(query,
		user.Username, user.PasswordHash, user.Email,
		user.FullName, user.Role, user.Active)

	err := row.Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("erro ao inserir usuário: %w", err)
	}

	return &user, nil
}

// UpdateUser atualiza os dados de um usuário
func (d *DB) UpdateUser(user User) error {
	query := `
		UPDATE users SET 
			username = $1, 
			email = $2, 
			full_name = $3, 
			role = $4, 
			active = $5
		WHERE id = $6
	`

	_, err := d.Exec(query,
		user.Username, user.Email, user.FullName,
		user.Role, user.Active, user.ID)

	if err != nil {
		return fmt.Errorf("erro ao atualizar usuário: %w", err)
	}

	return nil
}

// UpdateUserPassword atualiza apenas a senha de um usuário
func (d *DB) UpdateUserPassword(userID int, passwordHash string) error {
	query := "UPDATE users SET password_hash = $1 WHERE id = $2"

	_, err := d.Exec(query, passwordHash, userID)
	if err != nil {
		return fmt.Errorf("erro ao atualizar senha: %w", err)
	}

	return nil
}

// DeleteUser remove um usuário
func (d *DB) DeleteUser(id int) error {
	tx, err := d.BeginTx()
	if err != nil {
		return fmt.Errorf("erro ao iniciar transação: %w", err)
	}

	// Remove permissões do usuário
	if _, err := tx.Exec("DELETE FROM user_permissions WHERE user_id = $1", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao deletar permissões do usuário: %w", err)
	}

	// Remove as sessões do usuário
	if _, err := tx.Exec("DELETE FROM user_sessions WHERE user_id = $1", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao deletar sessões do usuário: %w", err)
	}

	// Remove o usuário
	if _, err := tx.Exec("DELETE FROM users WHERE id = $1", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao deletar usuário: %w", err)
	}

	return tx.Commit()
}

// GetUserPermissions retorna as permissões de um usuário
func (d *DB) GetUserPermissions(userID int) ([]UserPermission, error) {
	var permissions []UserPermission
	err := d.Select(&permissions, "SELECT * FROM user_permissions WHERE user_id = $1", userID)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar permissões: %w", err)
	}

	return permissions, nil
}

// SetUserPermission define permissão de um usuário para um PLC
func (d *DB) SetUserPermission(perm UserPermission) error {
	query := `
		INSERT INTO user_permissions (user_id, plc_id, can_read, can_write, can_config)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, plc_id) DO UPDATE
		SET can_read = $3, can_write = $4, can_config = $5
	`

	_, err := d.Exec(query,
		perm.UserID, perm.PLCID, perm.CanRead, perm.CanWrite, perm.CanConfig)

	if err != nil {
		return fmt.Errorf("erro ao definir permissão: %w", err)
	}

	return nil
}

// RemoveUserPermission remove permissão de um usuário para um PLC
func (d *DB) RemoveUserPermission(userID, plcID int) error {
	_, err := d.Exec("DELETE FROM user_permissions WHERE user_id = $1 AND plc_id = $2",
		userID, plcID)

	if err != nil {
		return fmt.Errorf("erro ao remover permissão: %w", err)
	}

	return nil
}

// CreateUserSession cria uma nova sessão para o usuário
func (d *DB) CreateUserSession(userID int, sessionID, ipAddress, userAgent string, expiresAt time.Time) error {
	query := `
		INSERT INTO user_sessions (id, user_id, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := d.Exec(query, sessionID, userID, ipAddress, userAgent, expiresAt)
	if err != nil {
		return fmt.Errorf("erro ao criar sessão: %w", err)
	}

	return nil
}

// GetUserSession recupera uma sessão pelo ID
func (d *DB) GetUserSession(sessionID string) (int, time.Time, error) {
	var userID int
	var expiresAt time.Time

	err := d.QueryRow("SELECT user_id, expires_at FROM user_sessions WHERE id = $1", sessionID).
		Scan(&userID, &expiresAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, time.Time{}, fmt.Errorf("sessão não encontrada")
		}
		return 0, time.Time{}, fmt.Errorf("erro ao consultar sessão: %w", err)
	}

	return userID, expiresAt, nil
}

// DeleteUserSession remove uma sessão
func (d *DB) DeleteUserSession(sessionID string) error {
	_, err := d.Exec("DELETE FROM user_sessions WHERE id = $1", sessionID)
	if err != nil {
		return fmt.Errorf("erro ao deletar sessão: %w", err)
	}

	return nil
}

// CleanExpiredSessions remove todas as sessões expiradas
func (d *DB) CleanExpiredSessions() (int, error) {
	result, err := d.Exec("DELETE FROM user_sessions WHERE expires_at < NOW()")
	if err != nil {
		return 0, fmt.Errorf("erro ao limpar sessões expiradas: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("erro ao obter número de sessões removidas: %w", err)
	}

	return int(rowsAffected), nil
}
