// Command dbmigrate copies the data from one Postgres database to another.
//
// It exists to move the project between Neon regions/projects (for example to one
// where Object Storage can be enabled). It deliberately does not use pg_dump:
// the schema is a single table, so there is no need to install the Postgres
// client.
//
// Usage:
//
//	SRC_DATABASE_URL=... DST_DATABASE_URL=... go run ./cmd/dbmigrate          # inspect only, writes nothing
//	SRC_DATABASE_URL=... DST_DATABASE_URL=... go run ./cmd/dbmigrate -apply   # copy the data
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/stephenrm-dr/verification-of-expenses/internal/db"
	_ "github.com/lib/pq"
)

func main() {
	apply := flag.Bool("apply", false, "copy the data to the destination (without this flag it only inspects)")
	verify := flag.Bool("verify", false, "compare source and destination without writing anything")
	flag.Parse()

	srcURL := os.Getenv("SRC_DATABASE_URL")
	if srcURL == "" {
		log.Fatal("SRC_DATABASE_URL is missing")
	}

	src, err := sql.Open("postgres", srcURL)
	if err != nil {
		log.Fatalf("Could not open the source: %v", err)
	}
	defer src.Close()

	fmt.Println("== SOURCE ==")
	inspect(src)

	if !*apply && !*verify {
		fmt.Println("\nInspection mode: nothing was written. Use -apply to copy.")
		return
	}

	dstURL := os.Getenv("DST_DATABASE_URL")
	if dstURL == "" {
		log.Fatal("DST_DATABASE_URL is missing")
	}

	if *verify {
		dst, err := sql.Open("postgres", dstURL)
		if err != nil {
			log.Fatalf("Could not open the destination: %v", err)
		}
		defer dst.Close()
		compare(src, dst)
		return
	}

	// InitDB creates the historial table if it does not exist, with the same schema the app uses.
	dst, err := db.InitDB(dstURL)
	if err != nil {
		log.Fatalf("Could not prepare the destination: %v", err)
	}
	defer dst.Close()

	copied, err := copyHistorial(src, dst)
	if err != nil {
		log.Fatalf("Error copying: %v", err)
	}

	// The copy preserves IDs, so the sequence must be repositioned or the app's
	// first INSERT would collide with an existing primary key.
	if _, err := dst.Exec("SELECT setval(pg_get_serial_sequence('historial','id'), COALESCE((SELECT MAX(id) FROM historial), 1))"); err != nil {
		log.Fatalf("Error adjusting the sequence: %v", err)
	}

	fmt.Printf("\n✅ %d rows synced and sequence repositioned.\n", copied)
	fmt.Println("\n== DESTINATION ==")
	inspect(dst)
}

// compare contrasts source and destination beyond the row count: if an INSERT
// had truncated a text or shifted a decimal, the aggregates would not match.
func compare(src, dst *sql.DB) {
	// monto is REAL (float4): SUM(monto) accumulates in single precision and gives
	// different results depending on row order, so the cast to numeric
	// is essential for the comparison to mean anything.
	const q = `SELECT COUNT(*), COALESCE(MAX(id),0), COALESCE(SUM(monto::numeric),0),
		COALESCE(SUM(LENGTH(descripcion || referencia || banco_usado || ciudad || COALESCE(imagen_path,''))),0)
		FROM historial`

	var sc, sMax int
	var sSum, sLen float64
	if err := src.QueryRow(q).Scan(&sc, &sMax, &sSum, &sLen); err != nil {
		log.Fatalf("Error querying the source: %v", err)
	}

	var dc, dMax int
	var dSum, dLen float64
	if err := dst.QueryRow(q).Scan(&dc, &dMax, &dSum, &dLen); err != nil {
		log.Fatalf("Error querying the destination: %v", err)
	}

	fmt.Println("\n== COMPARISON ==")
	fmt.Printf("  %-22s source=%d destination=%d\n", "rows", sc, dc)
	fmt.Printf("  %-22s source=%d destination=%d\n", "max id", sMax, dMax)
	fmt.Printf("  %-22s source=%.2f destination=%.2f\n", "sum of amounts", sSum, dSum)
	fmt.Printf("  %-22s source=%.0f destination=%.0f\n", "text length", sLen, dLen)

	// The sequence must end up at the max id or the app would fail on insert.
	var nextID int
	if err := dst.QueryRow(`SELECT last_value FROM pg_get_serial_sequence('historial','id')`).Scan(&nextID); err == nil {
		fmt.Printf("  %-22s %d\n", "destination sequence", nextID)
	}

	if sc == dc && sMax == dMax && sSum == dSum && sLen == dLen {
		fmt.Println("\n✅ Source and destination match.")
		return
	}

	fmt.Println("\n❌ There are differences between source and destination.")
	diffRows(src, dst)
	os.Exit(1)
}

