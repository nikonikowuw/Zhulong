package filestore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

const (
	defaultS3Region      = "us-east-1"
	minPresignTTL        = time.Second
	maxPresignTTL        = 7 * 24 * time.Hour
	defaultS3MaxRetries  = 2
	s3ProbeTimeout       = 10 * time.Second
	s3HealthCheckTimeout = 5 * time.Second
)

// S3Config configures an S3v4-compatible object store driver covering MinIO,
// AWS S3, Aliyun OSS (S3-compatible endpoint), Tencent COS, and Ceph RGW.
type S3Config struct {
	Endpoint           string
	Region             string
	Bucket             string
	Prefix             string
	AccessKeyID        string
	SecretAccessKey    string
	UsePathStyle       bool
	UseTLS             bool
	InsecureSkipVerify bool
	Transport          http.RoundTripper
	Logger             *zap.Logger
}

// S3Driver implements Store and Presigner against any S3v4-compatible endpoint.
type S3Driver struct {
	client *minio.Client
	bucket string
	prefix string
	logger *zap.Logger
}

// NewS3Driver validates S3Config and initializes the S3v4 client without
// performing network I/O in the constructor.
func NewS3Driver(cfg S3Config) (*S3Driver, error) {
	endpoint, inferredTLS, err := normalizeS3Endpoint(cfg.Endpoint, cfg.UseTLS)
	if err != nil {
		return nil, err
	}
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, apperr.Invalid("INVALID_STORAGE_CONFIG", "S3 bucket must not be empty", nil)
	}
	accessKey := strings.TrimSpace(cfg.AccessKeyID)
	secretKey := cfg.SecretAccessKey
	if accessKey == "" || secretKey == "" {
		return nil, apperr.Invalid("INVALID_STORAGE_CONFIG", "S3 credentials must not be empty", nil)
	}

	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultS3Region
	}

	lookup := minio.BucketLookupAuto
	if cfg.UsePathStyle {
		lookup = minio.BucketLookupPath
	}

	transport := cfg.Transport
	if transport == nil && cfg.InsecureSkipVerify {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // Explicitly requested by operator for self-signed MinIO
			},
		}
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:       inferredTLS,
		Region:       region,
		BucketLookup: lookup,
		Transport:    transport,
		MaxRetries:   defaultS3MaxRetries,
	})
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	return &S3Driver{
		client: client,
		bucket: bucket,
		prefix: strings.Trim(strings.TrimSpace(cfg.Prefix), "/"),
		logger: logger.Named("filestore.s3"),
	}, nil
}

// Backend returns BackendS3.
func (d *S3Driver) Backend() string {
	return BackendS3
}

// Put uploads an object to S3/MinIO under the configured bucket and prefix.
func (d *S3Driver) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	opts PutOptions,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return apperr.Invalid("MEDIA_PATH_INVALID", "Media content reader must not be nil", nil)
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}

	ct := opts.ContentType
	if ct == "" {
		ct = ContentTypeForKey(cleanKey)
	}
	objectName := d.objectName(cleanKey)

	putOpts := minio.PutObjectOptions{
		ContentType: ct,
	}
	size := opts.Size
	if size <= 0 {
		size = -1
	}
	_, putErr := d.client.PutObject(
		ctx,
		d.bucket,
		objectName,
		r,
		size,
		putOpts,
	)
	if putErr != nil {
		return mapS3Error(putErr, "write")
	}
	return nil
}

// Open opens a lazy HTTP Range-backed ReadSeekCloser for key after verifying
// object existence and metadata with Stat.
func (d *S3Driver) Open(
	ctx context.Context,
	key string,
) (ReadSeekCloser, ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, err
	}
	cleanKey, err := ValidateKey(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	if _, rest := SplitKey(cleanKey); rest == "" {
		return nil, ObjectInfo{}, apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", ErrNotAFile)
	}

	objectName := d.objectName(cleanKey)
	obj, err := d.client.GetObject(
		ctx,
		d.bucket,
		objectName,
		minio.GetObjectOptions{},
	)
	if err != nil {
		return nil, ObjectInfo{}, mapS3Error(err, "open")
	}

	stat, err := obj.Stat()
	if err != nil {
		closeReaderBestEffort(obj)
		return nil, ObjectInfo{}, mapS3Error(err, "open")
	}

	return obj, objectInfoFromMinIO(cleanKey, stat), nil
}

// Stat retrieves object metadata via HEAD without fetching object bytes.
func (d *S3Driver) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	cleanKey, err := ValidateKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if _, rest := SplitKey(cleanKey); rest == "" {
		return ObjectInfo{}, apperr.Invalid("MEDIA_NOT_A_FILE", "Media path is not a file", ErrNotAFile)
	}

	stat, err := d.client.StatObject(
		ctx,
		d.bucket,
		d.objectName(cleanKey),
		minio.StatObjectOptions{},
	)
	if err != nil {
		return ObjectInfo{}, mapS3Error(err, "stat")
	}
	return objectInfoFromMinIO(cleanKey, stat), nil
}

// Delete removes key from S3/MinIO idempotently.
func (d *S3Driver) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return err
	}

	err = d.client.RemoveObject(
		ctx,
		d.bucket,
		d.objectName(cleanKey),
		minio.RemoveObjectOptions{},
	)
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return mapS3Error(err, "delete")
	}
	return nil
}

