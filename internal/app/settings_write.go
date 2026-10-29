package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JackZhao98/tofibot/internal/manual"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The change path of the settings tool. Every write goes through the Store or Server method that already owns it (busy checks, archive
// semantics, workspace events stay there); this file validates the value, decides the change class, records the change and tells the
// person. Class direct is applied here; class confirm waits for the person's card (settings_gate.go).

// settingWriter applies one validated value.
type settingWriter func(ctx context.Context, sr settingRun, t Target, value any) error

func bindSettingWrite(key string, write settingWriter) {
	b := settingBindings[key]
	if b == nil {
		b = &settingBinding{}
		settingBindings[key] = b
	}
	b.write = write
}

var (
	errChangeNotRevertable   = errors.New("change is not revertable")
	errChangeAlreadyReverted = errors.New("change already reverted")
	errChangeStale           = errors.New("setting changed since")
)

func settingsInvalid(format string, args ...any) error {
	return tooloutcome.New(tooloutcome.Validation, "settings_invalid_value", "not_executed", fmt.Sprintf(format, args...), "repair_arguments").Err()
}

func settingsDenied(code, message string) error {
	return tooloutcome.New(tooloutcome.Denied, code, "not_executed", message, "explain_blocker").Err()
}

// parseSettingValue turns the call's value into the typed value the key's kind needs. null means "back to the default" and is allowed
// only for the automation keys, which have one.
func parseSettingValue(d SettingDef, raw json.RawMessage) (any, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, settingsInvalid("value is required.")
	}
	if text == "null" {
		if strings.HasPrefix(d.Key, "automation.") {
			return nil, nil
		}
		return nil, settingsInvalid("value must not be null for this key.")
	}
	asString := func() (string, bool) {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s, true
		}
		return "", false
	}
	switch d.Kind {
	case KindBool:
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			return b, nil
		}
		if s, ok := asString(); ok {
			if v, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
				return v, nil
			}
		}
		return nil, settingsInvalid("value must be true or false.")
	case KindInt:
		var f float64
		if json.Unmarshal(raw, &f) != nil {
			if s, ok := asString(); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					f = float64(n)
				} else {
					return nil, settingsInvalid("value must be a whole number.")
				}
			} else {
				return nil, settingsInvalid("value must be a whole number.")
			}
		}
		if f != float64(int(f)) {
			return nil, settingsInvalid("value must be a whole number.")
		}
		n := int(f)
		if n < d.Min || n > d.Max {
			return nil, settingsInvalid("value must be between %d and %d.", d.Min, d.Max)
		}
		return n, nil
	case KindBotList:
		var list []string
		if json.Unmarshal(raw, &list) != nil {
			return nil, settingsInvalid("value must be an array of strings.")
		}
		seen := map[string]bool{}
		out := make([]string, 0, len(list))
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			out = append(out, item)
		}
		sort.Strings(out)
		return out, nil
	case KindCollection, KindAction, KindReadOnly, KindNone:
		return nil, settingsInvalid("this key takes no value.")
	}
	s, ok := asString()
	if !ok {
		return nil, settingsInvalid("value must be a string.")
	}
	s = strings.TrimSpace(s)
	switch d.Kind {
	case KindEnum:
		for _, o := range d.Options {
			if o == s {
				return s, nil
			}
		}
		return nil, settingsInvalid("value must be one of: %s.", strings.Join(d.Options, ", "))
	case KindLanguage:
		if s != "" && !uiLanguages[s] {
			return nil, settingsInvalid("value must be one of: %s, or empty for automatic.", strings.Join(sortedKeys(uiLanguages), ", "))
		}
	case KindTimezone:
		if s == "" {
			return nil, settingsInvalid("value must be an IANA timezone name.")
		}
	case KindString:
		if d.MaxRunes > 0 && len([]rune(s)) > d.MaxRunes {
			return nil, settingsInvalid("value is too long: at most %d characters.", d.MaxRunes)
		}
	}
	return s, nil
}

