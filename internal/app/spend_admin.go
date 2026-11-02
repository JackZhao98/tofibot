package app

import (
	"log"
	"net/http"
	"time"
)

// adminAccountSpend is one account's month in GET /api/admin/spend.
type adminAccountSpend struct {
	AccountID string  `json:"account_id"`
	Month     string  `json:"month,omitempty"`
	SpendUSD  float64 `json:"spend_usd"`
	LimitUSD  int     `json:"limit_usd"`
	State     string  `json:"state"`
	// Available is false for an account that has not published a month yet (never started, disabled or being deleted).
	Available bool `json:"available"`
}

// adminSpend lets an admin read each account's spend this month and its limit.
// It only reads: the limit is the account owner's setting.
func (g *AccountGateway) adminSpend(w http.ResponseWriter, r *http.Request, a Account) bool {
	if r.URL.Path != "/api/admin/spend" {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method_not_allowed", "use GET")
		return true
	}
	rows, err := g.root.store.db.QueryContext(r.Context(), `SELECT id,username,email,role,disabled,legacy,deleting FROM accounts ORDER BY created_at,id`)
	if err != nil {
		writeErr(w, 500, "storage", "cannot list accounts")
		return true
	}
	var accounts []Account
	for rows.Next() {
		var x Account
		if rows.Scan(&x.ID, &x.Username, &x.Email, &x.Role, &x.Disabled, &x.Legacy, &x.Deleting) != nil {
			rows.Close()
			writeErr(w, 500, "storage", "cannot list accounts")
			return true
		}
		accounts = append(accounts, x)
	}
	rows.Close()
	out := []adminAccountSpend{}
	at := time.Now()
	for _, x := range accounts {
		item := adminAccountSpend{AccountID: x.ID, State: "off"}
		if !x.Disabled && !x.Deleting {
			// Read the published row; opening a workspace here would boot a runtime per account.
			var month, monthEnd string
			err := g.root.store.db.QueryRowContext(r.Context(), `SELECT month,month_end,spend_usd,limit_usd FROM account_spend WHERE account_id=?`, x.ID).Scan(&month, &monthEnd, &item.SpendUSD, &item.LimitUSD)
			if err == nil {
				item.Month, item.Available = month, true
				if end, e := time.Parse(time.RFC3339Nano, monthEnd); e == nil && !at.Before(end) {
					item.SpendUSD = 0 // the published month is over; nothing is spent in the new one until a call books
				}
				item.State = spendState(item.SpendUSD, item.LimitUSD)
			}
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"version": 1, "accounts": out})
	return true
}

// recordSpendSnapshot keeps one row per account: the workspace's own month and limit.
func (g *AccountGateway) recordSpendSnapshot(accountID string, st SpendStatus) {
	if _, err := g.root.store.db.Exec(`INSERT INTO account_spend(account_id,month,month_end,spend_usd,limit_usd,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(account_id) DO UPDATE SET month=excluded.month,month_end=excluded.month_end,spend_usd=excluded.spend_usd,limit_usd=excluded.limit_usd,updated_at=excluded.updated_at`,
		accountID, st.Month, st.MonthEnd, st.SpendUSD, st.LimitUSD, now()); err != nil {
		log.Printf("[spend] publish for account %s: %v", accountID, err)
	}
}
