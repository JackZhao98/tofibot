package guest

import "testing"

func TestTerminalRunsJobRule(t *testing.T) {
	const shell = "# tofi-default-terminal\nexec bash -i"
	cases := []struct {
		name, command string
		job, known    bool
		want          bool
	}{
		{"explicit command, no output, no job field", "sleep 600", false, false, true},
		{"explicit command with args", "python build.py --all", false, true, true},
		{"shell at prompt", shell, false, true, false},
		{"shell running a job", shell, true, true, true},
		{"shell, job unknown", shell, false, false, false},
		{"bare bash -i", "bash -i", false, true, false},
		{"bash running a script is a command", "bash build.sh", false, true, true},
		{"empty command is a shell", "", false, true, false},
	}
	for _, c := range cases {
		if got := terminalRunsJob(c.command, c.job, c.known); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
