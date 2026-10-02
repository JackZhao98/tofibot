package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/google/uuid"
)

func (g *AccountGateway) computerIdentity(a Account) string {
	if a.Legacy && g.legacyComputerPhase == "worker" {
		return g.config.AccountLegacyComputerUUID
	}
	return a.ID
}

// The operator transfers ownership offline. Startup validates its durable
// Worker proof before any account runtime or background job is opened.
func (g *AccountGateway) validateLegacyComputer(ctx context.Context) error {
	_, err := g.root.store.db.Exec(`CREATE TABLE IF NOT EXISTS account_computer_bindings(source_id TEXT PRIMARY KEY,computer_uuid TEXT NOT NULL,instance_id TEXT NOT NULL)`)
	if err != nil {
		return err
	}
	var saved string
	if err = g.root.store.db.QueryRow(`SELECT COALESCE(MAX(computer_uuid),'') FROM account_computer_bindings WHERE source_id='legacy-owner'`).Scan(&saved); err != nil {
		return err
	}
	id := g.config.AccountLegacyComputerUUID
	if id == "" {
		if saved != "" {
			return errors.New("legacy computer binding requires explicit adoption configuration")
		}
		return nil
	}
	instance, err := uuid.Parse(g.root.instance.ID)
	if err != nil || instance.String() != g.root.instance.ID {
		return errors.New("canonical instance UUID required")
	}
	expected := uuid.NewSHA1(uuid.MustParse("79d8c04b-3d65-4b99-97bf-f4e8a5630d47"), []byte(instance.String()+":legacy-owner")).String()
	if id != expected || (saved != "" && saved != id) {
		return errors.New("legacy computer mapping does not match installation")
	}
	var exists bool
	if g.root.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts WHERE id='legacy-owner' AND legacy=1)`).Scan(&exists) != nil || !exists {
		return errors.New("imported legacy owner required")
	}
	data, err := g.computerControl(ctx, map[string]string{"op": "adoption_status", "account_id": id})
	if err != nil {
		return err
	}
	var proof struct {
		AccountID  string `json:"account_id"`
		InstanceID string `json:"instance_id"`
		AssetID    string `json:"asset_id"`
		Phase      string `json:"phase"`
		Verified   bool   `json:"verified"`
	}
	if json.Unmarshal(data, &proof) != nil || proof.AccountID != id || proof.InstanceID != instance.String() || proof.AssetID != "personal" || !proof.Verified || (proof.Phase != "worker" && proof.Phase != "legacy") {
		return errors.New("verified legacy computer ownership unavailable")
	}
	if proof.Phase == "legacy" && !filepath.IsAbs(g.config.ComputerSocket) {
		return errors.New("rollback requires explicit legacy computer socket")
	}
	_, err = g.root.store.db.Exec(`INSERT INTO account_computer_bindings VALUES('legacy-owner',?,?) ON CONFLICT(source_id) DO NOTHING`, id, instance.String())
	if err == nil {
		g.legacyComputerPhase = proof.Phase
	}
	return err
}
