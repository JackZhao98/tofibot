package extensions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxMetadataIndexFileBytes   = 1 << 20
	maxMetadataIndexTools       = 1000
	maxMetadataIndexServerName  = 256
	maxMetadataIndexFingerprint = 256
	maxMetadataIndexToolName    = 1024
	maxMetadataIndexDescription = 8 << 10
)

// MCPMetadataTool is the small, inert part of an MCP tool catalog useful for
// search. Schemas and connection details are intentionally not represented.
type MCPMetadataTool struct {
	RemoteName  string `json:"remote_name"`
	Description string `json:"description"`
}

type metadataIndexFile struct {
	Version     int               `json:"version"`
	Server      string            `json:"server"`
	Fingerprint string            `json:"fingerprint"`
	Tools       []MCPMetadataTool `json:"tools"`
}

type metadataIndexServer struct {
	Fingerprint string            `json:"fingerprint"`
	Tools       []MCPMetadataTool `json:"tools"`
}

// PersistentMCPMetadataIndex stores bounded MCP search metadata in a private
// JSON file. The caller supplies an absolute path; no remote data is fetched.
type PersistentMCPMetadataIndex struct {
	mu      sync.RWMutex
	path    string
	servers map[string]metadataIndexServer
}

// NewPersistentMCPMetadataIndex loads an existing index. Missing, malformed,
// or unsupported files are treated as an empty index so callers can rebuild it.
func NewPersistentMCPMetadataIndex(path string) (*PersistentMCPMetadataIndex, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("metadata index path must be absolute")
	}
	i := &PersistentMCPMetadataIndex{path: filepath.Clean(path), servers: map[string]metadataIndexServer{}}
	return i, nil
}

// Get returns a deep copy only when the saved configuration fingerprint
// exactly matches the current one.
func (i *PersistentMCPMetadataIndex) Get(server, fingerprint string) ([]MCPMetadataTool, bool) {
	if i == nil || !validMetadataIdentity(server, fingerprint) {
		return nil, false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	entry, ok := i.servers[server]
	if !ok {
		file, valid := i.readServer(server)
		if !valid {
			return nil, false
		}
		entry = metadataIndexServer{Fingerprint: file.Fingerprint, Tools: file.Tools}
	}
	if entry.Fingerprint != fingerprint {
		return nil, false
	}
	return append([]MCPMetadataTool(nil), entry.Tools...), true
}

// Put atomically persists a complete server metadata list using mode 0600.
func (i *PersistentMCPMetadataIndex) Put(server, fingerprint string, tools []MCPMetadataTool) error {
	if i == nil {
		return errors.New("metadata index is nil")
	}
	if !validMetadataIdentity(server, fingerprint) {
		return errors.New("invalid metadata index identity")
	}
	if !validMetadataTools(tools) || len(tools) > maxMetadataIndexTools {
		return errors.New("invalid or oversized tool metadata")
	}
	copyTools := append([]MCPMetadataTool(nil), tools...)
	i.mu.Lock()
	defer i.mu.Unlock()
	filePath := i.serverPath(server)
	raw, err := json.Marshal(metadataIndexFile{Version: 1, Server: server, Fingerprint: fingerprint, Tools: copyTools})
	if err != nil {
		return err
	}
	if len(raw) > maxMetadataIndexFileBytes {
		return errors.New("metadata index file limit exceeded")
	}
	if err := os.MkdirAll(i.path, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(i.path, ".mcp-metadata-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(raw)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(tmpName, filePath); err != nil {
		return err
	}
	if dir, err := os.Open(i.path); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	i.servers[server] = metadataIndexServer{Fingerprint: fingerprint, Tools: copyTools}
	return nil
}

// InvalidateServer removes the in-memory entry and the persisted file for one
// server. Missing files are already invalidated.
func (i *PersistentMCPMetadataIndex) InvalidateServer(server string) error {
	if i == nil || server == "" || len(server) > maxMetadataIndexServerName {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.servers, server)
	err := os.Remove(i.serverPath(server))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func validMetadataIdentity(server, fingerprint string) bool {
	return server != "" && len(server) <= maxMetadataIndexServerName && fingerprint != "" && len(fingerprint) <= maxMetadataIndexFingerprint
}

func validMetadataTools(tools []MCPMetadataTool) bool {
	for _, tool := range tools {
		if strings.TrimSpace(tool.RemoteName) == "" || len(tool.RemoteName) > maxMetadataIndexToolName || len(tool.Description) > maxMetadataIndexDescription {
			return false
		}
	}
	return true
}

func (i *PersistentMCPMetadataIndex) serverPath(server string) string {
	sum := sha256.Sum256([]byte(server))
	return filepath.Join(i.path, hex.EncodeToString(sum[:])+".json")
}

func (i *PersistentMCPMetadataIndex) readServer(server string) (metadataIndexFile, bool) {
	raw, err := os.ReadFile(i.serverPath(server))
	if err != nil || len(raw) > maxMetadataIndexFileBytes {
		return metadataIndexFile{}, false
	}
	var file metadataIndexFile
	if json.Unmarshal(raw, &file) != nil || file.Version != 1 || file.Server != server || !validMetadataIdentity(file.Server, file.Fingerprint) || len(file.Tools) > maxMetadataIndexTools || !validMetadataTools(file.Tools) {
		return metadataIndexFile{}, false
	}
	return file, true
}
