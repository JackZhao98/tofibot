package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

type instanceIdentity struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
}

func initializeInstance(dir, environment string) (instanceIdentity, error) {
	if environment == "" {
		environment = "personal"
	}
	if environment != "personal" && environment != "acceptance" {
		return instanceIdentity{}, errors.New("TOFI_ENVIRONMENT must be personal or acceptance")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return instanceIdentity{}, err
	}
	path := filepath.Join(dir, "instance.json")
	body, err := os.ReadFile(path)
	if err == nil {
		var identity instanceIdentity
		if json.Unmarshal(body, &identity) != nil || identity.ID == "" {
			return identity, errors.New("invalid instance identity")
		}
		if identity.Environment != environment {
			return identity, errors.New("data directory belongs to a different environment; use a separate empty directory")
		}
		return identity, nil
	}
	if !os.IsNotExist(err) {
		return instanceIdentity{}, err
	}
	if environment == "acceptance" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return instanceIdentity{}, err
		}
		if len(entries) != 0 {
			return instanceIdentity{}, errors.New("acceptance environment requires an empty data directory")
		}
	}
	identity := instanceIdentity{ID: uuid.NewString(), Environment: environment}
	body, _ = json.Marshal(identity)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return identity, err
	}
	_, err = f.Write(body)
	closeErr := f.Close()
	if err != nil {
		return identity, err
	}
	return identity, closeErr
}
