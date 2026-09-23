package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"example.com/m/v2/internal/models"
	"example.com/m/v2/internal/storage"
	"github.com/mdp/qrterminal/v3"
	_ "github.com/jackc/pgx/v5/stdlib" // Driver de Postgres (pgx)
	_ "github.com/lib/pq"              // Driver de Postgres (alternativo)
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

func init() {
	// Truco: Registrar modernc como "sqlite3" para satisfacer a whatsmeow
	sql.Register("sqlite3", &sqlite.Driver{})
}

type WAClient struct {
	Client      *whatsmeow.Client
	QRCode      string
	IsConnected bool
	LastError   string
}

var (
	GlobalWA      *WAClient
	waContainer   *sqlstore.Container
)

// Connect establece la conexión con WhatsApp, manejando la sesión en PostgreSQL si está disponible, o SQLite como fallback.
func Connect() (*WAClient, error) {
	if waContainer == nil {
		dbLog := waLog.Stdout("Database", "ERROR", true)
		
		var driver string
		var dsn string

		// Intentar usar PostgreSQL (Neon) para persistencia en la nube
		pgUrl := os.Getenv("DATABASE_URL")
		if pgUrl != "" {
			// USAR PGX PARA MEJOR COMPATIBILIDAD CON POOLERS Y BINARY DATA
			driver = "pgx"
			dsn = sanitizeDSN(pgUrl)
			fmt.Printf("🚀 WhatsApp: Usando PostgreSQL (pgx) para persistencia de sesión. (Host: %s)\n", getHostFromDSN(dsn))
		} else {
			// Fallback local a SQLite
			dbPath := os.Getenv("WA_DB_PATH")
			if dbPath == "" {
				dbPath = "whatsapp_final_v6.db"
			}
			driver = "sqlite3"
			dsn = fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&cache=shared", dbPath)
			fmt.Println("💻 WhatsApp: Usando SQLite local para persistencia de sesión.")
		}
		
		container, err := sqlstore.New(context.Background(), driver, dsn, dbLog)
		if err != nil {
			return nil, err
		}
		waContainer = container
	}
	
	deviceStore, err := waContainer.GetFirstDevice(context.Background())
	if err != nil {
		return nil, err
	}
	clientLog := waLog.Stdout("Client", "ERROR", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)

	wa := &WAClient{Client: client}
	GlobalWA = wa

	if client.Store.ID == nil {
		// No hay sesión: capturar QR
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			return nil, err
		}
		
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					wa.QRCode = evt.Code
					wa.IsConnected = false
					fmt.Println("\n📸 NUEVO QR GENERADO (Disponible en Web/Terminal)")
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				} else if evt.Event == "success" {
					wa.QRCode = ""
					wa.IsConnected = true
					fmt.Println("✅ Vinculación exitosa via QR.")
				}
			}
		}()
	} else {
		err = client.Connect()
		if err != nil {
			wa.LastError = err.Error()
			return nil, err
		}
		wa.IsConnected = true
		wa.LastError = ""
		fmt.Printf("✅ WhatsApp reconectado automáticamente as %s.\n", deviceStore.ID.String())
	}

	return wa, nil
}

// Logout cierra la sesión actual de forma permanente (desvincula el dispositivo).
func (wa *WAClient) Logout() error {
	if wa.Client == nil {
		return fmt.Errorf("cliente no inicializado")
	}
	
	// Unpair desvincula el dispositivo de los servidores de WA
	err := wa.Client.Logout(context.Background())
	if err != nil {
		fmt.Printf("⚠️ Error durante Logout (unpair): %v\n", err)
		// Intentamos desconectar igual
	}
	
	wa.Client.Disconnect()
	wa.IsConnected = false
	wa.QRCode = ""
	GlobalWA = nil
	return nil
}

