package app

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Account credential policy, NIST SP 800-63B style: a length floor, no
// composition rules, and a screen against passwords that are known-common or
// derived from the account's own identity. Setup, admin-created accounts and
// password changes all use it.
//
// The blocklist is the single source of truth for the server and the UI. The
// UI ships a byte-identical copy at ui/src/password-blocklist.json;
// TestPasswordBlocklistUIMirror fails when the two differ.

const (
	passwordMinChars = 12
	passwordMaxBytes = 1024
	usernameMinBytes = 3
	usernameMaxBytes = 64
	emailMaxBytes    = 254
	// An identity token shorter than this is only rejected on equality: a
	// two-letter mailbox would otherwise forbid most passwords.
	identityContainsMin = 4
)

//go:embed password_blocklist.json
var passwordBlocklistJSON []byte

var commonPasswords = func() map[string]struct{} {
	var list []string
	if err := json.Unmarshal(passwordBlocklistJSON, &list); err != nil {
		panic("password_blocklist.json: " + err.Error())
	}
	set := make(map[string]struct{}, len(list))
	for _, item := range list {
		set[strings.ToLower(item)] = struct{}{}
	}
	return set
}()

// accountFieldError names the field a client should point at. Code is the
// stable error.code; Field is one of username, email, password.
type accountFieldError struct {
	Code    string
	Field   string
	Message string
}

func (e *accountFieldError) Error() string { return e.Message }

func writeFieldErr(w http.ResponseWriter, status int, e *accountFieldError) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": e.Code, "field": e.Field, "message": e.Message}})
}

func checkUsername(username string) *accountFieldError {
	bad := &accountFieldError{Code: "invalid_username", Field: "username", Message: "use a 3–64 character username of letters, digits, '.', '_' or '-'"}
	if len(username) < usernameMinBytes || len(username) > usernameMaxBytes {
		return bad
	}
	for _, c := range username {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-' && c != '.' {
			return bad
		}
	}
	return nil
}

func checkEmail(email string) *accountFieldError {
	addr, err := mail.ParseAddress(email)
	if len(email) > emailMaxBytes || err != nil || addr.Address != email {
		return &accountFieldError{Code: "invalid_email", Field: "email", Message: "use a valid email address"}
	}
	return nil
}

// checkPassword applies the password rules for an account with this identity.
func checkPassword(username, email, password string) *accountFieldError {
	if utf8.RuneCountInString(password) < passwordMinChars || len(password) > passwordMaxBytes {
		return &accountFieldError{Code: "weak_password", Field: "password", Message: "use a password of at least 12 characters (at most 1024 bytes)"}
	}
	lower := strings.ToLower(password)
	for _, token := range identityTokens(username, email) {
		if lower == token || (utf8.RuneCountInString(token) >= identityContainsMin && strings.Contains(lower, token)) {
			return &accountFieldError{Code: "password_contains_identity", Field: "password", Message: "the password must not contain the username or email"}
		}
	}
	if isCommonPassword(lower) {
		return &accountFieldError{Code: "common_password", Field: "password", Message: "this password is too common"}
	}
	return nil
}

func identityTokens(username, email string) []string {
	var tokens []string
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			tokens = append(tokens, value)
		}
	}
	add(username)
	add(email)
	if at := strings.LastIndex(email, "@"); at > 0 {
		add(email[:at])
	}
	return tokens
}

// isCommonPassword expects a lowercased password. Besides the list it rejects
// a short unit repeated ("aaaaaaaaaaaa", "abcabcabcabc") and one straight run
// of consecutive characters ("abcdefghijklm", "987654321098" is in the list).
func isCommonPassword(lower string) bool {
	if _, ok := commonPasswords[lower]; ok {
		return true
	}
	runes := []rune(lower)
	for unit := 1; unit <= 4 && unit < len(runes); unit++ {
		if len(runes)%unit != 0 {
			continue
		}
		repeated := true
		for i := unit; i < len(runes); i++ {
			if runes[i] != runes[i-unit] {
				repeated = false
				break
			}
		}
		if repeated {
			return true
		}
	}
	if len(runes) > 1 {
		step := runes[1] - runes[0]
		if step == 1 || step == -1 {
			straight := true
			for i := 2; i < len(runes); i++ {
				if runes[i]-runes[i-1] != step {
					straight = false
					break
				}
			}
			if straight {
				return true
			}
		}
	}
	return false
}

// checkAccountFields validates a new account in field order.
func checkAccountFields(username, email, password string) *accountFieldError {
	if e := checkUsername(username); e != nil {
		return e
	}
	if e := checkEmail(email); e != nil {
		return e
	}
	return checkPassword(username, email, password)
}

func validateOwner(username, email, password string) bool {
	return checkAccountFields(username, email, password) == nil
}
