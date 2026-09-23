package db

import (
	"database/sql"
	"github.com/stephenrm-dr/verification-of-expenses/internal/models"
	_ "github.com/lib/pq"
)

// InitDB initializes the database and creates the table if it does not exist.
func InitDB(connStr string) (*sql.DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	sqlStmt := `
	CREATE TABLE IF NOT EXISTS historial (
		id SERIAL PRIMARY KEY,
		fecha_pago TEXT,
		descripcion TEXT,
		monto NUMERIC(15,2),
		ciudad TEXT,
		banco_usado TEXT,
		referencia TEXT,
		imagen_path TEXT
	);`
	_, err = db.Exec(sqlStmt)
	if err != nil {
		return nil, err
	}

	// Quick migration: try to add the imagen_path column if it does not exist
	db.Exec("ALTER TABLE historial ADD COLUMN imagen_path TEXT")
	// Convert existing NULLs to empty strings to avoid Scan errors
	db.Exec("UPDATE historial SET imagen_path = '' WHERE imagen_path IS NULL")

	if err := migrateMontoToNumeric(db); err != nil {
		return nil, err
	}

	return db, nil
}

// migrateMontoToNumeric converts monto from REAL to NUMERIC(15,2).
//
// REAL is a single-precision float (~7 significant digits): with amounts of
// eight or nine digits the stored value is no longer exact, and sums depend
// on row order. Money needs an exact decimal.
//
// It is idempotent: if the column is already numeric it does nothing, so it can run
// on every startup at no cost.
func migrateMontoToNumeric(db *sql.DB) error {
	var dataType string
	err := db.QueryRow(`SELECT data_type FROM information_schema.columns
		WHERE table_name = 'historial' AND column_name = 'monto'`).Scan(&dataType)
	if err != nil {
		// If the type cannot be determined, do not block startup.
		return nil
	}
	if dataType == "numeric" {
		return nil
	}

	// The cast goes through text to preserve the decimal representation the
	// user saw when entering the amount, instead of the float's real binary value.
	_, err = db.Exec(`ALTER TABLE historial
		ALTER COLUMN monto TYPE NUMERIC(15,2) USING monto::text::numeric(15,2)`)
	return err
}

// CreateTransaction inserts a new transaction into the database.
func CreateTransaction(db *sql.DB, t models.Transaccion) error {
	_, err := db.Exec(`INSERT INTO historial (fecha_pago, descripcion, monto, ciudad, banco_usado, referencia, imagen_path) 
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, t.FechaPago, t.Descripcion, t.Monto, t.Ciudad, t.Banco, t.Referencia, t.ImagenPath)
	return err
}

// ListTransactions retrieves all transactions ordered by ID descending.
func ListTransactions(db *sql.DB) ([]models.Transaccion, error) {
	rows, err := db.Query("SELECT id, fecha_pago, descripcion, monto, ciudad, banco_usado, referencia, COALESCE(imagen_path, '') FROM historial ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transacciones := []models.Transaccion{}
	for rows.Next() {
		var t models.Transaccion
		err := rows.Scan(&t.ID, &t.FechaPago, &t.Descripcion, &t.Monto, &t.Ciudad, &t.Banco, &t.Referencia, &t.ImagenPath)
		if err != nil {
			return nil, err
		}
		transacciones = append(transacciones, t)
	}
	return transacciones, nil
}

// SearchTransactions searches transactions by description or reference (case insensitive).
func SearchTransactions(db *sql.DB, query string) ([]models.Transaccion, error) {
	searchQuery := "%" + query + "%"
	rows, err := db.Query(`SELECT id, fecha_pago, descripcion, monto, ciudad, banco_usado, referencia, COALESCE(imagen_path, '') 
		FROM historial 
		WHERE LOWER(descripcion) LIKE LOWER($1) OR LOWER(referencia) LIKE LOWER($2)
		ORDER BY id DESC`, searchQuery, searchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transacciones := []models.Transaccion{}
	for rows.Next() {
		var t models.Transaccion
		err := rows.Scan(&t.ID, &t.FechaPago, &t.Descripcion, &t.Monto, &t.Ciudad, &t.Banco, &t.Referencia, &t.ImagenPath)
		if err != nil {
			return nil, err
		}
		transacciones = append(transacciones, t)
	}
	return transacciones, nil
}

// GetTransactionByID retrieves a single transaction by its ID.
func GetTransactionByID(db *sql.DB, id int) (models.Transaccion, error) {
	var t models.Transaccion
	err := db.QueryRow("SELECT id, fecha_pago, descripcion, monto, ciudad, banco_usado, referencia, COALESCE(imagen_path, '') FROM historial WHERE id = $1", id).
		Scan(&t.ID, &t.FechaPago, &t.Descripcion, &t.Monto, &t.Ciudad, &t.Banco, &t.Referencia, &t.ImagenPath)
	return t, err
}

// UpdateTransaction updates an existing transaction.
func UpdateTransaction(db *sql.DB, t models.Transaccion) error {
	_, err := db.Exec(`UPDATE historial SET fecha_pago = $1, descripcion = $2, monto = $3, ciudad = $4, banco_usado = $5, referencia = $6, imagen_path = $7 
		WHERE id = $8`, t.FechaPago, t.Descripcion, t.Monto, t.Ciudad, t.Banco, t.Referencia, t.ImagenPath, t.ID)
	return err
}

// DeleteTransaction deletes a transaction by its ID.
func DeleteTransaction(db *sql.DB, id int) error {
	_, err := db.Exec("DELETE FROM historial WHERE id = $1", id)
	return err
}