// Exists reports whether key exists in S3/MinIO.
func (d *S3Driver) Exists(ctx context.Context, key string) (bool, error) {
	_, err := d.Stat(ctx, key)
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// Health verifies connectivity and bucket accessibility on the S3 endpoint.
func (d *S3Driver) Health(ctx context.Context) (HealthStatus, error) {
	status := HealthStatus{
		Backend: BackendS3,
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}

	checkCtx, cancel := context.WithTimeout(ctx, s3HealthCheckTimeout)
	defer cancel()

	exists, err := d.client.BucketExists(checkCtx, d.bucket)
	if err != nil {
		status.Message = sanitizeS3ErrorMessage(err)
		return status, nil
	}
	if !exists {
		status.Message = "configured S3 bucket does not exist"
		return status, nil
	}
	status.Available = true
	return status, nil
}

// PresignGet generates a time-limited S3v4 presigned GET URL for key.
func (d *S3Driver) PresignGet(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cleanKey, err := ValidateObjectKeyForWrite(key)
	if err != nil {
		return "", err
	}

	if ttl < minPresignTTL {
		ttl = minPresignTTL
	}
	if ttl > maxPresignTTL {
		ttl = maxPresignTTL
	}

	presignedURL, err := d.client.PresignedGetObject(
		ctx,
		d.bucket,
		d.objectName(cleanKey),
		ttl,
		nil,
	)
	if err != nil {
		return "", mapS3Error(err, "presign")
	}
	return presignedURL.String(), nil
}

// ProbeWritable performs a bucket-existence check and a small put/stat/delete
// roundtrip to validate write permissions for operator diagnostics.
func (d *S3Driver) ProbeWritable(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, s3ProbeTimeout)
	defer cancel()

	exists, err := d.client.BucketExists(probeCtx, d.bucket)
	if err != nil {
		return fmt.Errorf("check S3 bucket %q: %w", d.bucket, err)
	}
	if !exists {
		return fmt.Errorf("S3 bucket %q does not exist", d.bucket)
	}

	probeKey := fmt.Sprintf("exports/.zhulong_probe_%d", time.Now().UTC().UnixNano())
	payload := "zhulong_s3_probe"
	putErr := d.Put(
		probeCtx,
		probeKey,
		strings.NewReader(payload),
		PutOptions{
			Size:        int64(len(payload)),
			ContentType: "text/plain",
		},
	)
	if putErr != nil {
		return fmt.Errorf("write S3 probe object: %w", putErr)
	}

	if _, err := d.Stat(probeCtx, probeKey); err != nil {
		if delErr := d.Delete(probeCtx, probeKey); delErr != nil {
			d.logger.Debug("cleanup failed S3 probe object failed", zap.Error(delErr))
		}
		return fmt.Errorf("stat S3 probe object: %w", err)
	}
	if err := d.Delete(probeCtx, probeKey); err != nil {
		return fmt.Errorf("cleanup S3 probe object: %w", err)
	}
	return nil
}

func (d *S3Driver) objectName(cleanKey string) string {
	if d.prefix == "" {
		return cleanKey
	}
	return d.prefix + "/" + cleanKey
}

func normalizeS3Endpoint(raw string, useTLS bool) (string, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false, apperr.Invalid("INVALID_STORAGE_CONFIG", "S3 endpoint must not be empty", nil)
	}

	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Host == "" {
			return "", false, apperr.Invalid("INVALID_STORAGE_CONFIG", "S3 endpoint URL is invalid", err)
		}
		if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", false, apperr.Invalid("INVALID_STORAGE_CONFIG", "S3 endpoint must not include a path", nil)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			return parsed.Host, true, nil
		case "http":
			return parsed.Host, false, nil
		default:
			return "", false, apperr.Invalid(
				"INVALID_STORAGE_CONFIG",
				"S3 endpoint scheme must be http or https",
				nil,
			)
		}
	}

	host := strings.TrimRight(trimmed, "/")
	if strings.Contains(host, "/") {
		return "", false, apperr.Invalid("INVALID_STORAGE_CONFIG", "S3 endpoint must not include a path", nil)
	}
	return host, useTLS, nil
}

func objectInfoFromMinIO(cleanKey string, stat minio.ObjectInfo) ObjectInfo {
	ct := stat.ContentType
	if ct == "" {
		ct = ContentTypeForKey(cleanKey)
	}
	return ObjectInfo{
		Key:         cleanKey,
		Name:        path.Base(cleanKey),
		SizeBytes:   stat.Size,
		ModifiedAt:  stat.LastModified.UTC(),
		ContentType: ct,
		ETag:        stat.ETag,
	}
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	if resp.StatusCode == http.StatusNotFound {
		return true
	}
	switch resp.Code {
	case minio.NoSuchKey, minio.NoSuchBucket, "NotFound":
		return true
	default:
		return false
	}
}

func mapS3Error(err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "S3 storage request timed out or canceled", err)
	}
	if isS3NotFound(err) {
		resp := minio.ToErrorResponse(err)
		if resp.Code == minio.NoSuchBucket {
			return apperr.Internal(
				"MEDIA_ROOT_UNAVAILABLE",
				"Configured S3 bucket is unavailable",
				errors.Join(ErrStoreUnavailable, err),
			)
		}
		return apperr.NotFound(
			"MEDIA_ENTRY_NOT_FOUND",
			"Media entry not found",
			errors.Join(ErrNotFound, err),
		)
	}

	resp := minio.ToErrorResponse(err)
	if resp.StatusCode == 0 || resp.StatusCode >= http.StatusInternalServerError {
		return apperr.Internal(
			"MEDIA_ROOT_UNAVAILABLE",
			"S3 storage endpoint is unavailable",
			errors.Join(ErrStoreUnavailable, err),
		)
	}
	return apperr.Internal(
		"MEDIA_ENTRY_UNREADABLE",
		fmt.Sprintf("S3 object %s failed", op),
		err,
	)
}

func sanitizeS3ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code != "" {
		return fmt.Sprintf("S3 error %s (status %d)", resp.Code, resp.StatusCode)
	}
	return "S3 endpoint unreachable"
}