// settingText is the plain text of a value, the form the audit table stores and the notice shows.
func settingText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []string:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// valueFromText is the inverse of settingText for the kinds that can be reverted.
func valueFromText(d SettingDef, text string) (any, error) {
	switch d.Kind {
	case KindBool:
		return strconv.ParseBool(text)
	case KindInt:
		return strconv.Atoi(text)
	case KindBotList:
		var list []string
		if err := json.Unmarshal([]byte(text), &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	return text, nil
}

// settingLoosens says whether moving from one value to the other makes a limit looser.
func settingLoosens(d SettingDef, from, to string) bool {
	rank := map[string]int{"read_only": 0, "write": 1, "high_risk": 2}
	switch d.Key {
	case "connection.tool_tier":
		a, ok1 := rank[from]
		b, ok2 := rank[to]
		return ok1 && ok2 && b < a
	case "permissions.approval_mode":
		return to == "auto" && from != "auto"
	}
	return false
}

// settingCurrentText reads the current value of a key as text.
func (sr settingRun) settingCurrentText(ctx context.Context, d SettingDef, t Target) (string, error) {
	b := settingBindings[d.Key]
	if b == nil || b.read == nil {
		return "", nil
	}
	v, err := b.read(ctx, sr, t)
	if err != nil {
		return "", err
	}
	return settingText(v.Value), nil
}

func (sr settingRun) forbiddenError(d SettingDef, t Target, lang string) error {
	return settingsDenied("settings_forbidden", fmt.Sprintf("%s Page: %s (%s)", settingsForbiddenText, settingLinkLabel(d, lang), settingLinkPath(d, t)))
}

func (sr settingRun) set(ctx context.Context, in settingsToolInput) (string, error) {
	d, t, err := sr.lookup(in)
	if err != nil {
		return "", err
	}
	lang := sr.s.settingsLanguage()
	if d.Risk.Self == RiskForbidden {
		return "", sr.forbiddenError(d, t, lang)
	}
	b := settingBindings[d.Key]
	if b == nil || b.write == nil {
		return "", settingsDenied("settings_not_settable", fmt.Sprintf("The tool does not change this key. The person changes it on its page: %s (%s)", settingLinkLabel(d, lang), settingLinkPath(d, t)))
	}
	if !sr.s.runAttended(sr.c, sr.r) {
		return "", settingsDenied("settings_change_needs_owner", "No person attends this run, so nothing was changed. Do not retry or route around it; say in your reply what change is needed so the person can make it in chat or on the page.")
	}
	var value any
	if d.Kind != KindAction {
		if value, err = parseSettingValue(d, in.Value); err != nil {
			return "", err
		}
	}
	oldText, err := sr.settingCurrentText(ctx, d, t)
	if err != nil {
		return "", mapSettingsWriteError(err)
	}
	newText := settingText(value)
	if item, isAuto := automationItemFor(strings.TrimPrefix(d.Key, "automation.")); isAuto && value == nil {
		newText = item.Default // back to the default: the audit shows the value that applies
	}
	if d.Kind != KindAction && oldText == newText {
		return settingsOutput(map[string]any{"applied": false, "unchanged": true, "key": d.Key, "value": oldText, "link": settingLinkPath(d, t), "link_label": settingLinkLabel(d, lang)})
	}
	cc := sr.changeContext(d, t)
	cc.Loosen = settingLoosens(d, oldText, newText)
	switch class := d.RiskFor(cc); class {
	case RiskDirect:
		return sr.applyDirect(ctx, d, t, value, oldText, newText, lang)
	case RiskConfirm:
		return sr.confirmChange(ctx, d, t, value, oldText, newText, lang)
	}
	return "", sr.forbiddenError(d, t, lang)
}

// applyDirect makes a change of class direct: cap, record, write, notice.
func (sr settingRun) applyDirect(ctx context.Context, d SettingDef, t Target, value any, oldText, newText, lang string) (string, error) {
	limit := sr.s.autoInt(autoSettingsChangeLimit)
	if sr.s.store.runSettingsChangeCount(sr.r.ID) >= limit {
		return "", settingsDenied("settings_change_limit", fmt.Sprintf("This run already made %d settings changes, the most it may make. Do not retry; list the remaining changes in your reply so the person can make them or ask again.", limit))
	}
	change, err := sr.applyChange(ctx, d, t, value, oldText, newText, changeActorBot, RiskDirect, "")
	if err != nil {
		return "", err
	}
	return settingsOutput(map[string]any{"applied": true, "key": d.Key, "old": clipNoticeValue(change.Old), "new": clipNoticeValue(change.New), "change_id": change.ID, "revertable": change.Revertable, "link": settingLinkPath(d, t), "link_label": settingLinkLabel(d, lang)})
}

// applyChange records the change, runs the writer and, for a direct change by a Bot, posts the notice with its Undo.
func (sr settingRun) applyChange(ctx context.Context, d SettingDef, t Target, value any, oldText, newText, actor string, risk RiskClass, approvalID string) (SettingsChange, error) {
	b := settingBindings[d.Key]
	row := SettingsChange{ActorKind: actor, BotID: sr.r.BotID, RunID: sr.r.ID, ConversationID: sr.c.ID, Key: d.Key,
		TargetBotID: t.BotID, TargetConversationID: t.ConversationID, TargetConnection: t.Connection, TargetTool: t.Tool,
		Old: oldText, New: newText, Risk: string(risk), ApprovalQuestionID: approvalID}
	if d.Audit {
		var err error
		if row, err = sr.s.store.beginSettingsChange(row); err != nil {
			return row, err
		}
	}
	if err := b.write(ctx, sr, t, value); err != nil {
		if d.Audit {
			_ = sr.s.store.finishSettingsChange(row.ID, false, "")
		}
		return row, mapSettingsWriteError(err)
	}
	if !d.Audit {
		return row, nil
	}
	noticeID := ""
	if actor == changeActorBot && risk == RiskDirect {
		noticeID, _ = sr.s.store.addSettingsChangeNotice(row, d.LabelKey, manual.Term("en", d.LabelKey))
	}
	if err := sr.s.store.finishSettingsChange(row.ID, true, noticeID); err != nil {
		return row, err
	}
	row.NoticeMessageID = noticeID
	return row, nil
}

// mapSettingsWriteError turns a store refusal into a typed outcome the model can act on; unknown errors stay errors.
func mapSettingsWriteError(err error) error {
	if _, ok := tooloutcome.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, ErrGroupBusy):
		return tooloutcome.New(tooloutcome.Transient, "settings_target_busy", "not_executed", "The conversation has queued or running work. Nothing was changed; try again when it is idle.", "retry").Err()
	case errors.Is(err, sql.ErrNoRows):
		return settingsDenied("settings_target_not_found", "The target does not exist any more. Nothing was changed.")
	}
	var applyErr *automationApplyError
	if errors.As(err, &applyErr) {
		return settingsInvalid("%s", applyErr.Error())
	}
	return settingsInvalid("%s", err.Error())
}

