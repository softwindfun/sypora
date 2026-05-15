package s3client

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"sypora/internal/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type ObjectInfo struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
}

type Client struct {
	mc     *minio.Client
	cfg    config.S3Config
	bucket string
}

func New(cfg config.S3Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client create: %w", err)
	}

	return &Client{mc: mc, cfg: cfg, bucket: cfg.Bucket}, nil
}

func (c *Client) TestConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", c.bucket)
	}
	return nil
}

func (c *Client) Upload(localPath, remoteKey string) (string, error) {
	ctx := context.Background()

	info, err := c.mc.FPutObject(ctx, c.bucket, remoteKey, localPath, minio.PutObjectOptions{})
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", remoteKey, err)
	}
	return info.ETag, nil
}

func (c *Client) Download(remoteKey, localPath string) error {
	ctx := context.Background()

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("mkdir for download: %w", err)
	}

	if err := c.mc.FGetObject(ctx, c.bucket, remoteKey, localPath, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("download %s: %w", remoteKey, err)
	}
	return nil
}

func (c *Client) DownloadToWriter(remoteKey string, w io.WriterAt) (int64, error) {
	ctx := context.Background()

	obj, err := c.mc.GetObject(ctx, c.bucket, remoteKey, minio.GetObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("get object %s: %w", remoteKey, err)
	}
	defer obj.Close()

	n, err := io.Copy(io.Discard, obj)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (c *Client) ListObjects(prefix string) ([]ObjectInfo, error) {
	ctx := context.Background()

	var objects []ObjectInfo
	for obj := range c.mc.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list objects: %w", obj.Err)
		}
		objects = append(objects, ObjectInfo{
			Key:          obj.Key,
			Size:         obj.Size,
			ETag:         obj.ETag,
			LastModified: obj.LastModified,
		})
	}
	return objects, nil
}

func (c *Client) DeleteObject(remoteKey string) error {
	ctx := context.Background()

	if err := c.mc.RemoveObject(ctx, c.bucket, remoteKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete %s: %w", remoteKey, err)
	}
	return nil
}

func (c *Client) ObjectInfo(remoteKey string) (*ObjectInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := c.mc.StatObject(ctx, c.bucket, remoteKey, minio.StatObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", remoteKey, err)
	}
	etag := info.ETag
	// minio client may return ETag wrapped in quotes
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		etag = etag[1 : len(etag)-1]
	}
	return &ObjectInfo{
		Key:          info.Key,
		Size:         info.Size,
		ETag:         etag,
		LastModified: info.LastModified,
	}, nil
}
