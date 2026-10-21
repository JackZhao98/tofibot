package guest

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// importMaxBytes matches the host's upload and blob ceilings.
const importMaxBytes int64 = 20 << 20

// importFolder is where the host places files a person uploaded in chat: a
// folder in the shared guest home, outside every Bot profile.
const importFolder = "home/uploads"

type importArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Total  int64  `json:"total_size"`
	Data   string `json:"data_base64"`
}

// importFileChunk is the host-to-guest direction of files.export_chunk. A file
// arrives in ordered chunks into "<path>.part" and is renamed into place with
// the last one, so a reader never sees a partial file. A file that already
// exists is never replaced: the first chunk answers "exists", which makes the
// whole transfer idempotent and keeps edits the Bot made to its own copy.
func (s *Service) importFileChunk(ctx context.Context, botID string, args importArgs) (map[string]any, error) {
	if !filepath.IsAbs(args.Path) {
		return nil, errors.New("import path must be absolute")
	}
	if args.Total < 0 || args.Total > importMaxBytes || args.Offset < 0 || args.Offset > args.Total {
		return nil, errors.New("invalid import size or offset")
	}
	data, err := base64.StdEncoding.DecodeString(args.Data)
	if err != nil || len(data) > exportChunkBytes || args.Offset+int64(len(data)) > args.Total {
		return nil, errors.New("invalid import chunk")
	}
	p, err := s.resolvePath(botID, args.Path, true)
	if err != nil {
		return nil, err
	}
	folder := filepath.Join(s.root, importFolder)
	if rel, e := filepath.Rel(folder, p); e != nil || rel == "." || !within(folder, p) {
		return nil, errors.New("import path must be inside the uploads folder")
	}
	lock := s.fileWriteLock(p)
	if err := lockFileWrite(ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	part := p + ".part"
	if args.Offset == 0 {
		if info, e := os.Lstat(p); e == nil {
			if !info.Mode().IsRegular() {
				return nil, errors.New("import target is not a regular file")
			}
			return map[string]any{"status": "exists", "size": info.Size()}, nil
		} else if !os.IsNotExist(e) {
			return nil, e
		}
		if err := os.MkdirAll(filepath.Dir(p), 0770); err != nil {
			return nil, err
		}
	}
	flags := os.O_WRONLY | syscall.O_NOFOLLOW
	if args.Offset == 0 {
		flags |= os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0660)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if args.Offset > 0 {
		if info, e := f.Stat(); e != nil || info.Size() != args.Offset {
			return nil, errors.New("import chunk out of order")
		}
	}
	if _, err := f.WriteAt(data, args.Offset); err != nil {
		return nil, err
	}
	next := args.Offset + int64(len(data))
	if next < args.Total {
		return map[string]any{"status": "partial", "next_offset": next}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(part, p); err != nil {
		return nil, err
	}
	return map[string]any{"status": "written", "size": args.Total}, nil
}
