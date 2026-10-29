package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// guestFileImport asks for one regular file of the computer to be copied into
// the server file store through the bounded files.export_chunk channel.
type guestFileImport struct {
	Path string
	// Name overrides the file's own name when not empty.
	Name string
	// NoSymlinks makes the guest refuse a link in the path (outbox sync).
	NoSymlinks bool
	Opts       attachmentOpts
}

// importGuestFile streams the file in chunks into addAttachmentOpts. A change
// of the source while it is read fails the import; nothing partial is kept.
// The second result is the guest's version tag of the bytes that were copied.
func (s *Server) importGuestFile(ctx context.Context, r Run, c Conversation, in guestFileImport) (Attachment, string, error) {
	limit := maxAttachmentSize
	if in.Opts.MaxBytes > 0 && in.Opts.MaxBytes < limit {
		limit = in.Opts.MaxBytes
	}
	type wireChunk struct {
		Data    string `json:"data_base64"`
		Next    int64  `json:"next_offset"`
		Size    int64  `json:"size"`
		EOF     bool   `json:"eof"`
		Version string `json:"version"`
		Name    string `json:"name"`
	}
	fetch := func(offset int64, version string) (wireChunk, []byte, error) {
		req := map[string]any{"path": in.Path, "offset": offset, "limit": 512 << 10, "version": version}
		if in.NoSymlinks {
			req["no_symlinks"] = true
		}
		args, _ := json.Marshal(req)
		out, err := s.microVMAction(ctx, r, "files.export_chunk", args)
		if err != nil {
			return wireChunk{}, nil, err
		}
		var w wireChunk
		if json.Unmarshal([]byte(out), &w) != nil || w.Next < offset || w.Size > limit {
			return w, nil, errors.New("invalid or oversized VM file")
		}
		b, err := base64.StdEncoding.DecodeString(w.Data)
		if err != nil || int64(len(b)) != w.Next-offset {
			return w, nil, errors.New("invalid VM file chunk")
		}
		return w, b, nil
	}
	first, firstBytes, err := fetch(0, "")
	if err != nil {
		return Attachment{}, "", err
	}
	version, sourceName, size := first.Version, first.Name, first.Size
	var offset int64
	reader, writer := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		defer writer.Close()
		fail := func(err error) { _ = writer.CloseWithError(err); errCh <- err }
		if _, err := writer.Write(firstBytes); err != nil {
			errCh <- err
			return
		}
		offset = first.Next
		if first.EOF {
			errCh <- nil
			return
		}
		for {
			chunk, b, e := fetch(offset, version)
			if e != nil {
				fail(e)
				return
			}
			if version != chunk.Version || size != chunk.Size {
				fail(errors.New("file changed while publishing"))
				return
			}
			if _, e = writer.Write(b); e != nil {
				errCh <- e
				return
			}
			offset = chunk.Next
			if chunk.EOF {
				errCh <- nil
				return
			}
		}
	}()
	name := filepath.Base(strings.TrimSpace(in.Name))
	if name == "." || name == "" {
		name = sourceName
	}
	a, err := s.store.addAttachmentOpts(ctx, c.ID, name, "application/octet-stream", reader, in.Opts)
	if err != nil {
		_ = reader.CloseWithError(err)
	}
	copyErr := <-errCh
	if err == nil {
		err = copyErr
	}
	if err != nil {
		if a.ID != "" {
			_ = s.store.deleteUnboundAttachment(a.ID)
		}
		return Attachment{}, "", err
	}
	return a, version, nil
}
