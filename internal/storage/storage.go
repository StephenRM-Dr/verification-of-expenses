// Package storage abstrae dónde viven los comprobantes subidos.
//
// Si hay credenciales S3 configuradas por entorno, los archivos van a un bucket
// externo (Cloudflare R2, AWS S3, Backblaze B2, Neon Object Storage...). Si no,
// caen al disco local, que es el comportamiento histórico y sigue siendo válido
// para desarrollo. En ambos casos la ruta guardada en la base de datos mantiene
// el formato "/uploads/<archivo>", de modo que ni el frontend ni las filas
// existentes necesitan cambiar.
package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const localDir = "cargas-brailer"

var (
	client *minio.Client
	bucket string
)

// Init configura el backend de almacenamiento leyendo el entorno. Devuelve sin
// error si no hay configuración S3: en ese caso se usa el disco local.
func Init() error {
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return fmt.Errorf("no se pudo crear %s: %w", localDir, err)
	}

	endpoint := firstEnv("S3_ENDPOINT", "AWS_ENDPOINT_URL_S3")
	bucket = firstEnv("S3_BUCKET", "AWS_BUCKET")
	accessKey := firstEnv("S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	secretKey := firstEnv("S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")

	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		log.Println("💾 [storage] Sin configuración S3: usando disco local (los archivos NO sobreviven a un reinicio en cloud)")
		return nil
	}

	// El SDK espera el host sin esquema y decide TLS con la bandera secure.
	secure := !strings.HasPrefix(endpoint, "http://")
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	host = strings.TrimSuffix(host, "/")

	region := firstEnv("S3_REGION", "AWS_REGION")
	if region == "" {
		region = "auto"
	}

	c, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: region,
		// Neon y R2 requieren direccionamiento path-style; S3 clásico lo acepta.
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return fmt.Errorf("no se pudo crear el cliente S3: %w", err)
	}

	client = c
	log.Printf("☁️  [storage] S3 activo — bucket %q en %s", bucket, host)
	return nil
}

// UsingS3 indica si los archivos nuevos se guardan en el bucket.
func UsingS3() bool { return client != nil }

// Save persiste el archivo y devuelve la ruta a guardar en la base de datos.
func Save(ctx context.Context, name string, r io.Reader, size int64, contentType string) (string, error) {
	if client == nil {
		dst, err := os.Create(filepath.Join(localDir, name))
		if err != nil {
			return "", err
		}
		defer dst.Close()
		if _, err := io.Copy(dst, r); err != nil {
			return "", err
		}
		return "/uploads/" + name, nil
	}

	// size -1 deja que el SDK haga multipart cuando no se conoce el tamaño.
	if size == 0 {
		size = -1
	}
	_, err := client.PutObject(ctx, bucket, name, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("no se pudo subir %s al bucket: %w", name, err)
	}
	return "/uploads/" + name, nil
}

// Open devuelve el contenido del archivo, venga del bucket o del disco.
// Acepta tanto "/uploads/archivo.png" como el nombre suelto.
func Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	name := Name(ref)
	if client == nil {
		return os.Open(filepath.Join(localDir, name))
	}

	obj, err := client.GetObject(ctx, bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject es perezoso: el error real (incluido NoSuchKey) aparece al leer,
	// así que forzamos un Stat para no devolver un lector inservible.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}

// Delete elimina el archivo del bucket o del disco. Que no exista no es error:
// borrar una transacción cuyo comprobante ya se había perdido debe funcionar igual.
func Delete(ctx context.Context, ref string) error {
	name := Name(ref)
	if name == "" || name == "." {
		return nil
	}

	if client == nil {
		if err := os.Remove(filepath.Join(localDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	return client.RemoveObject(ctx, bucket, name, minio.RemoveObjectOptions{})
}

// List devuelve los nombres de los archivos almacenados. Sirve para saber de una
// sola pasada qué comprobantes existen, en vez de consultarlos uno por uno.
func List(ctx context.Context) (map[string]bool, error) {
	nombres := make(map[string]bool)

	if client == nil {
		entradas, err := os.ReadDir(localDir)
		if err != nil {
			return nil, err
		}
		for _, e := range entradas {
			if !e.IsDir() {
				nombres[e.Name()] = true
			}
		}
		return nombres, nil
	}

	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		nombres[obj.Key] = true
	}
	return nombres, nil
}

// Name extrae el nombre de archivo de una ruta "/uploads/x.png" o de una ruta local.
func Name(ref string) string {
	if i := strings.LastIndex(ref, "/uploads/"); i >= 0 {
		ref = ref[i+len("/uploads/"):]
	}
	return filepath.Base(filepath.FromSlash(ref))
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}
