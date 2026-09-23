# 🚀 Despliegue Premium (Gratis): Vercel + Koyeb

Esta configuración permite que el **Frontend** viva en Vercel (Gratis) y el **Backend** en Koyeb (Gratis), usando **Neon** como cerebro para no perder nunca la sesión de WhatsApp.

## 1. Backend en Koyeb (Go)

Koyeb es ideal porque permite procesos persistentes. Gracias a que migramos la sesión de WhatsApp a PostgreSQL (Neon), el backend no necesita disco duro local.

### Pasos en Koyeb:
1.  **Crear Servicio:** Conecta tu GitHub y elige el repositorio.
2.  **Configuración del Servicio:**
    *   **Root Directory:** `/brailer`
    *   **Builder:** Docker (detectará el Dockerfile).
    *   **Instance Size:** Nano (Capa gratuita).
3.  **Variables de Entorno:**
    *   `DATABASE_URL`: Tu URL completa de Neon (PostgreSQL).
    *   `WA_DB_PATH`: `/data/whatsapp.db`.
    *   `PORT`: `8080`.
4.  **Dominio:** Copia la URL que te asigne Koyeb (ej. `brailer-api-user.koyeb.app`).

## 2. Frontend en Vercel (Next.js)

Vercel es el mejor lugar para Next.js. El despliegue es automático.

### Pasos en Vercel:
1.  **Importar Proyecto:** Elige tu repositorio.
2.  **Configuración:**
    *   **Root Directory:** `brailer-web`
    *   **Framework Preset:** Next.js.
3.  **Variables de Entorno (CRÍTICO):**
    *   `NEXT_PUBLIC_API_URL`: Pega la URL de Koyeb (ej. `https://brailer-api-user.koyeb.app/api`).
4.  **Desplegar:** Haz clic en Deploy.

## 3. Ventajas de esta Configuración

- **WhatsApp Eterno:** Al guardar la sesión en Neon, nunca tendrás que re-escanear el QR aunque Koyeb reinicie el servidor.
- **Cero Costo:** Ambas plataformas tienen capas gratuitas generosas.
- **Velocidad:** Vercel ofrece una latencia mínima para la interfaz de usuario.

---
*Nota: Si necesitas que el backend esté encendido 24/7 sin que entre en "hibernación", Koyeb es excelente. Si usas Render, recuerda que se duerme tras 15 minutos de inactividad.*
