package app

import "context"

// The confirm path (the approval card) is added with slice S5; until then a confirm-class change is refused with its reason.
func (sr settingRun) confirmChange(ctx context.Context, d SettingDef, t Target, value any, oldText, newText, lang string) (string, error) {
	return "", settingsDenied("settings_change_needs_approval", "This change needs the person's approval, which is not available yet.")
}

func (sr settingRun) confirmRevert(ctx context.Context, d SettingDef, t Target, change SettingsChange, value any, lang string) (string, error) {
	return "", settingsDenied("settings_change_needs_approval", "This change needs the person's approval, which is not available yet.")
}