// readAttachment carga el comprobante desde el backend de almacenamiento activo.
func readAttachment(fileName string) ([]byte, error) {
	f, err := storage.Open(context.Background(), fileName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// SendTransaction envía una transacción estructurada a un destinatario (JID).
func (wa *WAClient) SendTransaction(recipient string, t models.Transaccion) error {
	fmt.Printf("📩 [WA] Iniciando proceso para ID %d -> Destinatario: %s\n", t.ID, recipient)
	jid, err := types.ParseJID(recipient)
	if err != nil {
		fmt.Printf("❌ [WA] Error parseando JID '%s': %v\n", recipient, err)
		return err
	}

	messageText := fmt.Sprintf("*NUEVO REGISTRO - BRAILER LEDGER*\n\n"+
		"*Fecha:* %s\n"+
		"*Descripción:* %s\n"+
		"*Monto:* Bs. %.2f\n"+
		"*Referencia:* %s\n"+
		"*Banco:* %s\n"+
		"*Ciudad:* %s\n",
		t.FechaPago, t.Descripcion, t.Monto, t.Referencia, t.Banco, t.Ciudad)

	// Si hay imagen, intentar enviarla
	if t.ImagenPath != "" {
		fileName := storage.Name(t.ImagenPath)

		fmt.Printf("🔍 [WA] Buscando comprobante: %s\n", fileName)
		data, err := readAttachment(fileName)
		if err == nil {
			ext := strings.ToLower(filepath.Ext(fileName))
			mimeType := mime.TypeByExtension(ext)
			if mimeType == "" || mimeType == "application/octet-stream" {
				switch ext {
				case ".pdf":
					mimeType = "application/pdf"
				case ".rar":
					mimeType = "application/x-rar-compressed"
				case ".zip":
					mimeType = "application/zip"
				case ".7z":
					mimeType = "application/x-7z-compressed"
				default:
					if mimeType == "" {
						mimeType = http.DetectContentType(data)
					}
				}
			}

			// Determinar si es imagen o documento
			isImage := strings.HasPrefix(mimeType, "image/")
			mediaType := whatsmeow.MediaImage
			if !isImage {
				mediaType = whatsmeow.MediaDocument
			}

			fmt.Printf("📤 [WA] Subiendo archivo (%s) a WhatsApp (%d bytes)...\n", mediaType, len(data))
			resp, err := wa.Client.Upload(context.Background(), data, mediaType)
			if err != nil {
				fmt.Printf("⚠️  [WA] Error al subir archivo (ID %d): %v. Reintentando solo texto...\n", t.ID, err)
			} else {
				fmt.Printf("✅ [WA] Archivo subido. URL: %s\n", resp.URL)

				var msg waE2E.Message
				if isImage {
					msg.ImageMessage = &waE2E.ImageMessage{
						Caption:       proto.String(messageText),
						Mimetype:      proto.String(mimeType),
						URL:           proto.String(resp.URL),
						DirectPath:    proto.String(resp.DirectPath),
						MediaKey:      resp.MediaKey,
						FileLength:    proto.Uint64(uint64(len(data))),
						FileSHA256:    resp.FileSHA256,
						FileEncSHA256: resp.FileEncSHA256,
					}
				} else {
					// Para documentos (PDF, RAR, ZIP)
					msg.DocumentMessage = &waE2E.DocumentMessage{
						Caption:       proto.String(messageText),
						Mimetype:      proto.String(mimeType),
						URL:           proto.String(resp.URL),
						DirectPath:    proto.String(resp.DirectPath),
						MediaKey:      resp.MediaKey,
						FileLength:    proto.Uint64(uint64(len(data))),
						FileSHA256:    resp.FileSHA256,
						FileEncSHA256: resp.FileEncSHA256,
						FileName:      proto.String(fileName),
					}
				}
				
				fmt.Printf("📤 [WA] Enviando mensaje con adjunto (ID %d)...\n", t.ID)
				_, err = wa.Client.SendMessage(context.Background(), jid, &msg)
				if err == nil {
					fmt.Printf("✅ [WA] Mensaje con adjunto (ID %d) enviado con éxito.\n", t.ID)
					return nil
				}
				fmt.Printf("⚠️  [WA] Error enviando adjunto (ID %d): %v. Reintentando solo texto...\n", t.ID, err)
			}
		} else {
			fmt.Printf("⚠️  [WA] No se pudo leer el archivo (ID %d): %v. Archivo: %s\n", t.ID, err, fileName)
		}
	}

	// Fallback: Si no hay imagen, falló el envío o falló la subida, enviar solo texto
	fmt.Printf("📤 [WA] Enviando mensaje de texto (ID %d)...\n", t.ID)
	_, err = wa.Client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(messageText),
	})
	if err == nil {
		fmt.Printf("✅ [WA] Mensaje de texto (ID %d) enviado con éxito.\n", t.ID)
	} else {
		fmt.Printf("❌ [WA] Error final enviando ID %d: %v\n", t.ID, err)
	}
	return err
}

// GetGroupJIDByName busca el JID de un grupo basándose en su nombre.
func (wa *WAClient) GetGroupJIDByName(name string) (types.JID, error) {
	groups, err := wa.Client.GetJoinedGroups(context.Background())
	if err != nil {
		return types.EmptyJID, err
	}

	for _, group := range groups {
		if group.Name == name {
			return group.JID, nil
		}
	}

	return types.EmptyJID, fmt.Errorf("grupo '%s' no encontrado", name)
}

// GetJoinedGroupsNames devuelve una lista de nombres de grupos unidos.
func (wa *WAClient) GetJoinedGroupsNames() ([]string, error) {
	groups, err := wa.Client.GetJoinedGroups(context.Background())
	if err != nil {
		return nil, err
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	return names, nil
}

// sanitizeDSN elimina el sufijo '-pooler' de los hosts de Neon para WhatsApp, 
// ya que whatsmeow requiere Session Mode para manejar correctamente los prepared statements y llaves criptográficas.
func sanitizeDSN(dsn string) string {
	if strings.Contains(dsn, "-pooler") {
		return strings.Replace(dsn, "-pooler", "", 1)
	}
	return dsn
}

// getHostFromDSN extrae el hostname para fines de logging
func getHostFromDSN(dsn string) string {
	parts := strings.Split(dsn, "@")
	if len(parts) > 1 {
		hostPart := strings.Split(parts[1], "/")[0]
		return hostPart
	}
	return "unknown"
}