// diffRows finds which specific rows differ, to tell a copy failure apart from
// an edit made on the source while the migration was in progress.
func diffRows(src, dst *sql.DB) {
	load := func(conn *sql.DB) (map[int]float64, error) {
		rows, err := conn.Query(`SELECT id, monto FROM historial`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		m := make(map[int]float64)
		for rows.Next() {
			var id int
			var monto float64
			if err := rows.Scan(&id, &monto); err != nil {
				return nil, err
			}
			m[id] = monto
		}
		return m, rows.Err()
	}

	s, err := load(src)
	if err != nil {
		log.Fatalf("Error reading the source: %v", err)
	}
	d, err := load(dst)
	if err != nil {
		log.Fatalf("Error reading the destination: %v", err)
	}

	fmt.Println("\n  Rows with differences:")
	shown := 0
	for id, sm := range s {
		dm, ok := d[id]
		if !ok {
			fmt.Printf("    id=%-6d missing from the destination (amount %.2f)\n", id, sm)
			shown++
		} else if sm != dm {
			fmt.Printf("    id=%-6d source=%.2f destination=%.2f\n", id, sm, dm)
			shown++
		}
		if shown >= 20 {
			fmt.Println("    ... (showing the first 20)")
			return
		}
	}
	for id := range d {
		if _, ok := s[id]; !ok {
			fmt.Printf("    id=%-6d extra in the destination\n", id)
		}
	}
	if shown == 0 {
		fmt.Println("    (none: the difference was only in the aggregates)")
	}
}

func inspect(conn *sql.DB) {
	rows, err := conn.Query(`SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' ORDER BY table_name`)
	if err != nil {
		log.Fatalf("Could not list the tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			log.Fatalf("Error reading tables: %v", err)
		}
		tables = append(tables, name)
	}

	for _, t := range tables {
		var count int
		if err := conn.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", t)).Scan(&count); err != nil {
			fmt.Printf("  %-20s (could not count: %v)\n", t, err)
			continue
		}
		fmt.Printf("  %-20s %d rows\n", t, count)
	}

	var withImage int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM historial WHERE COALESCE(imagen_path,'') <> ''`).Scan(&withImage); err == nil {
		fmt.Printf("  %-20s %d rows with imagen_path\n", "→ historial", withImage)
	}
}

func copyHistorial(src, dst *sql.DB) (int, error) {
	rows, err := src.Query(`SELECT id, fecha_pago, descripcion, monto, ciudad, banco_usado,
		referencia, COALESCE(imagen_path, '') FROM historial ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	tx, err := dst.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// The upsert makes the copy idempotent: it can be run several times to carry
	// over the delta while the old system keeps receiving records, and the last
	// run during the cutover leaves both databases identical.
	stmt, err := tx.Prepare(`INSERT INTO historial
		(id, fecha_pago, descripcion, monto, ciudad, banco_usado, referencia, imagen_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			fecha_pago = EXCLUDED.fecha_pago, descripcion = EXCLUDED.descripcion,
			monto = EXCLUDED.monto, ciudad = EXCLUDED.ciudad,
			banco_usado = EXCLUDED.banco_usado, referencia = EXCLUDED.referencia,
			imagen_path = EXCLUDED.imagen_path`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	n := 0
	for rows.Next() {
		var id int
		var fecha, desc, ciudad, banco, ref, img string
		var monto float64
		if err := rows.Scan(&id, &fecha, &desc, &monto, &ciudad, &banco, &ref, &img); err != nil {
			return n, err
		}
		if _, err := stmt.Exec(id, fecha, desc, monto, ciudad, banco, ref, img); err != nil {
			return n, fmt.Errorf("row id=%d: %w", id, err)
		}
		n++
		if n%500 == 0 {
			fmt.Printf("  ... %d rows\n", n)
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}

	return n, tx.Commit()
}
