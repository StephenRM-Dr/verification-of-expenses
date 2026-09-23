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
	// Carga el .env local si existe. En Koyeb no hay archivo y las variables
	// llegan del entorno, así que su ausencia no es un error.
	if err := godotenv.Load(); err == nil {
		log.Println("📄 Configuración cargada desde .env")
	}

	// Sin credencial no se arranca: antes había una cadena de producción
	// embebida como valor por defecto, de modo que un despliegue mal configurado
	// se conectaba a la base equivocada sin avisar.
	dbConnStr := os.Getenv("DATABASE_URL")
	if dbConnStr == "" {
		log.Fatal("Falta DATABASE_URL: defínela en el entorno o en un archivo .env (ver .env.example)")
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

	// Iniciar Servidor API inmediatamente para pasar Health Checks
	fmt.Printf("🚀 Iniciando API Server en puerto %s...\n", port)
	apiServer := api.NewServer(database, nil)
	go func() {
		if err := apiServer.Start(port); err != nil {
			log.Printf("❌ Error crítico en API Server: %v", err)
		}
	}()

	// Iniciar WhatsApp en segundo plano
	fmt.Println("⏳ Inicializando WhatsApp...")
	go func() {
		client, err := whatsapp.Connect()
		if err != nil {
			fmt.Printf("\n⚠️  Aviso: No se pudo conectar a WhatsApp: %v\n", err)
			return
		}
		waClient = client
		apiServer.SetWhatsAppClient(waClient)
		fmt.Println("\n✅ WhatsApp Conectado.")
	}()

	// Mantener la Base de Datos "caliente" (Keep-alive) para evitar auto-suspend de Neon
	go func() {
		for {
			time.Sleep(2 * time.Minute)
			if database != nil {
				err := database.Ping()
				if err != nil {
					fmt.Printf("⚠️  Keep-alive: Error al conectar con DB: %v\n", err)
				} else {
					fmt.Println("保持 (Keep-alive): DB activa")
				}
			}
		}
	}()

	// Solo ejecutar el loop interactivo si estamos en una terminal (TTY)
	fileInfo, _ := os.Stdin.Stat()
	if (fileInfo.Mode() & os.ModeCharDevice) == 0 {
		fmt.Println("🚀 Ejecutando en modo no interactivo (Cloud/Docker)")
		select {} // Bloquear para mantener el contenedor vivo
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Println("\n===============================")
		fmt.Println("      BRAILER LEDGER (GO)      ")
		fmt.Println("===============================")
		fmt.Println("1. Ingresar Data")
		fmt.Println("2. Ver Histórico")
		fmt.Println("3. Buscar Registro")
		fmt.Println("4. Eliminar Registro")
		fmt.Println("5. Editar Registro")
		fmt.Println("6. Exportar a Excel")
		fmt.Println("7. Enviar a WhatsApp")
		fmt.Println("8. Salir")
		fmt.Print("\nSeleccione una opción: ")

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
			fmt.Println("Saliendo del sistema...")
			return
		default:
			fmt.Println("Opción no válida.")
		}
	}
}

func crearRegistro(database *sql.DB, scanner *bufio.Scanner) {
	fmt.Println("\n--- NUEVO INGRESO ---")

	t := promptTransaction(scanner, models.Transaccion{})

	err := db.CreateTransaction(database, t)
	if err != nil {
		fmt.Println("Error al guardar:", err)
	} else {
		fmt.Println("¡Registro exitoso!")
	}
}

func leerRegistros(database *sql.DB) {
	transacciones, err := db.ListTransactions(database)
	if err != nil {
		fmt.Println("Error al leer registros:", err)
		return
	}
	printTable(transacciones)
}

func printTable(transacciones []models.Transaccion) {
	fmt.Printf("\n%-4s | %-12s | %-20s | %-10s | %-15s | %-s\n", "ID", "Fecha", "Desc", "Monto", "Banco", "Ref")
	fmt.Println(strings.Repeat("-", 100))

	var total float64
	for _, t := range transacciones {
		fmt.Printf("%-4d | %-12s | %-20s | %-10.2f | %-15s | %-s\n",
			t.ID, t.FechaPago, t.Descripcion, t.Monto, t.Banco, t.Referencia)
		total += t.Monto
	}
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("%62s Total: Bs. %.2f\n", "", total)
}

func buscarRegistros(database *sql.DB, scanner *bufio.Scanner) {
	fmt.Print("\nIngrese término de búsqueda: ")
	scanner.Scan()
	query := scanner.Text()

	transacciones, err := db.SearchTransactions(database, query)
	if err != nil {
		fmt.Println("Error en búsqueda:", err)
		return
	}

	if len(transacciones) == 0 {
		fmt.Println("No se encontraron registros.")
	} else {
		printTable(transacciones)
	}
	utils.Pausa(scanner)
}

