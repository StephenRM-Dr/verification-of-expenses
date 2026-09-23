package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"example.com/m/v2/internal/api"
	"example.com/m/v2/internal/db"
	"example.com/m/v2/internal/export"
	"example.com/m/v2/internal/models"
	"example.com/m/v2/internal/storage"
	"example.com/m/v2/internal/utils"
	"example.com/m/v2/internal/whatsapp"
	"github.com/joho/godotenv"
)

// @title       Verification of Expenses - Transactions & Reports API
// @version     1.0
// @description Go backend for expense tracking, receipt storage, Excel reporting and WhatsApp notifications.
// @host        api.example.com
// @BasePath    /

var waClient *whatsapp.WAClient

func main() {
	// Load the local .env if present. On Koyeb there is no file and the variables
	// come from the environment, so its absence is not an error.
	if err := godotenv.Load(); err == nil {
		log.Println("📄 Configuration loaded from .env")
	}

	// Do not start without a credential: a production connection string used to be
	// embedded as the default value, so a misconfigured deployment would
	// silently connect to the wrong database.
	dbConnStr := os.Getenv("DATABASE_URL")
	if dbConnStr == "" {
		log.Fatal("DATABASE_URL is missing: set it in the environment or in a .env file (see .env.example)")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	database, err := db.InitDB(dbConnStr)
	if err != nil {
		log.Fatal("Error initializing database:", err)
	}
	defer database.Close()

	if err := storage.Init(); err != nil {
		log.Fatal("Error initializing storage:", err)
	}

	// Start the API server immediately so health checks pass
	fmt.Printf("🚀 Starting API server on port %s...\n", port)
	apiServer := api.NewServer(database, nil)
	go func() {
		if err := apiServer.Start(port); err != nil {
			log.Printf("❌ Critical API server error: %v", err)
		}
	}()

	// Start WhatsApp in the background
	fmt.Println("⏳ Initializing WhatsApp...")
	go func() {
		client, err := whatsapp.Connect()
		if err != nil {
			fmt.Printf("\n⚠️  Warning: Could not connect to WhatsApp: %v\n", err)
			return
		}
		waClient = client
		apiServer.SetWhatsAppClient(waClient)
		fmt.Println("\n✅ WhatsApp connected.")
	}()

	// Keep the database "warm" (keep-alive) to avoid Neon auto-suspend
	go func() {
		for {
			time.Sleep(2 * time.Minute)
			if database != nil {
				err := database.Ping()
				if err != nil {
					fmt.Printf("⚠️  Keep-alive: Error connecting to DB: %v\n", err)
				} else {
					fmt.Println("Keep-alive: DB active")
				}
			}
		}
	}()

	// Only run the interactive loop when attached to a terminal (TTY)
	fileInfo, _ := os.Stdin.Stat()
	if (fileInfo.Mode() & os.ModeCharDevice) == 0 {
		fmt.Println("🚀 Running in non-interactive mode (Cloud/Docker)")
		select {} // Block to keep the container alive
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Println("\n===============================")
		fmt.Println("   VERIFICATION OF EXPENSES    ")
		fmt.Println("===============================")
		fmt.Println("1. Add Transaction")
		fmt.Println("2. View History")
		fmt.Println("3. Search Transactions")
		fmt.Println("4. Delete Transaction")
		fmt.Println("5. Edit Transaction")
		fmt.Println("6. Export to Excel")
		fmt.Println("7. Send to WhatsApp")
		fmt.Println("8. Exit")
		fmt.Print("\nSelect an option: ")

		scanner.Scan()
		opcion := scanner.Text()

		switch opcion {
		case "1":
			crearRegistro(database, scanner)
		case "2":
			leerRegistros(database)
			utils.Pausa(scanner)
		case "3":
			buscarRegistros(database, scanner)
		case "4":
			eliminarRegistro(database, scanner)
		case "5":
			editarRegistro(database, scanner)
		case "6":
			exportarExcel(database, scanner)
		case "7":
			enviarWhatsApp(database, scanner)
		case "8":
			fmt.Println("Exiting...")
			return
		default:
			fmt.Println("Invalid option.")
		}
	}
}

func crearRegistro(database *sql.DB, scanner *bufio.Scanner) {
	fmt.Println("\n--- NEW TRANSACTION ---")

	t := promptTransaction(scanner, models.Transaccion{})

	err := db.CreateTransaction(database, t)
	if err != nil {
		fmt.Println("Error saving:", err)
	} else {
		fmt.Println("Transaction saved!")
	}
}

func leerRegistros(database *sql.DB) {
	transacciones, err := db.ListTransactions(database)
	if err != nil {
		fmt.Println("Error reading transactions:", err)
		return
	}
	printTable(transacciones)
}

func printTable(transacciones []models.Transaccion) {
	fmt.Printf("\n%-4s | %-12s | %-20s | %-10s | %-15s | %-s\n", "ID", "Date", "Desc", "Amount", "Bank", "Ref")
	fmt.Println(strings.Repeat("-", 100))

	var total float64
	for _, t := range transacciones {
		fmt.Printf("%-4d | %-12s | %-20s | %-10.2f | %-15s | %-s\n",
			t.ID, t.FechaPago, t.Descripcion, t.Monto, t.Banco, t.Referencia)
		total += t.Monto
	}
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("%62s Total: %.2f\n", "", total)
}

func buscarRegistros(database *sql.DB, scanner *bufio.Scanner) {
	fmt.Print("\nEnter search term: ")
	scanner.Scan()
	query := scanner.Text()

	transacciones, err := db.SearchTransactions(database, query)
	if err != nil {
		fmt.Println("Search error:", err)
		return
	}

	if len(transacciones) == 0 {
		fmt.Println("No transactions found.")
	} else {
		printTable(transacciones)
	}
	utils.Pausa(scanner)
}

func eliminarRegistro(database *sql.DB, scanner *bufio.Scanner) {
	leerRegistros(database)
	fmt.Print("\nID of the transaction to delete: ")
	scanner.Scan()
	var id int
	fmt.Sscanf(scanner.Text(), "%d", &id)

	err := db.DeleteTransaction(database, id)
	if err != nil {
		fmt.Println("Error deleting:", err)
	} else {
		fmt.Println("Transaction deleted successfully.")
	}
}

func editarRegistro(database *sql.DB, scanner *bufio.Scanner) {
	leerRegistros(database)
	fmt.Print("\nID of the transaction to edit: ")
	scanner.Scan()
	var id int
	fmt.Sscanf(scanner.Text(), "%d", &id)

	existing, err := db.GetTransactionByID(database, id)
	if err != nil {
		fmt.Println("Error: Transaction not found or DB error:", err)
		return
	}

	fmt.Printf("\n--- EDITING TRANSACTION [%d] ---\n", id)
	fmt.Println("Press ENTER to keep the current value.")

	updated := promptTransaction(scanner, existing)
	updated.ID = id

	err = db.UpdateTransaction(database, updated)
	if err != nil {
		fmt.Println("Error updating:", err)
	} else {
		fmt.Println("Transaction updated successfully!")
	}
}

func exportarExcel(database *sql.DB, scanner *bufio.Scanner) {
	fmt.Print("\nExport EVERYTHING (t) or a SEARCH (b)? [t]: ")
	scanner.Scan()
	tipo := strings.ToLower(scanner.Text())

	var transacciones []models.Transaccion
	var err error

	if tipo == "b" {
		fmt.Print("Search term: ")
		scanner.Scan()
		transacciones, err = db.SearchTransactions(database, scanner.Text())
	} else {
		transacciones, err = db.ListTransactions(database)
	}

	if err != nil || len(transacciones) == 0 {
		fmt.Println("No data to export.")
		return
	}

	fmt.Print("File name [reporte_pagos.xlsx]: ")
	scanner.Scan()
	fileName := scanner.Text()
	if fileName == "" {
		fileName = "reporte_pagos.xlsx"
	}
	if !strings.HasSuffix(fileName, ".xlsx") {
		fileName += ".xlsx"
	}

	err = export.ExportToExcel(transacciones, fileName)
	if err != nil {
		fmt.Println("Error exporting:", err)
	} else {
		fmt.Printf("Export successful! File: %s\n", fileName)
	}
	utils.Pausa(scanner)
}

func enviarWhatsApp(database *sql.DB, scanner *bufio.Scanner) {
	if waClient == nil {
		fmt.Println("❌ WhatsApp is not connected yet.")
		return
	}

	leerRegistros(database)
	fmt.Print("\nID of the transaction to send: ")
	scanner.Scan()
	var id int
	fmt.Sscanf(scanner.Text(), "%d", &id)

	t, err := db.GetTransactionByID(database, id)
	if err != nil {
		fmt.Println("Invalid ID.")
		return
	}

	fmt.Print("Recipient (Enter for group 'Prueba' or JID): ")
	scanner.Scan()
	recipient := strings.TrimSpace(scanner.Text())

	var targetJID string

	if recipient == "" || strings.ToLower(recipient) == "prueba" {
		fmt.Println("🔍 Looking for group 'Prueba'...")
		jid, err := waClient.GetGroupJIDByName("Prueba")
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}
		targetJID = jid.String()
		fmt.Printf("✅ Group 'Prueba' found: %s\n", targetJID)
	} else if !strings.Contains(recipient, "@") {
		// Try to look it up by name if it has no @
		fmt.Printf("🔍 Looking for group '%s'...\n", recipient)
		jid, err := waClient.GetGroupJIDByName(recipient)
		if err != nil {
			fmt.Printf("❌ %v. Try entering the full JID (e.g. 12345@g.us)\n", err)
			return
		}
		targetJID = jid.String()
		fmt.Printf("✅ Group found: %s\n", targetJID)
	} else {
		targetJID = recipient
	}

	// Send in the background
	go func() {
		err := waClient.SendTransaction(targetJID, t)
		if err != nil {
			fmt.Printf("\n❌ Error sending to WhatsApp (ID %d): %v\n", id, err)
		} else {
			fmt.Printf("\n✅ Transaction %d sent to WhatsApp in the background.\n", id)
		}
	}()

	fmt.Println("🚀 Send started in the background...")
}

func promptTransaction(scanner *bufio.Scanner, current models.Transaccion) models.Transaccion {
	t := current

	t.FechaPago = utils.LeerCadena(scanner, "Payment date (DD/MM/YYYY)", t.FechaPago)
	t.Descripcion = utils.LimpiarTexto(utils.LeerCadena(scanner, "Description", t.Descripcion))
	t.Monto = utils.LeerFlotante(scanner, "Amount", t.Monto)
	t.Ciudad = utils.LimpiarTexto(utils.LeerCadena(scanner, "City", t.Ciudad))
	t.Banco = utils.LimpiarTexto(utils.LeerCadena(scanner, "Bank used", t.Banco))
	t.Referencia = utils.LeerCadena(scanner, "Reference", t.Referencia)
	t.ImagenPath = utils.LeerCadena(scanner, "Image path (optional)", t.ImagenPath)

	return t
}
