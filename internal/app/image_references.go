package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

// generate_image takes its references from anywhere the Bot can reach an
// image: an attachment of this conversation (an upload, a published file, an
// image made earlier) or a file on its computer. Each one is normalized to a
// size the image service accepts, so a large photo or a GIF is scaled or
// converted rather than refused.
const (
	imageReferencesMax       = 8
	imageReferenceSourceMax  = 32 << 20 // largest file read as a reference
	imageReferenceLongSide   = 2048     // longer sides are scaled down to this
	imageReferenceEncodedMax = 4 << 20  // encoded size sent per reference
)

// readComputerFile reads a regular file from the Bot's computer through the
// same bounded chunk export publish_file and files.view use.
func (s *Server) readComputerFile(ctx context.Context, r Run, path string, limit int64) ([]byte, error) {
	var data []byte
	var version string
	for offset := int64(0); ; {
		args, _ := json.Marshal(map[string]any{"path": path, "offset": offset, "limit": viewChunkBytes, "version": version})
		out, err := s.microVMAction(ctx, r, "files.export_chunk", args)
		if err != nil {
			return nil, err
		}
		var w struct {
			Data    string `json:"data_base64"`
			Next    int64  `json:"next_offset"`
			Size    int64  `json:"size"`
			EOF     bool   `json:"eof"`
			Version string `json:"version"`
		}
		if json.Unmarshal([]byte(out), &w) != nil || w.Next < offset {
			return nil, errors.New("invalid file chunk from the computer")
		}
		if w.Size > limit {
			return nil, &fileTooLargeError{path: path, size: w.Size, limit: limit}
		}
		chunk, err := base64.StdEncoding.DecodeString(w.Data)
		if err != nil || int64(len(chunk)) != w.Next-offset {
			return nil, errors.New("invalid file chunk from the computer")
		}
		data, version, offset = append(data, chunk...), w.Version, w.Next
		if w.EOF {
			return data, nil
		}
	}
}

type fileTooLargeError struct {
	path        string
	size, limit int64
}

func (e *fileTooLargeError) Error() string {
	return fmt.Sprintf("%s is %d bytes; the limit is %d bytes", e.path, e.size, e.limit)
}

// imageReference resolves one reference, an attachment ID or an absolute path
// on the computer, to a data URL.
func (s *Server) imageReference(ctx context.Context, c Conversation, r Run, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	var data []byte
	if strings.HasPrefix(ref, "/") {
		if s.computer() == nil {
			return "", fmt.Errorf("%s is a computer path, but this Bot has no computer", ref)
		}
		var err error
		if data, err = s.readComputerFile(ctx, r, ref, imageReferenceSourceMax); err != nil {
			return "", err
		}
	} else {
		_, path, err := s.store.Attachment(ref)
		if err != nil {
			return "", fmt.Errorf("reference %s is neither an attachment nor an absolute computer path", ref)
		}
		if ok, e := s.store.attachmentAvailableInConversation(ref, c.ID); e != nil || !ok {
			return "", errors.New("attachment belongs to another conversation")
		}
		readCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		f, err := s.store.openAttachment(readCtx, path)
		if err != nil {
			return "", err
		}
		data, err = io.ReadAll(io.LimitReader(f, imageReferenceSourceMax+1))
		_ = f.Close()
		if err != nil {
			return "", err
		}
		if len(data) > imageReferenceSourceMax {
			return "", fmt.Errorf("reference %s exceeds %d bytes", ref, imageReferenceSourceMax)
		}
	}
	return imageReferenceDataURL(ref, data)
}

// imageReferenceDataURL accepts PNG, JPEG, GIF and WebP. A PNG or JPEG within
// bounds passes as it is; larger ones are scaled and re-encoded, and a GIF or
// WebP is sent as PNG (a GIF's first frame).
func imageReferenceDataURL(ref string, data []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif" && format != "webp") {
		return "", fmt.Errorf("%s is not a PNG, JPEG, GIF or WebP image", ref)
	}
	if config.Width < 1 || config.Height < 1 || config.Width > viewSourceDimsMax || config.Height > viewSourceDimsMax || config.Width*config.Height > viewSourcePixelsMax {
		return "", fmt.Errorf("%s is %dx%d pixels, too large to decode", ref, config.Width, config.Height)
	}
	longSide := max(config.Width, config.Height)
	if format != "gif" && format != "webp" && longSide <= imageReferenceLongSide && len(data) <= imageReferenceEncodedMax {
		return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%s cannot be decoded as an image", ref)
	}
	for target := min(longSide, imageReferenceLongSide); ; target = target * 3 / 4 {
		if target < longSide && target < 240 {
			break
		}
		resized := img
		if target < longSide {
			resized = resizeToLongSide(img, target)
		}
		var b bytes.Buffer
		if png.Encode(&b, resized) == nil && b.Len() <= imageReferenceEncodedMax {
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes()), nil
		}
		b.Reset()
		if jpeg.Encode(&b, flattenOnWhite(resized), &jpeg.Options{Quality: viewJPEGQualityFirst}) == nil && b.Len() <= imageReferenceEncodedMax {
			return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b.Bytes()), nil
		}
	}
	return "", fmt.Errorf("%s cannot be reduced to a size the image service accepts", ref)
}
