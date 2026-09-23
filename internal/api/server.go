package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync" // Añadido para manejo seguro del cliente de WhatsApp
	"time"

	_ "example.com/m/v2/docs" // Reemplaza con el nombre de tu módulo go.mod
	"example.com/m/v2/internal/db"
	"example.com/m/v2/internal/export"
	"example.com/m/v2/internal/models"
	"example.com/m/v2/internal/storage"
	"example.com/m/v2/internal/whatsapp"
	httpSwagger "github.com/swaggo/http-swagger"
)

type Server struct {
	db       *sql.DB
	waClient *whatsapp.WAClient
	mu       sync.RWMutex // Protege el acceso a waClient
	sending  map[int]bool // Trackea transacciones en proceso de envío
	smu      sync.Mutex   // Protege el acceso al mapa 'sending'
}

func NewServer(database *sql.DB, wa *whatsapp.WAClient) *Server {
	return &Server{
		db:       database,
		waClient: wa,
		sending:  make(map[int]bool),
	}
}

// SetWhatsAppClient permite inyectar el cliente una vez conectado
func (s *Server) SetWhatsAppClient(wa *whatsapp.WAClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waClient = wa
}

func (s *Server) Start(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/docs/", httpSwagger.WrapHandler)
	
	// Endpoint de health check para Koyeb y otros servicios cloud
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Middleware para CORS y Logging de depuración
	corsMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("📡 [API] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc("/api/transactions", s.handleTransactions)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/whatsapp/send", s.handleSendWhatsApp)
	mux.HandleFunc("/api/whatsapp/status", s.handleWhatsAppStatus)
	mux.HandleFunc("/api/whatsapp/logout", s.handleWhatsAppLogout)
	mux.HandleFunc("/api/export", s.handleExport)

	// Servir comprobantes desde el backend de almacenamiento activo (bucket o disco)
	mux.HandleFunc("/uploads/", s.handleUploads)

	fmt.Printf("🚀 API Server running on http://192.168.1.5:%s\n", port)
	return http.ListenAndServe(":"+port, corsMiddleware(mux))
}

// updateAndCleanup actualiza la transacción y borra el comprobante anterior si
// fue reemplazado por otro.
//
// El formulario solo envía el campo "image" cuando el usuario elige un archivo
// nuevo; si conserva el existente reenvía "imagen_path" con la misma ruta. Por
// eso la condición es que la ruta guardada cambie: así se limpia el archivo
// sustituido sin tocar el que sigue en uso.
func (s *Server) updateAndCleanup(ctx context.Context, t models.Transaccion) error {
	previous, findErr := db.GetTransactionByID(s.db, t.ID)

	if err := db.UpdateTransaction(s.db, t); err != nil {
		return err
	}

	if findErr != nil || previous.ImagenPath == "" || previous.ImagenPath == t.ImagenPath {
		return nil
	}

	// Igual que en el borrado, un fallo aquí no invalida la actualización:
	// queda un huérfano registrado en el log en vez de un error para el usuario.
	if err := storage.Delete(ctx, previous.ImagenPath); err != nil {
		log.Printf("⚠️  [uploads] Transacción %d actualizada, pero no se pudo borrar el comprobante anterior %s: %v",
			t.ID, previous.ImagenPath, err)
	}
	return nil
}

// handleUploads sirve un comprobante desde el bucket S3 o el disco local.
func (s *Server) handleUploads(w http.ResponseWriter, r *http.Request) {
	name := storage.Name(r.URL.Path)
	if name == "" || name == "." || name == "/" {
		http.NotFound(w, r)
		return
	}

	file, err := storage.Open(r.Context(), name)
	if err != nil {
		log.Printf("⚠️  [uploads] %s no disponible: %v", name, err)
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, file)
}

