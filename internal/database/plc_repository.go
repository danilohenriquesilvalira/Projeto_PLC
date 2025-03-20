// plc_repository.go

package database

import (
	"database/sql"
	"errors"
	"fmt"
)

// GetActivePLCs retorna todos os PLCs ativos
func (d *DB) GetActivePLCs() ([]PLC, error) {
	query := `
        SELECT 
            p.id, p.name, p.ip_address, p.rack, p.slot, 
            p.description, p.active, p.created_at, p.updated_at,
            p.use_vlan, p.gateway, p.subnet_mask, p.vlan_id,
            COALESCE(s.status, 'unknown') as status
        FROM plcs p 
        LEFT JOIN plc_status s ON p.id = s.plc_id 
        WHERE p.active = true
    `

	var plcs []PLC
	rows, err := d.Query(query)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar PLCs ativos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var plc PLC
		var status sql.NullString
		var gateway sql.NullString
		var subnetMask sql.NullString
		var vlanID sql.NullInt64

		// Scan all fields individually
		err := rows.Scan(
			&plc.ID, &plc.Name, &plc.IPAddress, &plc.Rack, &plc.Slot,
			&plc.Description, &plc.Active, &plc.CreatedAt, &plc.UpdatedAt,
			&plc.UseVLAN, &gateway, &subnetMask, &vlanID,
			&status)

		if err != nil {
			return nil, fmt.Errorf("erro ao ler dados do PLC: %w", err)
		}

		// Tratar campos que podem ser NULL
		if gateway.Valid {
			plc.Gateway = gateway.String
		}

		if subnetMask.Valid {
			plc.SubnetMask = subnetMask.String
		}

		if vlanID.Valid {
			plc.VLANID = int(vlanID.Int64)
		}

		if status.Valid {
			plc.Status = status.String
		} else {
			plc.Status = "unknown"
		}

		plcs = append(plcs, plc)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("erro durante iteração de PLCs: %w", err)
	}

	return plcs, nil
}

// GetPLCByID retorna um PLC específico pelo ID
func (d *DB) GetPLCByID(id int) (*PLC, error) {
	query := `
		SELECT 
			p.id, p.name, p.ip_address, p.rack, p.slot, 
			p.description, p.active, p.created_at, p.updated_at,
			p.use_vlan, p.gateway, p.subnet_mask, p.vlan_id,
			COALESCE(s.status, 'unknown') as status
		FROM plcs p 
		LEFT JOIN plc_status s ON p.id = s.plc_id 
		WHERE p.id = $1
	`

	var plc PLC
	var status sql.NullString
	var gateway sql.NullString
	var subnetMask sql.NullString
	var vlanID sql.NullInt64

	row := d.QueryRow(query, id)

	// Scan each field individually instead of using StructScan
	err := row.Scan(
		&plc.ID, &plc.Name, &plc.IPAddress, &plc.Rack, &plc.Slot,
		&plc.Description, &plc.Active, &plc.CreatedAt, &plc.UpdatedAt,
		&plc.UseVLAN, &gateway, &subnetMask, &vlanID,
		&status)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("PLC com ID %d não encontrado", id)
		}
		return nil, fmt.Errorf("erro ao consultar PLC: %w", err)
	}

	// Tratar campos que podem ser NULL
	if gateway.Valid {
		plc.Gateway = gateway.String
	}

	if subnetMask.Valid {
		plc.SubnetMask = subnetMask.String
	}

	if vlanID.Valid {
		plc.VLANID = int(vlanID.Int64)
	}

	if status.Valid {
		plc.Status = status.String
	} else {
		plc.Status = "unknown"
	}

	// Busca a contagem de tags
	tagCountQuery := `SELECT COUNT(*) FROM tags WHERE plc_id = $1 AND active = true`
	var count int
	if err := d.Get(&count, tagCountQuery, plc.ID); err == nil {
		plc.TagCount = count
	}

	return &plc, nil
}

// CreatePLC insere um novo PLC
func (d *DB) CreatePLC(plc PLC) (*PLC, error) {
	// Verificar se VLAN está sendo usada e se os campos necessários estão presentes
	if plc.UseVLAN {
		if plc.Gateway == "" || plc.SubnetMask == "" {
			return nil, fmt.Errorf("VLAN ativada, mas gateway ou máscara de sub-rede não foram fornecidos")
		}
	}

	query := `
		INSERT INTO plcs (
			name, ip_address, rack, slot, description, active, 
			use_vlan, gateway, subnet_mask, vlan_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		) RETURNING id, created_at, updated_at
	`

	row := d.QueryRow(query,
		plc.Name, plc.IPAddress, plc.Rack, plc.Slot, plc.Description, plc.Active,
		plc.UseVLAN,
		sql.NullString{String: plc.Gateway, Valid: plc.Gateway != ""},
		sql.NullString{String: plc.SubnetMask, Valid: plc.SubnetMask != ""},
		sql.NullInt64{Int64: int64(plc.VLANID), Valid: plc.VLANID != 0})

	err := row.Scan(&plc.ID, &plc.CreatedAt, &plc.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("erro ao inserir PLC: %w", err)
	}

	return &plc, nil
}

