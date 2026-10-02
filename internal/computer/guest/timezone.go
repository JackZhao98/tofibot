package guest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const guestTimezoneFile = ".tofi-timezone"

type timezoneArgs struct {
	Timezone string `json:"timezone"`
}

func (s *Service) timezonePath() string { return filepath.Join(s.root, guestTimezoneFile) }

func (s *Service) timezoneGet() (map[string]any, error) {
	b, err := os.ReadFile(s.timezonePath())
	if os.IsNotExist(err) {
		return map[string]any{"timezone": "", "configured": false}, nil
	}
	if err != nil {
		return nil, err
	}
	zone := strings.TrimSpace(string(b))
	if zone == "" {
		return map[string]any{"timezone": "", "configured": false}, nil
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return nil, errors.New("stored timezone is invalid")
	}
	return map[string]any{"timezone": zone, "configured": true}, nil
}

func (s *Service) timezoneSet(args timezoneArgs) (map[string]any, error) {
	zone := strings.TrimSpace(args.Timezone)
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return nil, errors.New("timezone must be a valid IANA timezone")
		}
	}
	path := s.timezonePath()
	if zone == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return s.timezoneGet()
	}
	tmp, err := os.CreateTemp(s.root, ".tofi-timezone-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString(zone + "\n")
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return nil, fmt.Errorf("persist timezone: %w", err)
	}
	return s.timezoneGet()
}

func decodeTimezoneArgs(raw []byte) (timezoneArgs, error) {
	var args timezoneArgs
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return args, err
	}
	return args, nil
}