// handleTransactions nuclea el CRUD de transacciones.
// @Summary      Manejar Transacciones (CRUD)
// @Description  Permite Listar (GET), Crear (POST), Editar (PUT) usando multipart/form-data o JSON, y Eliminar (DELETE) transacciones financieras.
// @Tags         transactions
// @Accept       json,mpfd
// @Produce      json
// @Param        id            query     int     false  "ID de la transacción (Requerido solo para DELETE)"
// @Param        id            formData  int     false  "ID de la transacción (Requerido solo para PUT con FormData)"
// @Param        fecha_pago    formData  string  false  "Fecha de pago (DD/MM/YYYY)"
// @Param        descripcion   formData  string  false  "Descripción o concepto del gasto"
// @Param        monto         formData  number  false  "Monto financiero de la operación"
// @Param        ciudad        formData  string  false  "Ciudad de la transacción"
// @Param        banco_usado   formData  string  false  "Banco utilizado"
// @Param        referencia    formData  string  false  "Número de referencia bancaria"
// @Param        imagen_path   formData  string  false  "Ruta de imagen existente si no se reemplaza"
// @Param        image         formData  file    false  "Archivo comprobante / soporte físico"
// @Success      200           {array}   models.Transaccion "Operación exitosa (GET devuelve lista, PUT confirma estado)"
// @Success      201           {object}  map[string]string  "Transacción creada exitosamente (POST)"
// @Failure      400           {string}  string "Datos de entrada inválidos"
// @Failure      500           {string}  string "Error interno del servidor de base de datos"
// @Router       /api/transactions [get]
// @Router       /api/transactions [post]
// @Router       /api/transactions [put]
// @Router       /api/transactions [delete]
func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		transacciones, err := db.ListTransactions(s.db)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(transacciones)

	case "POST", "PUT":
		// Manejar Multi-part form for images
		err := r.ParseMultipartForm(10 << 20) // 10MB
		if err != nil {
			var t models.Transaccion
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				http.Error(w, "Invalid data", http.StatusBadRequest)
				return
			}

			if r.Method == "POST" {
				if err := db.CreateTransaction(s.db, t); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusCreated)
			} else {
				if err := s.updateAndCleanup(r.Context(), t); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		// Extraer datos del form
		monto, _ := strconv.ParseFloat(r.FormValue("monto"), 64)
		t := models.Transaccion{
			FechaPago:   r.FormValue("fecha_pago"),
			Descripcion: r.FormValue("descripcion"),
			Monto:       monto,
			Ciudad:      r.FormValue("ciudad"),
			Banco:       r.FormValue("banco_usado"),
			Referencia:  r.FormValue("referencia"),
			ImagenPath:  r.FormValue("imagen_path"), // Mantener imagen existente si no se sube una nueva
		}

		if r.Method == "PUT" {
			id, _ := strconv.Atoi(r.FormValue("id"))
			t.ID = id
		}

		// Manejar archivo si existe
		file, handler, err := r.FormFile("image")
		if err == nil {
			defer file.Close()

			ext := filepath.Ext(handler.Filename)
			fileName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)

			contentType := handler.Header.Get("Content-Type")
			if contentType == "" {
				contentType = mime.TypeByExtension(ext)
			}

			savedPath, err := storage.Save(r.Context(), fileName, file, handler.Size, contentType)
			if err != nil {
				log.Printf("❌ [uploads] Error guardando %s: %v", fileName, err)
				http.Error(w, "Failed to save image", http.StatusInternalServerError)
				return
			}
			t.ImagenPath = savedPath
		}

		if r.Method == "POST" {
			if err := db.CreateTransaction(s.db, t); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		} else {
			if err := s.updateAndCleanup(r.Context(), t); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

	case "DELETE":
		idStr := r.URL.Query().Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Invalid ID", http.StatusBadRequest)
			return
		}

		// Leer la ruta del comprobante antes de borrar la fila: después ya no
		// habría forma de saber qué archivo quedó huérfano en el bucket.
		existing, findErr := db.GetTransactionByID(s.db, id)

		if err := db.DeleteTransaction(s.db, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// El archivo se borra después de la fila: si el storage falla, la
		// transacción ya se eliminó y solo queda un huérfano, que es preferible
		// a bloquear la operación del usuario.
		if findErr == nil && existing.ImagenPath != "" {
			if err := storage.Delete(r.Context(), existing.ImagenPath); err != nil {
				log.Printf("⚠️  [uploads] Transacción %d eliminada, pero no se pudo borrar %s: %v",
					id, existing.ImagenPath, err)
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSummary procesa métricas globales y mensuales.
// @Summary      Obtener resumen analítico
// @Description  Calcula el balance acumulado general, los gastos del mes corriente en base a la fecha actual y la cantidad de transacciones registradas.
// @Tags         metrics
// @Produce      json
// @Success      200  {object}  map[string]interface{} "Ejemplo: {'total_general': 5000.5, 'total_mes': 1200.0, 'conteo': 14}"
// @Failure      500  {string}  string "Error de servidor al calcular la lista"
// @Router       /api/summary [get]
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	transacciones, err := db.ListTransactions(s.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var total float64
	var totalMes float64
	now := time.Now()
	currentMonth := now.Format("01/2006")

	for _, t := range transacciones {
		total += t.Monto
		// Nota: parseo simple de fecha DD/MM/YYYY
		if strings.HasSuffix(t.FechaPago, currentMonth) {
			totalMes += t.Monto
		}
	}

	summary := map[string]interface{}{
		"total_general": total,
		"total_mes":     totalMes,
		"conteo":        len(transacciones),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// handleSendWhatsApp despacha mensajes concurrentes asíncronos.
// @Summary      Reportar transacción a WhatsApp
// @Description  Encola de forma asíncrona y segura el envío estructurado de un reporte hacia un canal o grupo de WhatsApp parametrizado. Evita duplicados simultáneos.
// @Tags         whatsapp
// @Produce      json
// @Param        id    query     int     true   "ID único de la transacción a reportar"
// @Param        to    query     string  false  "Nombre o JID del destinatario (Por defecto: 'Prueba')"
// @Success      200   {object}  map[string]string "Estado de la transacción en cola: 'queued' o 'processing'"
// @Failure      404   {string}  string "Transacción o Grupo objetivo no encontrado"
// @Failure      503   {string}  string "El cliente de WhatsApp no se encuentra inicializado o conectado"
// @Router       /api/whatsapp/send [post]
func (s *Server) handleSendWhatsApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idStr := r.URL.Query().Get("id")
	id, _ := strconv.Atoi(idStr)

	t, err := db.GetTransactionByID(s.db, id)
	if err != nil {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	s.mu.RLock()
	wa := s.waClient
	s.mu.RUnlock()

	if wa == nil {
		wa = whatsapp.GlobalWA
	}

	if wa == nil {
		http.Error(w, "WhatsApp not connected", http.StatusServiceUnavailable)
		return
	}

	recipient := r.URL.Query().Get("to")
	if recipient == "" {
		recipient = "Prueba"
	}

	var targetJID string
	if !strings.Contains(recipient, "@") {
		jid, err := s.waClient.GetGroupJIDByName(recipient)
		if err != nil {
			log.Printf("❌ Error: Grupo '%s' no encontrado: %v", recipient, err)
			http.Error(w, "Group not found", http.StatusNotFound)
			return
		}
		targetJID = jid.String()
	} else {
		targetJID = recipient
	}

	s.smu.Lock()
	if s.sending[id] {
		s.smu.Unlock()
		log.Printf("⚠️  Envío duplicado omitido para ID %d", id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "processing", "message": "Already sending"})
		return
	}
	s.sending[id] = true
	s.smu.Unlock()

	go func() {
		defer func() {
			s.smu.Lock()
			delete(s.sending, id)
			s.smu.Unlock()
		}()

		err := wa.SendTransaction(targetJID, t)
		if err != nil {
			log.Printf("❌ Error enviando WhatsApp ID %d: %v", id, err)
		} else {
			log.Printf("✅ WhatsApp ID %d enviado con éxito.", id)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
}

// handleWhatsAppStatus audita el estado del cliente de WhatsApp y sus canales vinculados.
// @Summary      Estado de WhatsApp Multi-Device
// @Description  Devuelve el estado actual del puente de comunicación, errores persistentes, cadenas QR activas para emparejamiento o el listado de grupos vinculados.
// @Tags         whatsapp
// @Produce      json
// @Success      200  {object}  map[string]interface{} "Estructura del estado del cliente y grupos vinculados"
// @Router       /api/whatsapp/status [get]
func (s *Server) handleWhatsAppStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	wa := s.waClient
	s.mu.RUnlock()

	if wa == nil {
		wa = whatsapp.GlobalWA
	}

	status := map[string]interface{}{
		"connected": false,
		"qr":        "",
	}

	if wa != nil {
		status["connected"] = wa.IsConnected
		status["qr"] = wa.QRCode
		status["last_error"] = wa.LastError
		if wa.IsConnected {
			groups, _ := wa.GetJoinedGroupsNames()
			status["groups"] = groups
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleWhatsAppLogout revoca credenciales de sesión activa.
// @Summary      Cerrar sesión de WhatsApp y regenerar QR
// @Description  Desvincula de manera remota el dispositivo y levanta de forma asíncrona una nueva rutina para refrescar las llaves criptográficas y servir un QR limpio.
// @Tags         whatsapp
// @Produce      json
// @Success      200  {object}  map[string]string "Confirmación de reinicio de credenciales: {'status': 'resetting'}"
// @Router       /api/whatsapp/logout [post]
func (s *Server) handleWhatsAppLogout(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	wa := s.waClient
	s.mu.RUnlock()

	if wa == nil {
		wa = whatsapp.GlobalWA
	}

	if wa != nil {
		err := wa.Logout()
		if err != nil {
			log.Printf("Error logging out: %v", err)
		}
	}

	fmt.Println("🔄 Reiniciando conexión de WhatsApp para nuevo QR...")
	go func() {
		newClient, err := whatsapp.Connect()
		if err != nil {
			log.Printf("Error reconnecting WhatsApp: %v", err)
			return
		}
		s.waClient = newClient
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "resetting"})
}

// handleExport empaqueta la base de datos completa a Excel format estructurado.
// @Summary      Exportar historial de transacciones a Excel (.xlsx)
// @Description  Sincroniza y descarga una sábana de datos procesada en formato binario legible por Microsoft Excel o plataformas externas de contabilidad.
// @Tags         export
// @Produce      application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
// @Success      200  {file}    binary "Descarga de archivo reporte_brailer.xlsx"
// @Failure      500  {string}  string "Error al construir dinámicamente la estructura Excel"
// @Router       /api/export [get]
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	transacciones, err := db.ListTransactions(s.db)
	if err != nil {
		http.Error(w, "Failed to fetch transactions", http.StatusInternalServerError)
		return
	}

	f, err := export.GenerateExcel(transacciones)
	if err != nil {
		http.Error(w, "Failed to generate Excel", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=reporte_brailer.xlsx")

	if err := f.Write(w); err != nil {
		log.Printf("Error writing Excel to response: %v", err)
	}
}