// UpdatePLC atualiza os dados de um PLC
func (d *DB) UpdatePLC(plc PLC) error {
	// Verificar se VLAN está sendo usada e se os campos necessários estão presentes
	if plc.UseVLAN {
		if plc.Gateway == "" || plc.SubnetMask == "" {
			return fmt.Errorf("VLAN ativada, mas gateway ou máscara de sub-rede não foram fornecidos")
		}
	}

	query := `
		UPDATE plcs SET 
			name = $1, 
			ip_address = $2, 
			rack = $3, 
			slot = $4, 
			description = $5, 
			active = $6,
			use_vlan = $7,
			gateway = $8,
			subnet_mask = $9,
			vlan_id = $10
		WHERE id = $11
	`

	_, err := d.Exec(query,
		plc.Name, plc.IPAddress, plc.Rack, plc.Slot, plc.Description, plc.Active,
		plc.UseVLAN,
		sql.NullString{String: plc.Gateway, Valid: plc.Gateway != ""},
		sql.NullString{String: plc.SubnetMask, Valid: plc.SubnetMask != ""},
		sql.NullInt64{Int64: int64(plc.VLANID), Valid: plc.VLANID != 0},
		plc.ID)

	if err != nil {
		return fmt.Errorf("erro ao atualizar PLC: %w", err)
	}

	return nil
}

// DeletePLC remove um PLC e suas tags associadas
func (d *DB) DeletePLC(id int) error {
	tx, err := d.BeginTx()
	if err != nil {
		return fmt.Errorf("erro ao iniciar transação: %w", err)
	}

	// Deleta as tags associadas ao PLC
	if _, err := tx.Exec("DELETE FROM tags WHERE plc_id = $1", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao deletar tags associadas: %w", err)
	}

	// Remove o status do PLC
	if _, err := tx.Exec("DELETE FROM plc_status WHERE plc_id = $1", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao deletar status do PLC: %w", err)
	}

	// Deleta o PLC
	if _, err := tx.Exec("DELETE FROM plcs WHERE id = $1", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("erro ao deletar PLC: %w", err)
	}

	return tx.Commit()
}

// UpdatePLCStatus atualiza o status de um PLC
func (d *DB) UpdatePLCStatus(status PLCStatus) error {
	query := `
		INSERT INTO plc_status (plc_id, status, last_update)
		VALUES ($1, $2, $3)
		ON CONFLICT (plc_id) DO UPDATE
		SET status = $2, last_update = $3
	`

	_, err := d.Exec(query, status.PLCID, status.Status, status.LastUpdate)
	if err != nil {
		return fmt.Errorf("erro ao atualizar status do PLC %d: %w", status.PLCID, err)
	}

	return nil
}

// GetPLCsByNetwork retorna todos os PLCs em uma determinada sub-rede
func (d *DB) GetPLCsByNetwork(subnet string) ([]PLC, error) {
	query := `
		SELECT 
			p.id, p.name, p.ip_address, p.rack, p.slot, 
			p.description, p.active, p.created_at, p.updated_at,
			p.use_vlan, p.gateway, p.subnet_mask, p.vlan_id,
			COALESCE(s.status, 'unknown') as status
		FROM plcs p 
		LEFT JOIN plc_status s ON p.id = s.plc_id 
		WHERE p.active = true AND p.ip_address LIKE $1
	`

	// Adiciona % para fazer correspondência parcial no endereço IP
	likePattern := subnet + "%"

	var plcs []PLC

	rows, err := d.Query(query, likePattern)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar PLCs na rede %s: %w", subnet, err)
	}
	defer rows.Close()

	for rows.Next() {
		var plc PLC
		var status sql.NullString
		var gateway sql.NullString
		var subnetMask sql.NullString
		var vlanID sql.NullInt64

		// Scan individual fields instead of StructScan
		err := rows.Scan(
			&plc.ID, &plc.Name, &plc.IPAddress, &plc.Rack, &plc.Slot,
			&plc.Description, &plc.Active, &plc.CreatedAt, &plc.UpdatedAt,
			&plc.UseVLAN, &gateway, &subnetMask, &vlanID,
			&status)

		if err != nil {
			return nil, fmt.Errorf("erro ao ler dados do PLC: %w", err)
		}

		// Tratar campos que podem ser NULL
		if gateway.Valid {
			plc.Gateway = gateway.String
		}

		if subnetMask.Valid {
			plc.SubnetMask = subnetMask.String
		}

		if vlanID.Valid {
			plc.VLANID = int(vlanID.Int64)
		}

		if status.Valid {
			plc.Status = status.String
		} else {
			plc.Status = "unknown"
		}

		plcs = append(plcs, plc)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("erro durante iteração de PLCs na rede: %w", err)
	}

	return plcs, nil
}

// GetPLCTags retorna todas as tags de um PLC
func (d *DB) GetPLCTags(plcID int) ([]Tag, error) {
	query := `
		SELECT * FROM tags 
		WHERE plc_id = $1
		ORDER BY id
	`

	var tags []Tag
	err := d.Select(&tags, query, plcID)
	if err != nil {
		return nil, fmt.Errorf("erro ao consultar tags do PLC %d: %w", plcID, err)
	}

	return tags, nil
}

// GetTagByName retorna uma tag pelo nome e PLC ID
func (d *DB) GetTagByName(plcID int, name string) (*Tag, error) {
	var tag Tag
	query := "SELECT * FROM tags WHERE plc_id = $1 AND name = $2"

	err := d.Get(&tag, query, plcID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tag '%s' não encontrada no PLC ID %d", name, plcID)
		}
		return nil, fmt.Errorf("erro ao consultar tag: %w", err)
	}

	return &tag, nil
}

// GetTagByID retorna uma tag específica pelo ID
func (d *DB) GetTagByID(id int) (*Tag, error) {
	var tag Tag
	err := d.Get(&tag, "SELECT * FROM tags WHERE id = $1", id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tag com ID %d não encontrada", id)
		}
		return nil, fmt.Errorf("erro ao consultar tag: %w", err)
	}

	return &tag, nil
}
