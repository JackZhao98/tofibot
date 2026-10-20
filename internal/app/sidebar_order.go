package app

import (
	"database/sql"
	"errors"
	"net/http"
)

// The sidebar keeps two server-side orders per account (one database per
// account): the pinned tiles (conversations.pin_order, 1-based, 0 = not pinned)
// and the person's manual list order (conversations.sort_order, 1-based,
// 0 = never placed by hand). Both are written together by one transaction.
const (
	maxPinnedConversations  = 30
	maxOrderedConversations = 5000
)

var (
	ErrLayoutInvalid  = errors.New("invalid sidebar layout")
	ErrLayoutNotFound = errors.New("conversation not found")
)

// SidebarSlot is one conversation's stored position.
type SidebarSlot struct {
	ID        string `json:"id"`
	Pinned    bool   `json:"pinned"`
	PinOrder  int    `json:"pin_order"`
	SortOrder int    `json:"sort_order"`
}

// SetSidebarLayout replaces the pinned set and the manual order atomically.
// pinned is the full ordered pin list: anything pinned before and absent here is
// unpinned. order is the ordered list of unpinned conversations the client
// shows; those get sort_order 1..n and conversations it does not mention keep
// theirs. A conversation may not appear twice, in both lists, or (for pins) be
// archived. Nothing is written unless every id is valid.
func (s *Store) SetSidebarLayout(pinned, order []string) ([]SidebarSlot, error) {
	if len(pinned) > maxPinnedConversations || len(order) > maxOrderedConversations {
		return nil, ErrLayoutInvalid
	}
	seen := make(map[string]bool, len(pinned)+len(order))
	for _, id := range append(append([]string{}, pinned...), order...) {
		if id == "" || seen[id] {
			return nil, ErrLayoutInvalid
		}
		seen[id] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	check := func(id string, pin bool) error {
		var archived int
		if err := tx.QueryRow(`SELECT archived FROM conversations WHERE id=? AND user_visible=1`, id).Scan(&archived); err == sql.ErrNoRows {
			return ErrLayoutNotFound
		} else if err != nil {
			return err
		}
		if pin && archived != 0 {
			return ErrLayoutInvalid
		}
		return nil
	}
	for _, id := range pinned {
		if err = check(id, true); err != nil {
			return nil, err
		}
	}
	for _, id := range order {
		if err = check(id, false); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(`UPDATE conversations SET pin_order=0 WHERE pin_order<>0`); err != nil {
		return nil, err
	}
	for i, id := range pinned {
		if _, err = tx.Exec(`UPDATE conversations SET pin_order=? WHERE id=?`, i+1, id); err != nil {
			return nil, err
		}
	}
	for i, id := range order {
		if _, err = tx.Exec(`UPDATE conversations SET sort_order=? WHERE id=?`, i+1, id); err != nil {
			return nil, err
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeGroups, now()); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT id,pin_order,sort_order FROM conversations WHERE user_visible=1 AND (pin_order<>0 OR sort_order<>0) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := []SidebarSlot{}
	for rows.Next() {
		var x SidebarSlot
		if err = rows.Scan(&x.ID, &x.PinOrder, &x.SortOrder); err != nil {
			rows.Close()
			return nil, err
		}
		x.Pinned = x.PinOrder > 0
		out = append(out, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (s *Server) routeSidebarOrder(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "conversations/order" {
		return false
	}
	if r.Method != http.MethodPut {
		writeErr(w, 405, "method", "method not allowed")
		return true
	}
	var x struct {
		Pinned []string `json:"pinned"`
		Order  []string `json:"order"`
	}
	if decode(r, &x) != nil {
		writeErr(w, 400, "invalid_request", "pinned and order lists required")
		return true
	}
	slots, err := s.store.SetSidebarLayout(x.Pinned, x.Order)
	switch {
	case errors.Is(err, ErrLayoutInvalid):
		writeErr(w, 400, "invalid_layout", "lists must hold distinct, unarchived ids and stay within the size limits")
	case errors.Is(err, ErrLayoutNotFound):
		writeErr(w, 404, "not_found", "conversation not found")
	case err != nil:
		writeErr(w, 500, "storage", err.Error())
	default:
		writeJSON(w, 200, map[string]any{"layout": slots})
	}
	return true
}
