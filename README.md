# Verification of Expenses (Go backend)

Backend for a minimalist financial ledger: record transactions with receipt images, export reports to Excel, and send notifications to WhatsApp groups. It exposes a REST API and, when run in a terminal, an interactive CLI menu.

> All configuration in this repository is placeholder-only. Provide your own credentials through environment variables (see `.env.example`).

## Tech Stack
- **Language:** Go 1.25
- **Database:** PostgreSQL (`github.com/jackc/pgx/v5`)
- **WhatsApp integration:** `go.mau.fi/whatsmeow`
- **Excel export:** `github.com/xuri/excelize/v2`
- **Receipt storage:** S3-compatible object storage (Cloudflare R2, AWS S3, Backblaze B2, Neon Object Storage) with local-disk fallback

## Prerequisites
1. [Go 1.25+](https://go.dev/dl/)
2. A PostgreSQL database (local or hosted, e.g. Neon)
3. (Optional) A WhatsApp account to link for sending reports

## Getting Started

1. **Install dependencies**
   ```bash
   go mod download
   ```

2. **Configure the environment**
   ```bash
   cp .env.example .env
   ```
   Then edit `.env`:
   - `DATABASE_URL` (**required**): PostgreSQL connection string. The app exits at startup if it is missing. For hosted databases such as Neon, append `sslmode=require`.
   - `PORT`: API server port (default `8080`).
   - `AWS_*` / `S3_*` (optional): S3-compatible storage credentials for receipts. If unset, files are stored in `./cargas-brailer`. That directory is ephemeral on most cloud platforms, so configure S3 storage for production.

3. **Run**
   ```bash
   go run main.go
   ```
   In a terminal (TTY) the app shows an interactive menu and runs the API server in the background. In non-interactive environments (Docker, cloud) it runs the API server only.

4. **Build for production**
   ```bash
   go build -v -o main .
   ./main
   ```

## WhatsApp Integration
On startup the app connects to WhatsApp asynchronously. On the first run, when no session exists, a QR code is printed to the console (it can also be fetched from the `/api/whatsapp/status` endpoint) so you can link your device from the WhatsApp app.

## API Documentation
Swagger docs are in the `docs/` directory (`swagger.json` / `swagger.yaml`). The `host` field is a placeholder (`api.example.com`); change the `@host` annotation in `main.go` and regenerate with `swag init` for your deployment.

## Deployment
A `Dockerfile` and a `railway.json` are included. Set `DATABASE_URL` (and optionally the S3 variables) in your platform's environment; the app listens on `PORT` (default `8080`).

## Troubleshooting

1. **"Falta DATABASE_URL" / "Failed to connect to database"**
   - Make sure `DATABASE_URL` is set in the environment or in `.env`, and that it is valid.
   - Check your network or firewall if the database is remote.

2. **WhatsApp fails to send or reports "not connected"**
   - Confirm you scanned the QR code.
   - If the session looks corrupt, delete the whatsmeow session `.db` files in the project root and restart to link again.

3. **CORS or API errors from a frontend**
   - The CORS middleware accepts all origins (`*`). Check that no firewall is blocking the API port.

4. **Server stops on a cloud platform (Railway/Koyeb)**
   - In non-interactive mode `main.go` blocks to keep the process alive. Make sure `PORT` is set correctly in your platform's environment variables.
