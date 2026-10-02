package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
)

const oauthOwnerPrefix = "human-oauth:"
const vmOAuthLifetime = 10 * time.Minute

var errOAuthComputerBusy = errors.New("shared computer is busy")

type vmOAuthSession struct {
	id, botID, server, guestID, flowID string
	targetID                           string
	mu                                 sync.Mutex
	status, err                        string
	ended                              time.Time
	cancel                             context.CancelFunc
	done                               chan struct{}
}

func (v *vmOAuthSession) owner() string { return oauthOwnerPrefix + v.id }
func (v *vmOAuthSession) snapshot() map[string]any {
	v.mu.Lock()
	defer v.mu.Unlock()
	return map[string]any{"session_id": v.id, "bot_id": v.botID, "status": v.status, "error": v.err}
}

func (s *Server) closeVMOAuth() {
	s.vmOAuthMu.Lock()
	s.vmOAuthClosing = true
	for _, v := range s.vmOAuth {
		v.cancel()
	}
	s.vmOAuthMu.Unlock()
	s.vmOAuthWG.Wait()
}

// Reserve the same scheduler resource as ordinary graphical runs. Human input
// for this Bot may coexist with this reservation; no Bot run may bypass it.
func (s *Server) startVMOAuth(requestCtx context.Context, botID, name string) (_ *vmOAuthSession, resultErr error) {
	if s.microVM == nil {
		return nil, errors.New("shared computer is not configured")
	}
	bot, err := s.store.GetBot(botID)
	if err != nil || bot.Archived {
		return nil, errors.New("an active Bot is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), vmOAuthLifetime)
	v := &vmOAuthSession{id: uuid.NewString(), botID: botID, server: name, status: "preparing", cancel: cancel, done: make(chan struct{})}
	s.vmOAuthMu.Lock()
	if s.vmOAuthClosing {
		s.vmOAuthMu.Unlock()
		cancel()
		return nil, errors.New("server is stopping")
	}
	if s.vmOAuth == nil {
		s.vmOAuth = map[string]*vmOAuthSession{}
	}
	// Keep terminal status available briefly, without retaining an unbounded log.
	for id, old := range s.vmOAuth {
		old.mu.Lock()
		ended := old.ended
		old.mu.Unlock()
		if !ended.IsZero() && time.Since(ended) > vmOAuthLifetime {
			delete(s.vmOAuth, id)
		}
	}
	for _, old := range s.vmOAuth {
		old.mu.Lock()
		active := old.ended.IsZero()
		old.mu.Unlock()
		if active {
			s.vmOAuthMu.Unlock()
			cancel()
			return nil, errOAuthComputerBusy
		}
	}
	if len(s.vmOAuth) >= 32 {
		s.vmOAuthMu.Unlock()
		cancel()
		return nil, errors.New("too many recent authorization attempts; retry shortly")
	}
	s.vmOAuth[v.id] = v
	s.vmOAuthWG.Add(1)
	s.vmOAuthMu.Unlock()
	stopRequestCancel := context.AfterFunc(requestCtx, cancel)
	handedOff := false
	defer func() {
		stopRequestCancel()
		if !handedOff {
			s.finishVMOAuth(v, "error", "无法启动授权，请重试。")
		}
	}()
	lease := s.computerLease(botID)
	if !lease.TryLock() {
		return nil, errOAuthComputerBusy
	}
	defer lease.Unlock()
	if !s.claimComputerOwner("oauth:"+botID, v.owner()) {
		return nil, errOAuthComputerBusy
	}
	guest, err := s.microVM.OAuthStart(ctx)
	if err != nil {
		return nil, err
	}
	v.guestID = guest.SessionID
	flowID, authURL, err := s.extensions.OAuthStart(ctx, name, guest.RedirectURI)
	if err != nil {
		return nil, err
	}
	v.flowID = flowID
	u, err := url.Parse(authURL)
	if err != nil || u.Query().Get("state") == "" {
		return nil, errors.New("invalid authorization response")
	}
	if _, err = s.microVM.OAuthArm(ctx, v.guestID, u.Query().Get("state")); err != nil {
		return nil, err
	}
	if err = s.oauthGuestAction(ctx, v, "desktop.hold", map[string]any{"ttl_sec": 120}); err != nil {
		return nil, err
	}
	if err = s.oauthGuestAction(ctx, v, "browser.action", map[string]any{"action": "new", "url": authURL}); err != nil {
		return nil, err
	}
	if !stopRequestCancel() || requestCtx.Err() != nil {
		return nil, requestCtx.Err()
	}
	v.mu.Lock()
	v.status = "pending"
	v.mu.Unlock()
	handedOff = true
	go s.pollVMOAuth(ctx, v)
	return v, nil
}

func (s *Server) oauthGuestAction(ctx context.Context, v *vmOAuthSession, action string, args any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := s.microVM.Action(callCtx, computer.Action{BotID: v.botID, RunID: v.owner(), Source: "human", Name: action, Args: raw})
	if err == nil && !result.OK {
		return errors.New("shared computer action failed")
	}
	if err == nil && action == "browser.action" {
		var page struct {
			Action   string `json:"action"`
			TargetID string `json:"target_id"`
		}
		if json.Unmarshal(result.Result, &page) == nil && page.Action == "new" {
			v.targetID = page.TargetID
		}
	}
	return err
}
func (s *Server) pollVMOAuth(ctx context.Context, v *vmOAuthSession) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	hold := time.NewTicker(15 * time.Second)
	defer hold.Stop()
	for {
		select {
		case <-ctx.Done():
			status := "cancelled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				status = "expired"
			}
			s.finishVMOAuth(v, status, "")
			return
		case <-hold.C:
			// Input acquisition and hold refresh use the same short lease, so
			// a keepalive cannot reappear between release and takeover.
			lease := s.computerLease(v.botID)
			if lease.TryLock() {
				s.computerOwnerMu.Lock()
				human := strings.HasPrefix(s.computerOwners[v.botID], "human-control:")
				s.computerOwnerMu.Unlock()
				if !human {
					_ = s.oauthGuestAction(ctx, v, "desktop.hold", map[string]any{"ttl_sec": 120})
				}
				lease.Unlock()
			}
		case <-ticker.C:
			result, err := s.microVM.OAuthPoll(ctx, v.guestID)
			if ctx.Err() != nil {
				continue
			}
			if err != nil {
				s.finishVMOAuth(v, "error", "共享电脑连接中断，请重新授权。")
				return
			}
			switch result.Status {
			case "pending":
				continue
			case "complete":
				err = s.extensions.OAuthCallback(ctx, v.flowID, result.Code, result.State)
				if err != nil {
					s.finishVMOAuth(v, "error", "授权交换失败，请检查服务的回调配置后重试。")
					return
				}
				_, _ = s.store.WorkspaceEvent(workspaceScopeConfig)
				s.finishVMOAuth(v, "complete", "")
				return
			case "denied":
				s.finishVMOAuth(v, "denied", "授权未获批准，可重新连接。")
				return
			default:
				s.finishVMOAuth(v, "expired", "")
				return
			}
		}
	}
}

