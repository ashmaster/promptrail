package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Storage struct {
	client     *s3.Client
	presigner  *s3.PresignClient
	bucketName string
}

func NewR2Storage(accountID, accessKeyID, secretAccessKey, bucketName string) (*R2Storage, error) {
	if accountID == "" || accessKeyID == "" || secretAccessKey == "" {
		return nil, fmt.Errorf("R2 credentials not configured")
	}

	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)

	cfg := aws.Config{
		Region: "auto",
		Credentials: credentials.NewStaticCredentialsProvider(
			accessKeyID,
			secretAccessKey,
			"",
		),
		BaseEndpoint: aws.String(endpoint),
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})
	presigner := s3.NewPresignClient(client)

	return &R2Storage{
		client:     client,
		presigner:  presigner,
		bucketName: bucketName,
	}, nil
}

// GenerateUploadURL creates a presigned PUT URL for uploading a blob.
func (r *R2Storage) GenerateUploadURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	req, err := r.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucketName),
		Key:         aws.String(key),
		ContentType: aws.String("application/gzip"),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

// GenerateReadURL creates a presigned GET URL for reading a blob.
func (r *R2Storage) GenerateReadURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	req, err := r.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return req.URL, nil
}

// DeleteObject removes a blob from R2.
func (r *R2Storage) DeleteObject(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// BlobKey returns the R2 key for a session blob.
func BlobKey(userID, sessionID string) string {
	return fmt.Sprintf("%s/%s.json.gz", userID, sessionID)
}
