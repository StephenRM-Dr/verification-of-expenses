# 🚀 Despliegue en Railway - Brailer Ledger

Este documento detalla los pasos para desplegar el sistema completo (Frontend y Backend) en **Railway** como un monorepo, asegurando la persistencia de la sesión de WhatsApp.

## 1. Estructura del Monorepo en Railway

Railway detectará automáticamente los dos servicios si vinculas tu repositorio de GitHub. Debes configurar dos servicios independientes en el mismo proyecto:

### A. Servicio Backend (Go)
1.  **Directorio Raíz (Root Directory):** `/brailer`
2.  **Dockerfile:** Detectado automáticamente en la carpeta.
3.  **Variables de Entorno:**
    *   `DATABASE_URL`: `postgresql://neondb_owner:npg_1Gm3NEDXZHtk@ep-lucky-grass-acx9h4bp-pooler.sa-east-1.aws.neon.tech/neondb?sslmode=require&channel_binding=require`
    *   `PORT`: `8080` (Railway lo asigna automáticamente, el código lo lee).
    *   `WA_DB_PATH`: `/data/whatsapp.db` (Importante para persistencia).
4.  **Volúmenes (Volumes):**
    *   Crea un volumen montado en `/data` para que el archivo `whatsapp.db` persista entre reinicios.
5.  **Dominio:** Railway te dará una URL (ej: `backend-brailer.up.railway.app`). **Cópiala**.

### B. Servicio Frontend (Next.js)
1.  **Directorio Raíz (Root Directory):** `/brailer-web`
2.  **Variables de Entorno:**
    *   `NEXT_PUBLIC_API_URL`: La URL de tu servicio de backend (ej: `https://backend-brailer.up.railway.app/api`).
3.  **Build Command:** `npm run build`
4.  **Start Command:** `npm run start`

---

## 2. Optimizaciones Implementadas para la Nube

*   **Persistencia de WhatsApp:** El código ahora busca una variable `WA_DB_PATH`. Si montas un volumen en Railway, no tendrás que escanear el QR cada vez que se despliegue una nueva versión.
*   **Modo No Interactivo:** El backend de Go ahora detecta si está en un contenedor y desactiva el menú de terminal para evitar crashes.
*   **CORS Dinámico:** El backend permite peticiones desde cualquier origen, facilitando la conexión con el dominio de Vercel o Railway.
*   **Pegado de Imágenes (Ctrl+V):** Ahora puedes pegar comprobantes directamente en el formulario sin buscarlos en archivos.
*   **Edición Total:** Puedes corregir montos, fechas o bancos directamente desde el historial.

---

## 3. Próximos Pasos Recomendados

1.  **Neon DB:** Asegúrate de que la base de datos de Neon tenga las tablas creadas (el código actual se encarga de registrarlas si no existen).
2.  **WhatsApp Cloud:** Si el tráfico crece, considera mover las imágenes de `cargas-brailer` a un servicio como **Cloudinary** para no depender de almacenamiento local en el contenedor.

¡Brailer Ledger está listo para el mundo! 🚀