// Only the initialization path or its single worker calls finish. Stop/Close
// cancel that worker and wait, so token exchange cannot race a second cleanup.
func (s *Server) finishVMOAuth(v *vmOAuthSession, status, detail string) {
	defer s.vmOAuthWG.Done()
	v.cancel()
	if v.flowID != "" {
		s.extensions.OAuthCancel(v.flowID)
	}
	if v.guestID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_, _ = s.microVM.OAuthCancel(ctx, v.guestID)
		cancel()
	}
	// Close admission while any in-flight acquisition finishes. Do not hold
	// the computer lease while locking an input session: input requests take
	// those locks in the opposite order.
	s.computerOwnerMu.Lock()
	reserved := s.computerOwners["oauth:"+v.botID] == v.owner()
	s.computerOwnerMu.Unlock()
	closingOwner := "human-oauth-closing:" + v.id
	var control *computerControl
	if reserved {
		lease := s.computerLease(v.botID)
		lease.Lock()
		s.computerOwnerMu.Lock()
		s.computerOwners["oauth:"+v.botID] = closingOwner
		control = s.computerControls[v.botID]
		s.desktopChangedLocked()
		s.computerOwnerMu.Unlock()
		lease.Unlock()
	}
	if control != nil {
		control.mu.Lock()
		s.releaseControl(control)
		control.mu.Unlock()
	}
	if reserved {
		if v.targetID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			args, _ := json.Marshal(map[string]string{"action": "close", "target_id": v.targetID})
			// Viewer source prevents cleanup from restarting a stopped desktop.
			_, _ = s.microVM.Action(ctx, computer.Action{BotID: v.botID, RunID: v.owner(), Source: "viewer", Name: "browser.action", Args: args})
			cancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		_ = s.oauthGuestAction(ctx, v, "desktop.release", struct{}{})
		cancel()
		s.releaseComputerOwner("oauth:"+v.botID, closingOwner)
	}
	v.mu.Lock()
	v.status = status
	v.err = detail
	v.ended = time.Now()
	v.mu.Unlock()
	close(v.done)
}

