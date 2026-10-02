package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// This journal contains ownership intent, not commands or output. It deliberately
// outlives deleted runs so Stop/deletion can still clean up a parked run's PTYs.
func migrateTerminalCleanup(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_terminal_cleanup (
		run_id TEXT PRIMARY KEY, bot_id TEXT NOT NULL, computer_socket TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`)
	return err
}

// Persist before sending a mutation: a lost response must not lose ownership.
// A changed service socket never silently transfers this obligation to another VM.
func (s *Store) registerRunTerminals(ctx context.Context, r Run, socket string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO run_terminal_cleanup(run_id,bot_id,computer_socket,created_at)
		SELECT id,bot_id,?,? FROM runs WHERE id=? AND bot_id=? AND status='running'
		ON CONFLICT(run_id) DO UPDATE SET run_id=excluded.run_id
		WHERE run_terminal_cleanup.bot_id=excluded.bot_id AND run_terminal_cleanup.computer_socket=excluded.computer_socket`, socket, now(), r.ID, r.BotID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.New("terminal ownership unavailable: run is not active or its computer changed")
	}
	return nil
}

func (s *Server) wakeTerminalCleanup() {
	if s.terminalCleanupWake != nil {
		select {
		case s.terminalCleanupWake <- struct{}{}:
		default:
		}
	}
}

func (s *Server) startTerminalCleanup() {
	if s.microVM == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.terminalCleanupCancel = cancel
	s.terminalCleanupWake = make(chan struct{}, 1)
	s.terminalCleanupDone = make(chan struct{})
	go func() {
		defer close(s.terminalCleanupDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			s.reconcileTerminalCleanup(ctx)
			select {
			case <-ctx.Done():
				return
			case <-s.terminalCleanupWake:
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) stopTerminalCleanup() {
	if s.terminalCleanupCancel == nil {
		return
	}
	s.terminalCleanupCancel()
	<-s.terminalCleanupDone
	// Workers have unwound. Best effort here; failed acknowledgements remain
	// durable and are retried on the next startup, never by replaying commands.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.reconcileTerminalCleanup(ctx)
}

func (s *Server) reconcileTerminalCleanup(ctx context.Context) {
	if s.microVM == nil || ctx.Err() != nil {
		return
	}
	// Completed runs retain terminal tabs, matching the existing product contract.
	_, _ = s.store.db.ExecContext(ctx, `DELETE FROM run_terminal_cleanup WHERE run_id IN (SELECT id FROM runs WHERE status='done')`)
	rows, err := s.store.db.QueryContext(ctx, `SELECT j.run_id,j.bot_id FROM run_terminal_cleanup j
		LEFT JOIN runs r ON r.id=j.run_id
		WHERE j.computer_socket=? AND (r.id IS NULL OR r.status IN ('cancelled','failed','interrupted'))
		ORDER BY j.created_at LIMIT 32`, s.microVM.Socket())
	if err != nil {
		return
	}
	var pending []Run
	for rows.Next() {
		var r Run
		if err = rows.Scan(&r.ID, &r.BotID); err != nil {
			break
		}
		pending = append(pending, r)
	}
	readErr := rows.Err()
	rows.Close() // Store has one connection; do not hold it across guest calls.
	if err != nil || readErr != nil {
		return
	}
	for _, r := range pending {
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		_, executing := s.runs[r.ID]
		s.mu.Unlock()
		if executing {
			continue // Let in-flight model actions unwind before cleanup.
		}
		callCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		_, err := s.microVM.Action(callCtx, computer.Action{BotID: r.BotID, RunID: r.ID, Source: "model", Name: "terminal.cancel_run", Args: json.RawMessage(`{}`)})
		cancel()
		if err == nil {
			_, _ = s.store.db.ExecContext(ctx, `DELETE FROM run_terminal_cleanup WHERE run_id=? AND computer_socket=?`, r.ID, s.microVM.Socket())
		}
	}
}
