package filestore

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config names one S3-compatible bucket (R2, S3, MinIO, B2 differ only in
// Endpoint, Region and PathStyle). Endpoint is a URL; its scheme picks TLS.
type S3Config struct {
	Endpoint, Region, Bucket string
	AccessKeyID, Secret      string
	PathStyle                bool
}

// StoreError is a store failure with a stable kind (errors.Is against the
// Err* values) and the S3 error code, for machines only. Its text carries
// neither, and never a URL, host or bucket.
type StoreError struct {
	Kind error
	Code string
}

func (e *StoreError) Error() string { return e.Kind.Error() }
func (e *StoreError) Unwrap() error { return e.Kind }

// ErrorCode is the S3 error code behind err, or "" when there is none.
func ErrorCode(err error) string {
	var se *StoreError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

type S3 struct {
	cfg    S3Config
	client *minio.Client
}

func NewS3(cfg S3Config) (*S3, error) {
	c, err := newClient(cfg, credentials.NewStaticV4(cfg.AccessKeyID, cfg.Secret, ""))
	if err != nil {
		return nil, err
	}
	return &S3{cfg: cfg, client: c}, nil
}

func newClient(cfg S3Config, creds *credentials.Credentials) (*minio.Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("endpoint must be an http(s) URL")
	}
	secure := u.Scheme == "https"
	tr, err := minio.DefaultTransport(secure)
	if err != nil {
		return nil, err
	}
	tr.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.ResponseHeaderTimeout = 15 * time.Second
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	return minio.New(u.Host, &minio.Options{Creds: creds, Secure: secure, Region: cfg.Region, BucketLookup: lookup, Transport: tr, MaxRetries: 3})
}

func (s *S3) Kind() string { return "s3" }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	r := minio.ToErrorResponse(err)
	se := &StoreError{Kind: ErrUnavailable, Code: r.Code}
	switch {
	case r.Code == "NoSuchKey" || (r.StatusCode == http.StatusNotFound && r.Code != "NoSuchBucket" && r.Code != "NoSuchUpload"):
		se.Kind = ErrNotFound
	case r.StatusCode == http.StatusPreconditionFailed || r.Code == "PreconditionFailed" || r.Code == "ConditionalRequestConflict":
		se.Kind = ErrExists
	case r.Code == "NoSuchBucket" || r.StatusCode == http.StatusUnauthorized || r.StatusCode == http.StatusForbidden:
		se.Kind = ErrDenied
	}
	return se
}

func (s *S3) Put(ctx context.Context, key string, src io.ReadSeeker, meta ObjectMeta) error {
	opts := minio.PutObjectOptions{
		ContentType:        meta.SafeMIME,
		ContentDisposition: "attachment",
		UserMetadata:       map[string]string{"sha256": meta.SHA256},
		SendContentMd5:     true,
		DisableMultipart:   true,
	}
	opts.SetMatchETagExcept("*")
	_, err := s.client.PutObject(ctx, s.cfg.Bucket, key, src, meta.Size, opts)
	return mapErr(err)
}

func (s *S3) Open(ctx context.Context, key string) (io.ReadSeekCloser, ObjectInfo, error) {
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, mapErr(err)
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, ObjectInfo{}, mapErr(err)
	}
	return obj, ObjectInfo{Key: key, Size: info.Size, ETag: info.ETag}, nil
}

func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.cfg.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, mapErr(err)
	}
	return ObjectInfo{Key: key, Size: info.Size, ETag: info.ETag}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if err := mapErr(s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{})); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (s *S3) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	for o := range s.client.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return mapErr(o.Err)
		}
		if err := fn(ObjectInfo{Key: o.Key, Size: o.Size, ETag: o.ETag}); err != nil {
			return err
		}
	}
	return nil
}

// PublicReadable reports whether key can be read with no credentials at all.
func (s *S3) PublicReadable(ctx context.Context, key string) bool {
	c, err := newClient(s.cfg, credentials.NewStatic("", "", "", credentials.SignatureAnonymous))
	if err != nil {
		return false
	}
	_, err = c.StatObject(ctx, s.cfg.Bucket, key, minio.StatObjectOptions{})
	return err == nil
}

// OpenRange reads length bytes at off, for the connection test.
func (s *S3) OpenRange(ctx context.Context, key string, off, length int64) ([]byte, error) {
	var o minio.GetObjectOptions
	if err := o.SetRange(off, off+length-1); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, o)
	if err != nil {
		return nil, mapErr(err)
	}
	defer obj.Close()
	b, err := io.ReadAll(io.LimitReader(obj, length+1))
	if err != nil {
		return nil, mapErr(err)
	}
	return b, nil
}