func (s *Server) routeVMOAuth(w http.ResponseWriter, r *http.Request, p string) bool {
	if p == "extensions/oauth-options" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"vm_available": s.microVM != nil, "web_callback_origin": s.webOAuthOrigin(r), "desktop_redirect_uri": desktopOAuthRedirect})
		return true
	}
	parts := strings.Split(p, "/")
	if len(parts) < 5 || parts[0] != "extensions" || parts[1] != "mcp" || parts[3] != "oauth" || parts[4] != "vm" {
		return false
	}
	name := parts[2]
	if len(parts) == 6 && parts[5] == "start" && r.Method == http.MethodPost {
		var in struct {
			BotID string `json:"bot_id"`
		}
		if err := decodeExtensionJSON(r, &in); err != nil || in.BotID == "" {
			writeErr(w, 400, "invalid_request", "bot_id is required")
			return true
		}
		v, err := s.startVMOAuth(r.Context(), in.BotID, name)
		if err != nil {
			if errors.Is(err, errOAuthComputerBusy) {
				writeErr(w, 409, "computer_busy", "电脑正在执行其他操作，请稍后再连接。")
			} else {
				writeErr(w, 400, "oauth", extensions.OAuthPublicError(err))
			}
			return true
		}
		writeJSON(w, 200, v.snapshot())
		return true
	}
	if len(parts) == 7 && ((parts[6] == "status" && r.Method == http.MethodGet) || (parts[6] == "cancel" && r.Method == http.MethodPost)) {
		s.vmOAuthMu.Lock()
		v := s.vmOAuth[parts[5]]
		s.vmOAuthMu.Unlock()
		if v == nil || v.server != name {
			writeErr(w, 404, "oauth", "authorization session not found")
			return true
		}
		if parts[6] == "cancel" {
			v.cancel()
			select {
			case <-v.done:
			case <-r.Context().Done():
				return true
			case <-time.After(20 * time.Second):
				writeErr(w, 503, "oauth", "授权正在结束，请稍后重试。")
				return true
			}
		}
		writeJSON(w, 200, v.snapshot())
		return true
	}
	writeErr(w, 404, "not_found", "not found")
	return true
}

// Transfer the guest's ordinary idle hold around human-input acquisition. The
// host reservation stays in place throughout, keeping every Bot queued.
func (s *Server) oauthInputHold(ctx context.Context, botID string, release bool) error {
	s.computerOwnerMu.Lock()
	owner := s.computerOwners["oauth:"+botID]
	s.computerOwnerMu.Unlock()
	if !strings.HasPrefix(owner, oauthOwnerPrefix) {
		return nil
	}
	action := "desktop.hold"
	args := json.RawMessage(`{"ttl_sec":120}`)
	if release {
		action = "desktop.release"
		args = json.RawMessage(`{}`)
	}
	callCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	result, err := s.microVM.Action(callCtx, computer.Action{BotID: botID, RunID: owner, Source: "human", Name: action, Args: args})
	if err == nil && !result.OK {
		return errors.New("authorization desktop hold transfer failed")
	}
	return err
}
