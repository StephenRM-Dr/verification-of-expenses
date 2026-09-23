// Package storage abstracts where uploaded receipts live.
//
// If S3 credentials are configured via the environment, files go to an external
// bucket (Cloudflare R2, AWS S3, Backblaze B2, Neon Object Storage...). Otherwise,
// they fall back to the local disk, which is the historical behavior and remains valid
// for development. In both cases the path stored in the database keeps
// the "/uploads/<file>" format, so neither the frontend nor the existing rows
// need to change.
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

const localDir = "uploads"

var (
	client *minio.Client
	bucket string
)

// Init configures the storage backend by reading the environment. It returns without
// error if there is no S3 configuration: in that case the local disk is used.
func Init() error {
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return fmt.Errorf("could not create %s: %w", localDir, err)
	}

	endpoint := firstEnv("S3_ENDPOINT", "AWS_ENDPOINT_URL_S3")
	bucket = firstEnv("S3_BUCKET", "AWS_BUCKET")
	accessKey := firstEnv("S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	secretKey := firstEnv("S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")

	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		log.Println("💾 [storage] No S3 configuration: using local disk (files do NOT survive a restart in the cloud)")
		return nil
	}

	// The SDK expects the host without a scheme and decides TLS with the secure flag.
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
		// Neon and R2 require path-style addressing; classic S3 accepts it.
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return fmt.Errorf("could not create the S3 client: %w", err)
	}

	client = c
	log.Printf("☁️  [storage] S3 activo — bucket %q en %s", bucket, host)
	return nil
}

// UsingS3 reports whether new files are saved to the bucket.
func UsingS3() bool { return client != nil }

// Save persists the file and returns the path to store in the database.
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

	// size -1 lets the SDK do multipart when the size is unknown.
	if size == 0 {
		size = -1
	}
	_, err := client.PutObject(ctx, bucket, name, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("could not upload %s to the bucket: %w", name, err)
	}
	return "/uploads/" + name, nil
}

// Open returns the file contents, whether it comes from the bucket or the disk.
// Accepts both "/uploads/file.png" and the bare name.
func Open(ctx context.Context, ref string) (io.ReadCloser, error) {
	name := Name(ref)
	if client == nil {
		return os.Open(filepath.Join(localDir, name))
	}

	obj, err := client.GetObject(ctx, bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// GetObject is lazy: the real error (including NoSuchKey) shows up on read,
	// so we force a Stat to avoid returning an unusable reader.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}

// Delete removes the file from the bucket or the disk. A missing file is not an error:
// deleting a transaction whose receipt was already lost must still work.
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

// List returns the names of the stored files. It lets you learn in a single
// pass which receipts exist, instead of querying them one by one.
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

// Name extracts the file name from a "/uploads/x.png" path or from a local path.
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
