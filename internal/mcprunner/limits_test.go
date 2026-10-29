package mcprunner

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLimitsHelperProcess(t *testing.T) {
	if os.Getenv("TOFI_LIMITS_HELPER") != "1" {
		return
	}
	var out []string
	for _, r := range []int{unix.RLIMIT_NPROC, unix.RLIMIT_NOFILE, unix.RLIMIT_FSIZE} {
		var l unix.Rlimit
		_ = unix.Getrlimit(r, &l)
		out = append(out, fmt.Sprintf("%d/%d", l.Cur, l.Max))
	}
	fmt.Print("LIMITS ", strings.Join(out, " "))
	os.Exit(0)
}

func TestLauncherAppliesLimitsBeforeTheCommandRuns(t *testing.T) {
	command, args := Limits{Nproc: 1500, Nofile: 300, Fsize: 1 << 20}.launch(os.Args[0], []string{"-test.run=TestLimitsHelperProcess"})
	if command == os.Args[0] && args[0] == "-test.run=TestLimitsHelperProcess" {
		t.Fatal("limits were not wrapped around the command")
	}
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), "TOFI_LIMITS_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(string(out), "LIMITS 1500/1500 300/300 1048576/1048576") {
		t.Fatalf("child limits: %s", out)
	}
	if c, a := (Limits{}).launch("/bin/x", []string{"y"}); c != "/bin/x" || len(a) != 1 {
		t.Fatalf("zero limits must not wrap: %s %v", c, a)
	}
}

func TestLauncherIgnoresOrdinaryArguments(t *testing.T) {
	if RunLauncher([]string{"tofi-mcp-runner"}) || RunLauncher([]string{"x", "-test.run=Foo"}) {
		t.Fatal("an ordinary start was taken for the launcher")
	}
}

func TestInstallAndImportAreRefusedWhenTheDataDiskIsNearlyFull(t *testing.T) {
	free := uint64(10 << 20)
	old := freeBytes
	freeBytes = func(string) (uint64, error) { return free, nil }
	defer func() { freeBytes = old }()
	hub, root := newTestHub(t, func(c *HubConfig) { c.MinFreeBytes = 512 << 20 })
	install := map[string]any{"id": "gog", "kind": "builtin_gog"}
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), install); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("install on a full disk: %d %s", w.Code, w.Body.String())
	}
	request := ImportRequest{Plugins: []ImportPlugin{{Request: InstallRequest{ID: "gog", Kind: "builtin_gog"}, Files: []ImportFile{importFile("home/x", "y")}}}}
	if got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request)); got[0].State != ImportFailed {
		t.Fatalf("import on a full disk: %+v", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "accounts", accountA, "state", "plugins")); len(entries) != 0 {
		t.Fatalf("a refused install left %v", entries)
	}
	free = 600 << 20
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), install); w.Code != http.StatusCreated {
		t.Fatalf("install with room: %d %s", w.Code, w.Body.String())
	}
}

func TestDefaultsApplyLimitsOnlyWithIsolation(t *testing.T) {
	plain, err := NewHub(HubConfig{Root: t.TempDir(), Master: testMaster})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if !plain.cfg.Limits.zero() || plain.cfg.MinFreeBytes != 1<<30 {
		t.Fatalf("plain hub: %+v free=%d", plain.cfg.Limits, plain.cfg.MinFreeBytes)
	}
	iso, err := NewHub(HubConfig{Root: t.TempDir(), Master: testMaster, IsolateUIDs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Close()
	if iso.cfg.Limits != DefaultLimits || DefaultLimits.Nproc != 128 || DefaultLimits.Nofile != 1024 || DefaultLimits.Fsize == 0 {
		t.Fatalf("isolated hub: %+v", iso.cfg.Limits)
	}
}
