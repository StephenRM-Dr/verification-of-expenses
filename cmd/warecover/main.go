// Command warecover recovers lost receipts from a WhatsApp chat export.
//
// Files uploaded before migrating to object storage lived on the container's
// ephemeral disk and were lost on every redeploy. But every transaction was
// sent to the WhatsApp group with its image attached and a message footer that
// includes the reference, the amount and the date. By exporting that chat "with
// media" they can be recovered, matching on those three fields at once.
//
// Usage:
//
//	go run ./cmd/warecover -dir "path/to/export"          # dry run, writes nothing
//	go run ./cmd/warecover -dir "path/to/export" -apply   # uploads and updates
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stephenrm-dr/verification-of-expenses/internal/db"
	"github.com/stephenrm-dr/verification-of-expenses/internal/storage"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

// A new message line starts with the date; the rest are continuations of the
// previous message (the footer that accompanies the attachment).
//
// NOTE: these patterns match the Spanish-locale WhatsApp export format (and the
// Spanish report message older versions of the app sent), so the Spanish keywords
// must stay as they are. English equivalents are also accepted.
var (
	reInicio  = regexp.MustCompile(`^\d{1,2}/\d{1,2}/\d{4},`)
	reAdjunto = regexp.MustCompile(`([^\s:\x{200e}]+\.\w{2,5})\s*\((?:archivo adjunto|file attached)\)`)
	reRef     = regexp.MustCompile(`\*(?:Referencia|Reference):\*\s*(.+)`)
	reMonto   = regexp.MustCompile(`\*(?:Monto|Amount):\*\s*(?:Bs\.\s*)?([\d.,]+)`)
	reFecha   = regexp.MustCompile(`\*(?:Fecha|Date):\*\s*(.+)`)
)

type adjunto struct {
	archivo    string
	referencia string
	monto      float64
	fecha      string
}

type fila struct {
	id         int
	referencia string
	monto      float64
	fecha      string
	imagen     string
}

// clave uniquely identifies a transaction. The reference alone is not enough:
// there are repeated references in the database, and assigning the wrong image
// to a payment would be worse than not recovering it.
type clave struct {
	referencia string
	monto      int64 // cents, to avoid comparing floats
	fecha      string
}

func main() {
	dir := flag.String("dir", "", "folder of the WhatsApp export (unzipped)")
	apply := flag.Bool("apply", false, "upload the files and update the database (without this it only simulates)")
	flag.Parse()

	if *dir == "" {
		log.Fatal("Missing -dir with the export folder")
	}

	godotenv.Load()

	conn := os.Getenv("DATABASE_URL")
	if conn == "" {
		log.Fatal("DATABASE_URL is missing")
	}

	adjuntos, err := parsearChat(*dir)
	if err != nil {
		log.Fatalf("Error reading the export: %v", err)
	}
	fmt.Printf("📄 System attachments found in the chat: %d\n", len(adjuntos))

	database, err := db.InitDB(conn)
	if err != nil {
		log.Fatalf("Error connecting to the database: %v", err)
	}
	defer database.Close()

	if err := storage.Init(); err != nil {
		log.Fatalf("Error initializing storage: %v", err)
	}
	if !storage.UsingS3() {
		log.Fatal("Storage is in local-disk mode: set the bucket variables before recovering")
	}

	filas, err := cargarFilas(database)
	if err != nil {
		log.Fatalf("Error reading the ledger: %v", err)
	}
	fmt.Printf("🗄️  Transactions in the database: %d\n", len(filas))

	ctx := context.Background()
	existentes, err := storage.List(ctx)
	if err != nil {
		log.Fatalf("Error listing the bucket: %v", err)
	}
	fmt.Printf("☁️  Receipts already present in the bucket: %d\n", len(existentes))

	plan, ambiguas, huerfanos := emparejar(adjuntos, filas, existentes, *dir)

	fmt.Printf("\n=== PLAN ===\n")
	fmt.Printf("  rows to recover            : %d\n", len(plan))
	fmt.Printf("  ambiguous attachments skipped: %d\n", ambiguas)
	fmt.Printf("  attachments without a row or file: %d\n", huerfanos)

	if !*apply {
		fmt.Println("\nDry-run mode: nothing was uploaded or modified. Use -apply to execute.")
		return
	}

	subidas, fallos := ejecutar(ctx, database, plan, *dir)
	fmt.Printf("\n✅ %d receipts recovered", subidas)
	if fallos > 0 {
		fmt.Printf(", %d with errors (see above)", fallos)
	}
	fmt.Println()
}

