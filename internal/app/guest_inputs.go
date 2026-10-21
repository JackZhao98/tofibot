package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"path"
	"strings"
	"sync"
	"unicode"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// Files a person uploads in chat are placed on the Bot's computer, in the
// shared guest home, one folder per conversation:
//
//	/workspace/home/uploads/<conversation id>/<original file name>
//
// A name that two attachments of the conversation share keeps its plain form
// for the earliest one; every later one gets "-<first 8 of its id>" before the
// extension. Only attachments of messages in that conversation are ever placed
// there, so a Bot's visibility matches read_attachment. Archives are not
// extracted. The transfer reuses the guest file channel (files.import_chunk,
// the reverse of publish_file's files.export_chunk) and runs lazily, at a
// run's first computer tool call.
// guestUploadsRoot is a variable only so a test can point it at a test guest.
var guestUploadsRoot = "/workspace/home/uploads"

const (
	guestImportChunk   = 512 << 10
	guestImportMaxFile = computer.MaxBlobBytes
	guestImportMaxN    = 40
	guestImportMaxByte = 80 << 20
)

type guestInputCandidate struct {
	Attachment
	Triggering bool
}

// guestInputCandidates lists the attachments a person sent in the conversation,
// oldest first. Triggering marks those of the run's trigger message.
func (s *Store) guestInputCandidates(conv, trigger string) ([]guestInputCandidate, error) {
	rows, err := s.db.Query(`SELECT a.id,a.conversation_id,a.name,a.mime,a.size,a.created_at,MAX(m.id=?) FROM attachments a
	 JOIN attachment_messages am ON am.attachment_id=a.id JOIN messages m ON m.id=am.message_id
	 WHERE m.conversation_id=? AND m.role='user' GROUP BY a.id ORDER BY a.created_at,a.id`, trigger, conv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []guestInputCandidate
	for rows.Next() {
		var c guestInputCandidate
		var trig int
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.Name, &c.MIME, &c.Size, &c.CreatedAt, &trig); err != nil {
			return nil, err
		}
		c.Triggering = trig == 1 && trigger != ""
		out = append(out, c)
	}
	return out, rows.Err()
}

// guestInputName makes a file name safe as one path element.
func guestInputName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	if len(name) > 200 {
		ext := path.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		stem := name[:200-len(ext)]
		for len(stem) > 0 && !isRuneStart(stem[len(stem)-1]) {
			stem = stem[:len(stem)-1]
		}
		name = stem + ext
	}
	if strings.HasSuffix(strings.ToLower(name), ".part") {
		name += "_"
	}
	return name
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// guestInputPaths maps attachment id to its path on the computer. It is a pure
// function of the ordered list, so the same attachment keeps its path.
func guestInputPaths(conv string, list []guestInputCandidate) map[string]string {
	folder := guestUploadsRoot + "/" + guestInputName(conv)
	used := map[string]bool{}
	out := make(map[string]string, len(list))
	for _, a := range list {
		name := guestInputName(a.Name)
		if used[strings.ToLower(name)] {
			ext := path.Ext(name)
			if len(ext) > 20 || ext == name {
				ext = ""
			}
			stem := strings.TrimSuffix(name, ext)
			short := a.ID
			if len(short) > 8 {
				short = short[:8]
			}
			name = stem + "-" + short + ext
			if used[strings.ToLower(name)] {
				name = stem + "-" + a.ID + ext
			}
		}
		used[strings.ToLower(name)] = true
		out[a.ID] = folder + "/" + name
	}
	return out
}

// guestInputPathsFor is nil when the Bot has no computer or nothing was sent.
func (s *Server) guestInputPathsFor(c Conversation, trigger string) map[string]string {
	if s.microVM == nil || s.store == nil {
		return nil
	}
	list, err := s.store.guestInputCandidates(c.ID, trigger)
	if err != nil || len(list) == 0 {
		return nil
	}
	return guestInputPaths(c.ID, list)
}

// syncGuestInputs copies the conversation's uploads onto the computer. The
// triggering message's files go first, then earlier ones newest first, within a
// count and byte budget. Files already there are skipped by the guest.
func (s *Server) syncGuestInputs(ctx context.Context, r Run, c Conversation) error {
	list, err := s.store.guestInputCandidates(c.ID, r.TriggerMessageID)
	if err != nil || len(list) == 0 {
		return err
	}
	paths := guestInputPaths(c.ID, list)
	order := make([]guestInputCandidate, 0, len(list))
	for _, a := range list {
		if a.Triggering {
			order = append(order, a)
		}
	}
	for i := len(list) - 1; i >= 0; i-- {
		if !list[i].Triggering {
			order = append(order, list[i])
		}
	}
	var sent int64
	var firstErr error
	for i, a := range order {
		if i >= guestImportMaxN || (sent > guestImportMaxByte && !a.Triggering) {
			break
		}
		if a.Size > guestImportMaxFile {
			continue
		}
		n, err := s.pushGuestInput(ctx, r, a.Attachment, paths[a.ID])
		sent += n
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("guest input %s: %v", a.ID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// pushGuestInput sends one attachment in ordered chunks and returns the bytes
// sent; zero when the guest already has the file.
func (s *Server) pushGuestInput(ctx context.Context, r Run, a Attachment, dst string) (int64, error) {
	_, src, err := s.store.Attachment(a.ID)
	if err != nil {
		return 0, err
	}
	f, err := s.store.openAttachment(ctx, src)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, guestImportChunk)
	var offset int64
	for {
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return offset, readErr
		}
		if offset+int64(n) > a.Size {
			return offset, errors.New("attachment is larger than recorded")
		}
		last := readErr != nil
		if last && offset+int64(n) != a.Size {
			return offset, errors.New("attachment is shorter than recorded")
		}
		args, _ := json.Marshal(map[string]any{"path": dst, "offset": offset, "total_size": a.Size, "data_base64": base64.StdEncoding.EncodeToString(buf[:n])})
		out, err := s.microVMAction(ctx, r, "files.import_chunk", args)
		if err != nil {
			return offset, err
		}
		var res struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		if res.Status == "exists" {
			return 0, nil
		}
		offset += int64(n)
		if last || offset >= a.Size {
			return offset, nil
		}
	}
}

// guestInputSync makes the copy happen once per tool set, before the first
// computer tool call. It never fails the call: a Bot whose files did not arrive
// finds that out from the file listing, and the next call tries again.
type guestInputSync struct {
	mu       sync.Mutex
	attempts int
	done     bool
}

func (g *guestInputSync) ensure(ctx context.Context, s *Server, r Run) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done || r.ConversationID == "" || s.store == nil {
		return
	}
	g.attempts++
	c, err := s.store.GetConversation(r.ConversationID)
	if err == nil {
		err = s.syncGuestInputs(ctx, r, c)
	}
	if err == nil || g.attempts >= 2 {
		g.done = true
	}
}
