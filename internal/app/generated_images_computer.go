package app

import (
	"context"
	"log"
	"sync"
	"time"
)

// Every image generate_image produces is also placed on the Bot's computer in
// the chat-upload folder (decision 0036), so the Bot can typeset over it or
// compose it into a larger page. The copy is lazy like chat uploads: it never
// boots a computer. A computer tool call of the run places the pending files
// before it runs; when a computer tool was already used in the run, a new
// image is placed at once. A failed copy never fails generate_image.
type generatedImages struct {
	mu      sync.Mutex
	created time.Time
	used    bool
	items   []*generatedImage
}

type generatedImage struct {
	a      Attachment
	path   string
	placed bool
	tries  int
}

func (s *Server) generatedImagesOf(runID string) *generatedImages {
	if v, ok := s.generatedOnComputer.Load(runID); ok {
		return v.(*generatedImages)
	}
	s.generatedOnComputer.Range(func(k, v any) bool {
		if time.Since(v.(*generatedImages).created) > 12*time.Hour {
			s.generatedOnComputer.Delete(k)
		}
		return true
	})
	v, _ := s.generatedOnComputer.LoadOrStore(runID, &generatedImages{created: time.Now()})
	return v.(*generatedImages)
}

// guestOutputCandidates are the conversation's files that share the uploads
// folder: what people sent, then what Bots published earlier. People's files
// come first so their paths never move; a later file with the same name gets
// the id suffix.
func (s *Store) guestOutputCandidates(conv string) ([]guestInputCandidate, error) {
	list, err := s.guestInputCandidates(conv, "")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT a.id,a.conversation_id,a.name,a.mime,a.size,a.created_at FROM attachments a
	 JOIN attachment_messages am ON am.attachment_id=a.id JOIN messages m ON m.id=am.message_id
	 WHERE m.conversation_id=? AND m.role<>'user' GROUP BY a.id ORDER BY a.created_at,a.id`, conv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c guestInputCandidate
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.Name, &c.MIME, &c.Size, &c.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// noteGeneratedImage records a generated image for the computer and returns its
// path there; empty when the Bot has no computer or the path cannot be fixed.
func (s *Server) noteGeneratedImage(ctx context.Context, c Conversation, r Run, a Attachment) string {
	if s.microVM == nil || s.store == nil || r.ID == "" {
		return ""
	}
	g := s.generatedImagesOf(r.ID)
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, it := range g.items {
		if it.a.ID == a.ID {
			return it.path
		}
	}
	list, err := s.store.guestOutputCandidates(c.ID)
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	var all []guestInputCandidate
	for _, x := range list {
		seen[x.ID] = true
		all = append(all, x)
	}
	for _, it := range g.items {
		if !seen[it.a.ID] {
			seen[it.a.ID] = true
			all = append(all, guestInputCandidate{Attachment: it.a})
		}
	}
	if !seen[a.ID] {
		all = append(all, guestInputCandidate{Attachment: a})
	}
	path := guestInputPaths(c.ID, all)[a.ID]
	if path == "" {
		return ""
	}
	g.items = append(g.items, &generatedImage{a: a, path: path})
	if g.used {
		g.flushLocked(ctx, s, r)
	}
	return path
}

// flushGeneratedImages runs before each computer tool call of the run: the
// computer is in use from here on, so pending images are placed now and later
// ones at once.
func (s *Server) flushGeneratedImages(ctx context.Context, r Run) {
	g := s.generatedImagesOf(r.ID)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.used = true
	g.flushLocked(ctx, s, r)
}

func (g *generatedImages) flushLocked(ctx context.Context, s *Server, r Run) {
	for _, it := range g.items {
		if it.placed || it.tries >= 2 || ctx.Err() != nil {
			continue
		}
		it.tries++
		if _, err := s.pushGuestInput(ctx, r, it.a, it.path); err != nil {
			log.Printf("generated image %s to computer: %v", it.a.ID, err)
			continue
		}
		it.placed = true
	}
}