func eliminarRegistro(database *sql.DB, scanner *bufio.Scanner) {
	leerRegistros(database)
	fmt.Print("\nID del registro a eliminar: ")
	scanner.Scan()
	var id int
	fmt.Sscanf(scanner.Text(), "%d", &id)

	err := db.DeleteTransaction(database, id)
	if err != nil {
		fmt.Println("Error al eliminar:", err)
	} else {
		fmt.Println("Registro borrado correctamente.")
	}
}

func editarRegistro(database *sql.DB, scanner *bufio.Scanner) {
	leerRegistros(database)
	fmt.Print("\nID del registro a editar: ")
	scanner.Scan()
	var id int
	fmt.Sscanf(scanner.Text(), "%d", &id)

	existing, err := db.GetTransactionByID(database, id)
	if err != nil {
		fmt.Println("Error: No se encontró el registro o error en DB:", err)
		return
	}

	fmt.Printf("\n--- EDITANDO REGISTRO [%d] ---\n", id)
	fmt.Println("Presione ENTER para mantener el valor actual.")

	updated := promptTransaction(scanner, existing)
	updated.ID = id

	err = db.UpdateTransaction(database, updated)
	if err != nil {
		fmt.Println("Error al actualizar:", err)
	} else {
		fmt.Println("¡Registro actualizado con éxito!")
	}
}

func exportarExcel(database *sql.DB, scanner *bufio.Scanner) {
	fmt.Print("\n¿Desea exportar TODO (t) o una BÚSQUEDA (b)? [t]: ")
	scanner.Scan()
	tipo := strings.ToLower(scanner.Text())

	var transacciones []models.Transaccion
	var err error

	if tipo == "b" {
		fmt.Print("Término de búsqueda: ")
		scanner.Scan()
		transacciones, err = db.SearchTransactions(database, scanner.Text())
	} else {
		transacciones, err = db.ListTransactions(database)
	}

	if err != nil || len(transacciones) == 0 {
		fmt.Println("No hay datos para exportar.")
		return
	}

	fmt.Print("Nombre del archivo [reporte_pagos.xlsx]: ")
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
		fmt.Println("Error al exportar:", err)
	} else {
		fmt.Printf("¡Exportación exitosa! Archivo: %s\n", fileName)
	}
	utils.Pausa(scanner)
}

func enviarWhatsApp(database *sql.DB, scanner *bufio.Scanner) {
	if waClient == nil {
		fmt.Println("❌ WhatsApp no está conectado todavía.")
		return
	}

	leerRegistros(database)
	fmt.Print("\nID del registro a enviar: ")
	scanner.Scan()
	var id int
	fmt.Sscanf(scanner.Text(), "%d", &id)

	t, err := db.GetTransactionByID(database, id)
	if err != nil {
		fmt.Println("ID no válido.")
		return
	}

	fmt.Print("Destinatario (Enter para grupo 'Prueba' o JID): ")
	scanner.Scan()
	recipient := strings.TrimSpace(scanner.Text())

	var targetJID string

	if recipient == "" || strings.ToLower(recipient) == "prueba" {
		fmt.Println("🔍 Buscando grupo 'Prueba'...")
		jid, err := waClient.GetGroupJIDByName("Prueba")
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return
		}
		targetJID = jid.String()
		fmt.Printf("✅ Grupo 'Prueba' encontrado: %s\n", targetJID)
	} else if !strings.Contains(recipient, "@") {
		// Intentar buscar por nombre si no tiene @
		fmt.Printf("🔍 Buscando grupo '%s'...\n", recipient)
		jid, err := waClient.GetGroupJIDByName(recipient)
		if err != nil {
			fmt.Printf("❌ %v. Intenta ingresar el JID completo (ej: 12345@g.us)\n", err)
			return
		}
		targetJID = jid.String()
		fmt.Printf("✅ Grupo encontrado: %s\n", targetJID)
	} else {
		targetJID = recipient
	}

	// Enviar en segundo plano
	go func() {
		err := waClient.SendTransaction(targetJID, t)
		if err != nil {
			fmt.Printf("\n❌ Error enviando a WhatsApp (ID %d): %v\n", id, err)
		} else {
			fmt.Printf("\n✅ Registro %d enviado a WhatsApp en segundo plano.\n", id)
		}
	}()

	fmt.Println("🚀 Envío iniciado en segundo plano...")
}

func promptTransaction(scanner *bufio.Scanner, current models.Transaccion) models.Transaccion {
	t := current

	t.FechaPago = utils.LeerCadena(scanner, "Fecha de pago (DD/MM/YYYY)", t.FechaPago)
	t.Descripcion = utils.LimpiarTexto(utils.LeerCadena(scanner, "Descripción", t.Descripcion))
	t.Monto = utils.LeerFlotante(scanner, "Monto", t.Monto)
	t.Ciudad = utils.LimpiarTexto(utils.LeerCadena(scanner, "Ciudad", t.Ciudad))
	t.Banco = utils.LimpiarTexto(utils.LeerCadena(scanner, "Banco Usado", t.Banco))
	t.Referencia = utils.LeerCadena(scanner, "Referencia", t.Referencia)
	t.ImagenPath = utils.LeerCadena(scanner, "Ruta de la Imagen (opcional)", t.ImagenPath)

	return t
}
