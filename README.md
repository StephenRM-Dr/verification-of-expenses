# Brailer Ledger Backend (Golang)

Este es el backend principal del sistema **Brailer Ledger**, una aplicación financiera minimalista que permite gestionar transacciones, generar reportes en Excel y enviar notificaciones directamente a grupos de WhatsApp.

## Tecnologías Utilizadas
- **Lenguaje:** Go 1.25.0
- **Base de Datos:** PostgreSQL (usando `github.com/jackc/pgx/v5`)
- **Integración WhatsApp:** `go.mau.fi/whatsmeow` (basado en Signal Protocol)
- **Exportación a Excel:** `github.com/xuri/excelize/v2`

## Requisitos Previos
1. Instalar [Go 1.25+](https://go.dev/dl/).
2. Acceso a una base de datos PostgreSQL (local o en la nube como Neon DB).
3. (Opcional) WhatsApp vinculado para envío de reportes.

## Configuración y Ejecución Local

1. **Clonar y descargar dependencias:**
   ```bash
   go mod download
   ```

2. **Variables de Entorno (Opcional pero recomendado):**
   Puedes configurar las variables directamente en tu terminal o sistema antes de ejecutar:
   - `DATABASE_URL`: Cadena de conexión a PostgreSQL. Si está vacía, usará la conexión por defecto hacia Neon DB.
   - `PORT`: Puerto para el servidor de la API (por defecto: `8080`).

3. **Ejecutar la aplicación:**
   ```bash
   go run main.go
   ```
   *Nota: La aplicación iniciará un menú interactivo en la terminal si se detecta un entorno TTY, y a su vez levantará un servidor API en segundo plano en el puerto 8080.*

4. **Compilar para producción:**
   ```bash
   go build -v -o main .
   ./main
   ```

## Integración con WhatsApp
Al iniciar, la aplicación intentará conectarse a WhatsApp de forma asíncrona. Si es la primera vez que se ejecuta (y no existe la base de datos de sesión), se imprimirá un código QR en la consola de la API o se podrá obtener mediante el endpoint `/api/whatsapp/status` para vincular tu dispositivo desde la app de WhatsApp.

## Solución de Problemas Frecuentes

1. **Error: "Failed to connect to database"**
   - Verifica que la variable `DATABASE_URL` sea correcta. Si usas Neon DB, asegúrate de añadir `sslmode=require`.
   - Verifica tu conexión a internet o firewall si estás apuntando a una DB externa.

2. **El envío de WhatsApp falla o marca "not connected"**
   - Asegúrate de haber escaneado el código QR correctamente.
   - Si la sesión parece corrupta, elimina los archivos `.db` generados por whatsmeow (`whatsapp_final_vX.db`) en la raíz del proyecto y reinicia la app para escanear de nuevo.

3. **CORS o errores de API desde el Frontend**
   - Asegúrate de que el backend está corriendo en la IP correcta. En `main.go`, el middleware de CORS está configurado para aceptar todas las peticiones (`*`), pero verifica que no haya un firewall bloqueando el puerto `8080`.

4. **El servidor se detiene en Cloud (Railway/Koyeb)**
   - El archivo `main.go` incluye un bucle para evitar cierres prematuros en modo no interactivo. Asegúrate de configurar correctamente el `PORT` en los variables de tu plataforma en la nube.
