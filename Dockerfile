FROM golang:1.25-alpine

WORKDIR /app

# Instalar dependencias necesarias
RUN apk add --no-cache ca-certificates git

# Copiamos las dependencias desde la raíz
COPY go.mod go.sum ./

# Permitimos que Go descargue versiones más recientes de sí mismo si el proyecto lo requiere (ej. Go 1.25)
ENV GOTOOLCHAIN=auto

RUN go mod download

# Copiamos todo el contenido del backend (ya está en la raíz)
COPY . .

# Deshabilitar CGO para máxima compatibilidad cloud
ENV CGO_ENABLED=0

# Compilar la aplicación
RUN go build -v -o main .

# Crear directorio para subidas y datos temporales
RUN mkdir -p cargas-brailer

# Koyeb usa el puerto 8080 en tu configuración actual
EXPOSE 8080
ENV PORT=8080

# Ejecutar el binario compilado
CMD ["./main"]
