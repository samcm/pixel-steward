package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3 struct {
	client *minio.Client
	bucket string
	region string
	mu     sync.Mutex
	ready  bool
}

type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseTLS    bool
}

func NewS3(ctx context.Context, config S3Config) (*S3, error) {
	endpoint := config.Endpoint
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		endpoint = parsed.Host
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.UseTLS,
		Region: config.Region,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: client, bucket: config.Bucket, region: config.Region}, nil
}

// ensureBucket keeps an unavailable object store from preventing the
// controller and its operator API from starting. A failed check is deliberately
// not cached: the next object operation retries after the service recovers.
func (s *S3) ensureBucket(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		return nil
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
			return fmt.Errorf("create object bucket: %w", err)
		}
	}
	s.ready = true
	return nil
}

func (s *S3) Put(ctx context.Context, key, contentType string, source io.Reader) (Object, error) {
	if err := validateObjectKey(key); err != nil {
		return Object{}, err
	}
	if err := s.ensureBucket(ctx); err != nil {
		return Object{}, err
	}
	info, err := s.client.PutObject(ctx, s.bucket, key, source, -1, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return Object{}, err
	}
	return Object{Key: key, Size: info.Size, ContentType: contentType, CreatedAt: time.Now().UTC()}, nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	if err := validateObjectKey(key); err != nil {
		return nil, Object{}, err
	}
	if err := s.ensureBucket(ctx); err != nil {
		return nil, Object{}, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, Object{}, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, Object{}, err
	}
	return object, Object{Key: key, Size: info.Size, ContentType: info.ContentType, CreatedAt: info.LastModified}, nil
}

func validateObjectKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "../") || strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("invalid object key")
	}
	return nil
}
