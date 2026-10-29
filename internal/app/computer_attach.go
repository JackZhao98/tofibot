package app

// Attach and detach (computer-decoupling S6): the administrator's endpoints and
// the outbox copy that runs before a detach.
//
// Who may do what (owner decision, 2026-10-28):
//   - a person cannot add a VM; the administrator assigns it. In an account
//     gateway the endpoint is /api/admin/accounts/{id}/computer/{attach|detach};
//     a workspace never serves /api/admin/*. A single-account server serves
//     /api/admin/computer/{attach|detach} to its owner, who is its administrator.
//   - the administrator sees VMs and the server only. Nothing here reads a
//     personal computer: the answer is built by adminVMSummary from the VM slot
//     and the VM row alone, and a personal row is never selected.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/google/uuid"
)

// detachSyncTimeout bounds the outbox copy: a computer that does not answer must not hold the detach.
const detachSyncTimeout = 30 * time.Second

// adminVMSummary is the administrator's whole view of an account's computer.
type adminVMSummary struct {
	Attached bool   `json:"attached"`
	ID       string `json:"id,omitempty"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	// BackendAvailable says whether this server can attach a VM at all.
	BackendAvailable bool `json:"backend_available"`
}

func (s *Server) adminVMSummary() adminVMSummary {
	out := adminVMSummary{Kind: computer.RowKindVM, Status: computerRowDetached, BackendAvailable: s.computerFactory != nil}
	if vm := s.computer(); vm != nil {
		out.Attached, out.ID = true, vm.ID()
		out.Status = computerRowAttached
		if slot := s.slotFor(vm.ID()); slot != nil {
			switch slot.currentState() {
			case slotLost:
				out.Status = computerRowLost
			case slotDraining:
				out.Status = "detaching"
			}
		}
		return out
	}
	if s.store != nil {
		var status, id string
		if s.store.db.QueryRow(`SELECT id,status FROM computers WHERE kind='vm' AND revoked_at IS NULL ORDER BY created_at LIMIT 1`).Scan(&id, &status) == nil {
			out.ID, out.Status = id, status
		}
	}
	return out
}

// serveComputerAttach answers the attach/detach/state calls for this server's VM.
func (s *Server) serveComputerAttach(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "state":
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "use GET")
			return
		}
		writeJSON(w, 200, s.adminVMSummary())
	case "attach":
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "use POST")
			return
		}
		switch err := s.AttachVM(r.Context()); {
		case err == nil:
			writeJSON(w, 200, s.adminVMSummary())
		case errors.Is(err, errComputerAttached):
			writeJSON(w, 200, s.adminVMSummary())
		case IsComputerBackendUnavailable(err):
			writeErr(w, 409, "computer_backend_unavailable", "this server has no computer backend configured")
		default:
			writeErr(w, 502, "computer_attach_failed", "the computer could not be attached")
		}
	case "detach":
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "use POST")
			return
		}
		var in struct {
			WaitSeconds int `json:"wait_seconds"`
		}
		if r.ContentLength != 0 && decodeStrict(r, 1024, &in) != nil {
			writeErr(w, 400, "invalid_request", "invalid detach request")
			return
		}
		if in.WaitSeconds < 0 || in.WaitSeconds > 600 {
			writeErr(w, 400, "invalid_request", "wait_seconds must be 0 to 600")
			return
		}
		report, err := s.DetachVM(r.Context(), time.Duration(in.WaitSeconds)*time.Second)
		if errors.Is(err, errComputerNone) {
			writeJSON(w, 200, map[string]any{"computer": s.adminVMSummary()})
			return
		}
		if err != nil {
			writeErr(w, 500, "computer_detach_failed", "the computer could not be detached")
			return
		}
		writeJSON(w, 200, map[string]any{"computer": s.adminVMSummary(), "detach": report})
	default:
		writeErr(w, 404, "not_found", "unknown computer operation")
	}
}

// routeComputerAttach serves /api/admin/computer/{state|attach|detach} on a
// single-account server. In an account gateway a workspace never sees /api/admin/.
func (s *Server) routeComputerAttach(w http.ResponseWriter, r *http.Request, path string) bool {
	op, ok := strings.CutPrefix(path, "admin/computer/")
	if !ok || strings.Contains(op, "/") {
		return false
	}
	if s.accountID != "" {
		// A gateway workspace: only the gateway's admin endpoint may do this.
		writeErr(w, 403, "unavailable", "computer assignment is an administrator action")
		return true
	}
	s.serveComputerAttach(w, r, op)
	return true
}

// adminComputer serves /api/admin/accounts/{id}/computer/{state|attach|detach}.
func (g *AccountGateway) adminComputer(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/accounts/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	id, op, ok := strings.Cut(rest, "/computer/")
	if !ok || strings.Contains(op, "/") {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		writeErr(w, 404, "not_found", "account not found")
		return true
	}
	var target Account
	if g.root.store.db.QueryRowContext(r.Context(), `SELECT id,username,email,role,disabled,must_change_password,legacy FROM accounts WHERE id=?`, id).
		Scan(&target.ID, &target.Username, &target.Email, &target.Role, &target.Disabled, &target.MustChangePassword, &target.Legacy) != nil {
		writeErr(w, 404, "not_found", "account not found")
		return true
	}
	workspace, err := g.workspace(target)
	if err != nil {
		writeErr(w, 503, "workspace_unavailable", "workspace unavailable")
		return true
	}
	workspace.serveComputerAttach(w, r, op)
	return true
}

// ---- the outbox copy before a detach ----

// syncOutboxForDetach copies the files in the computer's outbox to the server
// before the computer goes away (owner answer Q1: "synchronized at run end and
// before detach"). The files become attachments of origin `outbox` in the
// account's most recently active chat, with the same caps and ledger as the
// end-of-run sync. Best effort: a failure leaves the files on the computer and
// the detach goes on.
func (s *Server) syncOutboxForDetach(ctx context.Context, slot *computerSlot, report *DetachReport) {
	if s.store == nil {
		return
	}
	var convID string
	if s.store.db.QueryRow(`SELECT id FROM conversations WHERE kind='dm' AND archived=0 AND user_visible=1 AND bot_id IS NOT NULL ORDER BY updated_at DESC LIMIT 1`).Scan(&convID) != nil {
		return
	}
	conv, err := s.store.GetConversation(convID)
	if err != nil || conv.BotID == "" {
		return
	}
	run := Run{ID: "detach-sync:" + uuid.NewString(), BotID: conv.BotID, ConversationID: conv.ID}
	ctx, cancel := context.WithTimeout(ctx, detachSyncTimeout)
	defer cancel()
	files, err := s.listOutbox(ctx, run)
	if err != nil {
		log.Printf("[outbox] detach: listing failed, files stay on the computer: %v", err)
		return
	}
	var total int64
	names := map[string]bool{}
	for _, f := range files {
		if s.store.outboxAlreadySynced(f.Path, f.Size, f.MTimeNS) {
			continue
		}
		if f.Size > outboxFileBytes {
			report.Outbox.Refused++
			continue
		}
		if total+f.Size > outboxRunBytes {
			report.Outbox.Deferred++
			continue
		}
		a, _, err := s.importGuestFile(ctx, run, conv, guestFileImport{
			Path: f.Path, Name: outboxAttachmentName(f.Path, names), NoSymlinks: true,
			Opts: attachmentOpts{Origin: attachmentOriginOutbox, BotID: conv.BotID, MaxBytes: outboxFileBytes},
		})
		if err != nil {
			if ctx.Err() != nil || storageErrorCode(err) != "" {
				report.Outbox.Deferred++
				break
			}
			log.Printf("[outbox] detach: a file was not copied and stays on the computer: %v", err)
			report.Outbox.Refused++
			continue
		}
		digest := s.store.attachmentDigest(a.ID)
		if existing := s.store.outboxContentSynced(f.Path, digest); existing != "" && existing != a.ID {
			_ = s.store.deleteUnboundAttachment(a.ID)
			s.store.recordOutboxSynced(f.Path, f.Size, f.MTimeNS, digest, existing)
			continue
		}
		s.store.recordOutboxSynced(f.Path, f.Size, f.MTimeNS, digest, a.ID)
		total += f.Size
		report.Outbox.Synced++
	}
}