// parsearChat groups the lines into messages and extracts the ones the system sent
// with an attachment. The file may be on the first line of the message itself or in
// the immediately preceding message, depending on how WhatsApp split the footer.
func parsearChat(dir string) ([]adjunto, error) {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ruta string
	for _, e := range entradas {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
			ruta = filepath.Join(dir, e.Name())
			break
		}
	}
	if ruta == "" {
		return nil, fmt.Errorf("chat .txt file not found in %s", dir)
	}

	datos, err := os.ReadFile(ruta)
	if err != nil {
		return nil, err
	}

	var mensajes [][]string
	var actual []string
	for _, ln := range strings.Split(string(datos), "\n") {
		if reInicio.MatchString(ln) {
			if len(actual) > 0 {
				mensajes = append(mensajes, actual)
			}
			actual = []string{ln}
		} else if len(actual) > 0 {
			actual = append(actual, ln)
		}
	}
	if len(actual) > 0 {
		mensajes = append(mensajes, actual)
	}

	var res []adjunto
	for i, msg := range mensajes {
		cuerpo := strings.Join(msg, "\n")
		if !strings.Contains(cuerpo, "NUEVO REGISTRO") && !strings.Contains(cuerpo, "NEW TRANSACTION") {
			continue
		}

		m := reAdjunto.FindStringSubmatch(msg[0])
		if m == nil && i > 0 {
			m = reAdjunto.FindStringSubmatch(mensajes[i-1][0])
		}
		if m == nil {
			continue
		}

		res = append(res, adjunto{
			archivo:    strings.TrimSpace(m[1]),
			referencia: capturar(reRef, cuerpo),
			monto:      aMonto(capturar(reMonto, cuerpo)),
			fecha:      capturar(reFecha, cuerpo),
		})
	}
	return res, nil
}

func capturar(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(strings.ReplaceAll(m[1], "\r", ""))
	}
	return ""
}

func aMonto(s string) float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func aClave(ref string, monto float64, fecha string) clave {
	return clave{
		referencia: strings.TrimSpace(ref),
		monto:      int64(math.Round(monto * 100)),
		fecha:      strings.TrimSpace(fecha),
	}
}

func cargarFilas(database *sql.DB) ([]fila, error) {
	rows, err := database.Query(`SELECT id, referencia, monto, fecha_pago,
		COALESCE(imagen_path,'') FROM historial`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []fila
	for rows.Next() {
		var f fila
		if err := rows.Scan(&f.id, &f.referencia, &f.monto, &f.fecha, &f.imagen); err != nil {
			return nil, err
		}
		res = append(res, f)
	}
	return res, rows.Err()
}

// emparejar decides which file belongs to which row. It discards keys that
// appear more than once -in the chat or in the database- because there is no way
// to know which image goes with which payment, and it skips rows whose receipt is
// already in the bucket so as not to overwrite what already works.
func emparejar(adjuntos []adjunto, filas []fila, existentes map[string]bool, dir string) (map[int]string, int, int) {
	porClave := make(map[clave][]fila)
	for _, f := range filas {
		porClave[aClave(f.referencia, f.monto, f.fecha)] = append(porClave[aClave(f.referencia, f.monto, f.fecha)], f)
	}

	vistas := make(map[clave]int)
	for _, a := range adjuntos {
		vistas[aClave(a.referencia, a.monto, a.fecha)]++
	}

	plan := make(map[int]string)
	ambiguas, huerfanos := 0, 0

	for _, a := range adjuntos {
		k := aClave(a.referencia, a.monto, a.fecha)

		if vistas[k] > 1 || len(porClave[k]) > 1 {
			ambiguas++
			continue
		}
		if len(porClave[k]) == 0 {
			huerfanos++
			continue
		}
		f := porClave[k][0]

		// Already has a live receipt in the bucket: leave it alone.
		if f.imagen != "" && existentes[storage.Name(f.imagen)] {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, a.archivo)); err != nil {
			huerfanos++
			continue
		}
		plan[f.id] = a.archivo
	}
	return plan, ambiguas, huerfanos
}

func ejecutar(ctx context.Context, database *sql.DB, plan map[int]string, dir string) (int, int) {
	subidas, fallos, n := 0, 0, 0
	for id, archivo := range plan {
		n++

		f, err := os.Open(filepath.Join(dir, archivo))
		if err != nil {
			log.Printf("⚠️  id=%d: could not open %s: %v", id, archivo, err)
			fallos++
			continue
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			fallos++
			continue
		}

		ext := filepath.Ext(archivo)
		// The id suffix avoids collisions within the same nanosecond.
		nombre := fmt.Sprintf("%d-%d%s", time.Now().UnixNano(), id, ext)

		ruta, err := storage.Save(ctx, nombre, f, info.Size(), mime.TypeByExtension(ext))
		f.Close()
		if err != nil {
			log.Printf("⚠️  id=%d: error uploading %s: %v", id, archivo, err)
			fallos++
			continue
		}

		if _, err := database.Exec("UPDATE historial SET imagen_path = $1 WHERE id = $2", ruta, id); err != nil {
			log.Printf("⚠️  id=%d: uploaded but could not update the row: %v", id, err)
			fallos++
			continue
		}

		subidas++
		if n%100 == 0 {
			fmt.Printf("  ... %d of %d\n", n, len(plan))
		}
	}
	return subidas, fallos
}