// ---- revert

func (sr settingRun) revert(ctx context.Context, in settingsToolInput) (string, error) {
	if !sr.s.runAttended(sr.c, sr.r) {
		return "", settingsDenied("settings_change_needs_owner", "No person attends this run, so nothing was changed. Say in your reply what change is needed.")
	}
	change, err := sr.s.store.settingsChange(strings.TrimSpace(in.ChangeID))
	if err != nil {
		return "", tooloutcome.New(tooloutcome.NeedInformation, "settings_change_not_found", "not_executed", "No recorded change has this ID.", "repair_arguments").Err()
	}
	d, ok := settingDefFor(change.Key)
	if !ok {
		return "", settingsDenied("settings_change_not_revertable", "This change cannot be undone with the tool.")
	}
	lang := sr.s.settingsLanguage()
	t := change.target()
	if err := sr.checkRevertable(ctx, d, t, change); err != nil {
		return "", err
	}
	cc := sr.changeContext(d, t)
	cc.Loosen = settingLoosens(d, change.New, change.Old)
	value, err := valueFromText(d, change.Old)
	if err != nil {
		return "", settingsDenied("settings_change_not_revertable", "This change cannot be undone with the tool.")
	}
	switch d.RiskFor(cc) {
	case RiskDirect:
		reverted, err := sr.applyChange(ctx, d, t, value, change.New, change.Old, changeActorRevert, RiskDirect, "")
		if err != nil {
			return "", err
		}
		if err := sr.s.store.markSettingsReverted(change.ID, reverted.ID); err != nil {
			return "", mapSettingsWriteError(err)
		}
		sr.s.store.markSettingsNoticeReverted(change)
		return settingsOutput(map[string]any{"applied": true, "reverted": change.ID, "key": d.Key, "value": clipNoticeValue(change.Old), "link": settingLinkPath(d, t), "link_label": settingLinkLabel(d, lang)})
	case RiskConfirm:
		return sr.confirmRevert(ctx, d, t, change, value, lang)
	}
	return "", sr.forbiddenError(d, t, lang)
}

// checkRevertable refuses a change that was reverted, cannot be restored from the table, or whose setting changed again since.
func (sr settingRun) checkRevertable(ctx context.Context, d SettingDef, t Target, c SettingsChange) error {
	switch {
	case c.RevertedBy != "":
		return settingsDenied("settings_change_reverted", "This change was already undone.")
	case !c.Revertable || c.ActorKind == changeActorRevert || d.Kind == KindAction || d.Risk.Self == RiskForbidden:
		return settingsDenied("settings_change_not_revertable", "This change cannot be undone with the tool.")
	}
	current, err := sr.settingCurrentText(ctx, d, t)
	if err != nil {
		return err
	}
	if current != c.New {
		return settingsDenied("settings_changed_since", "The setting changed again after this change. Read it with get and make the change you want with set.")
	}
	return nil
}

// revertSettingsChangeByPerson is the Undo button and the Maintenance list: the person is the owner of the workspace, so no card is
// needed. It restores the old value only while the setting still holds the value the change wrote.
func (s *Server) revertSettingsChangeByPerson(ctx context.Context, id string) (SettingsChange, error) {
	change, err := s.store.settingsChange(id)
	if err != nil {
		return change, err
	}
	d, ok := settingDefFor(change.Key)
	if !ok {
		return change, errChangeNotRevertable
	}
	sr := settingRun{s: s}
	t := change.target()
	switch {
	case change.RevertedBy != "":
		return change, errChangeAlreadyReverted
	case !change.Revertable || change.ActorKind == changeActorRevert || d.Kind == KindAction || d.Risk.Self == RiskForbidden:
		return change, errChangeNotRevertable
	}
	if current, err := sr.settingCurrentText(ctx, d, t); err != nil || current != change.New {
		if err != nil {
			return change, err
		}
		return change, errChangeStale
	}
	value, err := valueFromText(d, change.Old)
	if err != nil {
		return change, errChangeNotRevertable
	}
	reverted, err := sr.applyChange(ctx, d, t, value, change.New, change.Old, changeActorRevert, RiskDirect, "")
	if err != nil {
		return change, err
	}
	if err := s.store.markSettingsReverted(change.ID, reverted.ID); err != nil {
		return change, errChangeAlreadyReverted
	}
	s.store.markSettingsNoticeReverted(change)
	change.RevertedBy = reverted.ID
	return change, nil
}
