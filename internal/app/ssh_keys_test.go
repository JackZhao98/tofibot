package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/computer"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func executeSSHFixture(t *testing.T, home string, op sshKeyOperation) sshKeyResult {
	t.Helper()
	for _, tool := range []string{"python3", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " missing")
		}
	}
	payload, _ := json.Marshal(op)
	cmd := exec.Command("python3", "-c", computerSSHKeyScript, base64.StdEncoding.EncodeToString(payload))
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture command failed: %v: %s", err, out)
	}
	var result sshKeyResult
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid fixture output: %s", out)
	}
	return result
}
func TestSSHKeysGenerateDiscoverAndNeverOverwrite(t *testing.T) {
	home := t.TempDir()
	op := sshKeyOperation{Action: "generate", Name: "id_ed25519.work"}
	result := executeSSHFixture(t, home, op)
	if result.Error != "" || result.Key == nil || result.Key.PublicKey == "" || !result.Key.PublicKeyVerified {
		t.Fatalf("generate: %+v", result)
	}
	path := filepath.Join(home, ".ssh", op.Name)
	private, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private mode")
	}
	before := result.Key.Fingerprint
	// Corrupt neighboring .pub: discovery must derive from actual private material.
	os.WriteFile(path+".pub", []byte("not a public key\n"), 0644)
	list := executeSSHFixture(t, home, sshKeyOperation{Action: "list"})
	if len(list.Keys) != 1 || list.Keys[0].Fingerprint != before || list.Keys[0].PublicKey == "" {
		t.Fatalf("discovery: %+v", list)
	}
	result = executeSSHFixture(t, home, op)
	if result.Error != "already_exists" {
		t.Fatalf("overwrite accepted: %+v", result)
	}
	after, _ := os.ReadFile(path)
	if string(private) != string(after) {
		t.Fatal("existing key modified")
	}
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), "PRIVATE KEY") || strings.Contains(string(encoded), string(private)) {
		t.Fatal("private material returned")
	}
}
func TestSSHKeysImportValidationAndPublicOnly(t *testing.T) {
	source := t.TempDir()
	generated := executeSSHFixture(t, source, sshKeyOperation{Action: "generate", Name: "id_ed25519"})
	private, _ := os.ReadFile(filepath.Join(source, ".ssh", "id_ed25519"))
	home := t.TempDir()
	result := executeSSHFixture(t, home, sshKeyOperation{Action: "import", Name: "imported", PrivateKey: string(private)})
	if result.Error != "" || result.Key == nil || result.Key.PublicKey != generated.Key.PublicKey {
		t.Fatalf("import: %+v", result)
	}
	result = executeSSHFixture(t, home, sshKeyOperation{Action: "import", Name: "broken", PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----"})
	if result.Error != "invalid_private_key" {
		t.Fatal("fake key accepted")
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "broken")); !os.IsNotExist(err) {
		t.Fatal("invalid key installed")
	}
	os.WriteFile(filepath.Join(home, ".ssh", "publiconly.pub"), []byte(generated.Key.PublicKey+"\n"), 0644)
	os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), private, 0600)
	os.Symlink(filepath.Join(home, ".ssh", "imported"), filepath.Join(home, ".ssh", "linked"))
	list := executeSSHFixture(t, home, sshKeyOperation{Action: "list"})
	if len(list.Keys) != 2 {
		t.Fatalf("unexpected discovery: %+v", list)
	}
	for _, key := range list.Keys {
		if key.Name == "publiconly" && key.HasPrivate {
			t.Fatal("public only claimed private")
		}
	}
}
func TestSSHKeysEncryptedImportNeverPrompts(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "encrypted")
	cmd := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "synthetic-passphrase", "-f", path)
	if err := cmd.Run(); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	private, _ := os.ReadFile(path)
	public, _ := os.ReadFile(path + ".pub")
	result := executeSSHFixture(t, t.TempDir(), sshKeyOperation{Action: "import", Name: "encrypted", PrivateKey: string(private), PublicKey: string(public)})
	if result.Error != "" || result.Key == nil || !result.Key.Encrypted || result.Key.PublicKeyVerified || result.Key.PublicKey == "" {
		t.Fatalf("encrypted import: %+v", result)
	}
}
func TestSSHKeyNamesAndToolPrivateInputBoundary(t *testing.T) {
	for _, name := range []string{"../id", "config", "known_hosts", "authorized_keys", "key.pub", ".hidden", "a/b"} {
		if validSSHKeyName(name) {
			t.Fatalf("invalid name %q", name)
		}
	}
	if !validSSHKeyName("id_ed25519.work") {
		t.Fatal("dotted name rejected")
	}
	server, count := secretTestServer(t)
	_, err := server.sshKeyTool(Run{BotID: "bot", ID: "run"}).Execute(context.Background(), json.RawMessage(`{"action":"import","name":"key","private_key":"secret"}`))
	if err == nil || *count != 0 {
		t.Fatal("tool accepted direct private input")
	}
}

func TestSSHKeyToolIsRegisteredAndExecutesThroughComputer(t *testing.T) {
	calls := 0
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-ssh-key.sock", Client: &http.Client{Transport: secretTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		var action computer.Action
		if err := json.NewDecoder(req.Body).Decode(&action); err != nil {
			t.Fatal(err)
		}
		if action.Name != "shell.exec" || action.BotID != "bot" || (action.RunID != "run" && action.RunID != "settings") {
			t.Fatal("computer request lost scope")
		}
		result := sshKeyResult{Key: &computerSSHKey{Name: "id_ed25519", PublicKey: "ssh-ed25519 synthetic-public", Source: "computer"}}
		out, _ := json.Marshal(result)
		body, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"exit_code": 0, "stdout": string(out)}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{microVM: client, computerOwners: map[string]string{}}
	found := false
	for _, tool := range server.microVMTools(Run{BotID: "bot", ID: "run"}) {
		if tool.Name == "computer_ssh_keys" {
			found = true
			result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"generate","name":"id_ed25519"}`))
			if err != nil || !strings.Contains(result, "synthetic-public") {
				t.Fatalf("tool failed: %v %s", err, result)
			}
		}
	}
	if !found || calls != 1 {
		t.Fatal("SSH tool unavailable")
	}
	server.computerOwners["bot"] = "active-other-run"
	if _, err = server.computerSSHKeys(context.Background(), Run{BotID: "bot", ID: "settings"}, sshKeyOperation{Action: "list"}); err != nil {
		t.Fatal(err)
	}
	if server.computerOwners["bot"] != "active-other-run" {
		t.Fatal("metadata polling stole control")
	}
}
