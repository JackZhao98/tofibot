package filestore

import (
	"bytes"
	"context"
	"errors"
)

// ProbeError is one named failure of the connection test.
type ProbeError struct{ Code string }

func (e *ProbeError) Error() string { return e.Code }

// Probe runs the connection test on key (a throwaway object): conditional put,
// the same put again (must be refused), stat, ranged read, delete, confirm
// deleted. Public reports that the object could be read with no credentials.
func Probe(ctx context.Context, s *S3, key string) (public bool, err error) {
	payload := []byte("tofi-probe-0123456789")
	meta := ObjectMeta{Size: int64(len(payload)), SHA256: "probe", SafeMIME: "application/octet-stream"}
	if err = s.Put(ctx, key, bytes.NewReader(payload), meta); err != nil {
		return false, &ProbeError{Code: putCode(err)}
	}
	defer func() { _ = s.Delete(context.WithoutCancel(ctx), key) }()
	if e := s.Put(ctx, key, bytes.NewReader(payload), meta); !errors.Is(e, ErrExists) {
		return false, &ProbeError{Code: "conditional_put_unsupported"}
	}
	if info, e := s.Stat(ctx, key); e != nil || info.Size != meta.Size {
		return false, &ProbeError{Code: "endpoint_unreachable"}
	}
	if got, e := s.OpenRange(ctx, key, 2, 4); e != nil || string(got) != string(payload[2:6]) {
		return false, &ProbeError{Code: "range_unsupported"}
	}
	public = s.PublicReadable(ctx, key)
	if e := s.Delete(ctx, key); e != nil {
		return public, &ProbeError{Code: "delete_denied"}
	}
	if _, e := s.Stat(ctx, key); !errors.Is(e, ErrNotFound) {
		return public, &ProbeError{Code: "delete_denied"}
	}
	return public, nil
}

func putCode(err error) string {
	switch {
	case errors.Is(err, ErrDenied):
		switch ErrorCode(err) {
		case "NoSuchBucket":
			return "bucket_not_found"
		case "AccessDenied":
			return "write_denied"
		}
		return "credentials_rejected"
	case errors.Is(err, ErrExists):
		return "conditional_put_unsupported"
	}
	return "endpoint_unreachable"
}
