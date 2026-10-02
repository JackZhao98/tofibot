package guest

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

type privateBrowserInput struct {
	Origin string `json:"origin"`
	Text   string `json:"text"`
}

// A single synchronous page operation checks the destination and active field
// before setting it. Splitting inspection from desktop paste permits focus to
// move to an ordinary field in between. No secret is put on the clipboard.
const privateBrowserInputFunction = `(input => {
  if (location.origin !== input.origin || !document.hasFocus() || document.visibilityState !== 'visible') return false;
  let field = document.activeElement;
  while (field && field.shadowRoot && field.shadowRoot.activeElement) field = field.shadowRoot.activeElement;
  if (!(field instanceof HTMLInputElement) || field.type !== 'password' || field.disabled || field.readOnly || !field.isConnected) return false;
  const rect = field.getBoundingClientRect();
  const style = getComputedStyle(field);
  if (rect.width <= 0 || rect.height <= 0 || rect.bottom <= 0 || rect.right <= 0 || rect.top >= innerHeight || rect.left >= innerWidth || style.visibility !== 'visible' || style.display === 'none' || Number(style.opacity) === 0) return false;
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
  setter.call(field, input.text);
  field.dispatchEvent(new Event('input', {bubbles:true}));
  field.dispatchEvent(new Event('change', {bubbles:true}));
  return true;
})`

func privateBrowserInputExpression(input privateBrowserInput) (string, error) {
	u, err := url.Parse(input.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || input.Origin != "https://"+u.Host {
		return "", errors.New("private input requires an exact HTTPS origin")
	}
	if len(input.Text) > MaxDesktopInput || !utf8.ValidString(input.Text) || strings.ContainsRune(input.Text, 0) {
		return "", errors.New("invalid private input")
	}
	raw, _ := json.Marshal(input)
	return privateBrowserInputFunction + "(" + string(raw) + ")", nil
}
