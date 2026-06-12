// Package objectstore is a thin wrapper over the AWS S3 SDK configured for an
// S3-compatible endpoint (SeaweedFS in the local stack, any S3 in cloud). It
// exists so callers archive raw payloads without re-deriving path-style /
// custom-endpoint config each time. The raw archive is the replay safety net
// (ADR-010): every payload is durably stored so any read model can be rebuilt.
package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Config holds the connection settings for an S3-compatible store.
type Config struct {
	Endpoint  string // e.g. http://localhost:8333 (SeaweedFS S3 API)
	Region    string // any value works for SeaweedFS; "us-east-1" by convention
	AccessKey string
	SecretKey string
	Bucket    string
}

// Store is an S3-compatible object store scoped to one bucket.
type Store struct {
	client *s3.Client
	bucket string
}

// New builds a Store. It uses path-style addressing (host/bucket/key) because
// SeaweedFS and most self-hosted S3 stores don't support virtual-host buckets.
func New(ctx context.Context, cfg Config) (*Store, error) {
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	})
	s := &Store{client: client, bucket: cfg.Bucket}
	if err := s.ensureBucket(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// ensureBucket creates the bucket if it doesn't already exist (idempotent).
func (s *Store) ensureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
	if err == nil {
		return nil
	}
	_, err = s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucket})
	if err != nil && !bucketAlreadyOwned(err) {
		return fmt.Errorf("objectstore: ensure bucket %q: %w", s.bucket, err)
	}
	return nil
}

// PutIfAbsent writes value at key only if no object exists there, so a replayed
// or redelivered message doesn't overwrite an existing archived payload. Returns
// (false, nil) when the object already existed. Idempotent by construction.
func (s *Store) PutIfAbsent(ctx context.Context, key string, value []byte, contentType string) (written bool, err error) {
	// IfNoneMatch:"*" → S3 fails with PreconditionFailed if the key exists.
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        bytes.NewReader(value),
		ContentType: &contentType,
		IfNoneMatch: aws.String("*"),
	})
	if err == nil {
		return true, nil
	}
	if preconditionFailed(err) {
		return false, nil
	}
	return false, fmt.Errorf("objectstore: put %q: %w", key, err)
}

func bucketAlreadyOwned(err error) bool {
	var owned *types.BucketAlreadyOwnedByYou
	var exists *types.BucketAlreadyExists
	return errors.As(err, &owned) || errors.As(err, &exists)
}

func preconditionFailed(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed", "412":
			return true
		}
	}
	return false
}
