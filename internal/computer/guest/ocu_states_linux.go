//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// ocuStatesScript walks one app's accessibility tree on the desktop bus and
// prints the state of every check box, toggle and radio button in document
// order, from the AT-SPI states CHECKED, PRESSED (toggle buttons) and
// INDETERMINATE. It reads the active window when there is one. The engine does
// not print these states, so this is how a Bot learns whether a box is on.
const ocuStatesScript = `
import gi, json, sys
gi.require_version("Atspi", "2.0")
from gi.repository import Atspi
want = sys.argv[1].lower()
roles = {"check box", "toggle button", "switch", "radio button", "check menu item", "radio menu item"}
out = []
def state_of(ss):
    if ss.contains(Atspi.StateType.INDETERMINATE):
        return "mixed"
    if ss.contains(Atspi.StateType.CHECKED) or ss.contains(Atspi.StateType.PRESSED):
        return "checked"
    return "unchecked"
def walk(node, depth):
    if depth > 60 or len(out) > 3000:
        return
    role = node.get_role_name()
    if role in roles:
        out.append({"role": role, "name": node.get_name() or "", "state": state_of(node.get_state_set())})
    for i in range(node.get_child_count()):
        child = node.get_child_at_index(i)
        if child is not None:
            walk(child, depth + 1)
desktop = Atspi.get_desktop(0)
for i in range(desktop.get_child_count()):
    app = desktop.get_child_at_index(i)
    if app is None or (app.get_name() or "").lower() != want:
        continue
    frames = [app.get_child_at_index(j) for j in range(app.get_child_count())]
    frames = [f for f in frames if f is not None]
    active = [f for f in frames if f.get_state_set().contains(Atspi.StateType.ACTIVE)]
    for f in (active or frames):
        walk(f, 0)
    break
print(json.dumps(out))
`

// ocuCheckStates returns the check states of one app's active window.
func ocuCheckStates(ctx context.Context, root string, d *desktop, app string) ([]ocuCheckState, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", ocuStatesScript, app)
	cmd.Dir = d.botDir
	env := append(cleanEnv(root, d.botDir), "DISPLAY="+d.display)
	cmd.Env = withDesktopA11yEnv(env, d.runtimeDir)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var states []ocuCheckState
	if err := json.Unmarshal(out, &states); err != nil {
		return nil, err
	}
	return states, nil
}
