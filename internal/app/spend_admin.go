package app

import (
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
	// Available is false for an account whose workspace cannot be opened (disabled or being deleted).
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
	for _, x := range accounts {
		item := adminAccountSpend{AccountID: x.ID, State: "off"}
		if !x.Disabled && !x.Deleting {
			if ws, err := g.workspace(x); err == nil && ws.store != nil {
				if st, e := ws.store.spendStatus(time.Now()); e == nil {
					item.Month, item.SpendUSD, item.LimitUSD, item.State, item.Available = st.Month, st.SpendUSD, st.LimitUSD, st.State, true
				}
			}
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"version": 1, "accounts": out})
	return true
}
