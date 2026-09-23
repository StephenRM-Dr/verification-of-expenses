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

	"github.com/stephenrm-dr/verification-of-expenses/internal/models"
	"github.com/stephenrm-dr/verification-of-expenses/internal/storage"
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
	// Trick: register modernc as "sqlite3" to satisfy whatsmeow
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

// Connect establishes the WhatsApp connection, keeping the session in PostgreSQL if available, or SQLite as a fallback.
func Connect() (*WAClient, error) {
	if waContainer == nil {
		dbLog := waLog.Stdout("Database", "ERROR", true)
		
		var driver string
		var dsn string

		// Try to use PostgreSQL (Neon) for cloud persistence
		pgUrl := os.Getenv("DATABASE_URL")
		if pgUrl != "" {
			// USAR PGX PARA MEJOR COMPATIBILIDAD CON POOLERS Y BINARY DATA
			driver = "pgx"
			dsn = sanitizeDSN(pgUrl)
			fmt.Printf("🚀 WhatsApp: Using PostgreSQL (pgx) for session persistence. (Host: %s)\n", getHostFromDSN(dsn))
		} else {
			// Fallback local a SQLite
			dbPath := os.Getenv("WA_DB_PATH")
			if dbPath == "" {
				dbPath = "whatsapp_final_v6.db"
			}
			driver = "sqlite3"
			dsn = fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&cache=shared", dbPath)
			fmt.Println("💻 WhatsApp: Using local SQLite for session persistence.")
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
		// No session: capture the QR
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
					fmt.Println("✅ Pairing successful via QR.")
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
		fmt.Printf("✅ WhatsApp reconnected automatically as %s.\n", deviceStore.ID.String())
	}

	return wa, nil
}

// Logout permanently closes the current session (unlinks the device).
func (wa *WAClient) Logout() error {
	if wa.Client == nil {
		return fmt.Errorf("client not initialized")
	}
	
	// Unpair unlinks the device from the WA servers
	err := wa.Client.Logout(context.Background())
	if err != nil {
		fmt.Printf("⚠️ Error during logout (unpair): %v\n", err)
		// Intentamos desconectar igual
	}
	
	wa.Client.Disconnect()
	wa.IsConnected = false
	wa.QRCode = ""
	GlobalWA = nil
	return nil
}

// readAttachment loads the receipt from the active storage backend.
func readAttachment(fileName string) ([]byte, error) {
	f, err := storage.Open(context.Background(), fileName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// SendTransaction sends a structured transaction to a recipient (JID).
func (wa *WAClient) SendTransaction(recipient string, t models.Transaccion) error {
	fmt.Printf("📩 [WA] Starting process for ID %d -> Recipient: %s\n", t.ID, recipient)
	jid, err := types.ParseJID(recipient)
	if err != nil {
		fmt.Printf("❌ [WA] Error parsing JID '%s': %v\n", recipient, err)
		return err
	}

	messageText := fmt.Sprintf("*NEW TRANSACTION - VERIFICATION OF EXPENSES*\n\n"+
		"*Date:* %s\n"+
		"*Description:* %s\n"+
		"*Amount:* %.2f\n"+
		"*Reference:* %s\n"+
		"*Bank:* %s\n"+
		"*City:* %s\n",
		t.FechaPago, t.Descripcion, t.Monto, t.Referencia, t.Banco, t.Ciudad)

	// If there is an image, try to send it
	if t.ImagenPath != "" {
		fileName := storage.Name(t.ImagenPath)

		fmt.Printf("🔍 [WA] Looking for receipt: %s\n", fileName)
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

			fmt.Printf("📤 [WA] Uploading file (%s) to WhatsApp (%d bytes)...\n", mediaType, len(data))
			resp, err := wa.Client.Upload(context.Background(), data, mediaType)
			if err != nil {
				fmt.Printf("⚠️  [WA] Error uploading file (ID %d): %v. Retrying text only...\n", t.ID, err)
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
				
				fmt.Printf("📤 [WA] Sending message with attachment (ID %d)...\n", t.ID)
				_, err = wa.Client.SendMessage(context.Background(), jid, &msg)
				if err == nil {
					fmt.Printf("✅ [WA] Message with attachment (ID %d) sent successfully.\n", t.ID)
					return nil
				}
				fmt.Printf("⚠️  [WA] Error sending attachment (ID %d): %v. Retrying text only...\n", t.ID, err)
			}
		} else {
			fmt.Printf("⚠️  [WA] Could not read the file (ID %d): %v. File: %s\n", t.ID, err, fileName)
		}
	}

	// Fallback: if there is no image, or the send or upload failed, send text only
	fmt.Printf("📤 [WA] Sending text message (ID %d)...\n", t.ID)
	_, err = wa.Client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: proto.String(messageText),
	})
	if err == nil {
		fmt.Printf("✅ [WA] Text message (ID %d) sent successfully.\n", t.ID)
	} else {
		fmt.Printf("❌ [WA] Final error sending ID %d: %v\n", t.ID, err)
	}
	return err
}

// GetGroupJIDByName looks up a group's JID by its name.
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

	return types.EmptyJID, fmt.Errorf("group '%s' not found", name)
}

// GetJoinedGroupsNames returns a list of joined group names.
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

// sanitizeDSN removes the '-pooler' suffix from Neon hosts for WhatsApp,
// since whatsmeow requires Session Mode to correctly handle prepared statements and cryptographic keys.
func sanitizeDSN(dsn string) string {
	if strings.Contains(dsn, "-pooler") {
		return strings.Replace(dsn, "-pooler", "", 1)
	}
	return dsn
}

// getHostFromDSN extracts the hostname for logging purposes
func getHostFromDSN(dsn string) string {
	parts := strings.Split(dsn, "@")
	if len(parts) > 1 {
		hostPart := strings.Split(parts[1], "/")[0]
		return hostPart
	}
	return "unknown"
}
