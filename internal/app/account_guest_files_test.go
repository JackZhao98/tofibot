package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
)

func TestAccountGuestFileUploadDownloadGeneratedAndReferences(t *testing.T) {
	sockets, err := os.MkdirTemp("/tmp", "tofi-files-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockets)
	var servers []*Server
	var bots []Bot
	var roots []string
	var dataDirs []string
	g := accountFixture(t)
	if _, err := g.create(context.Background(), "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true)); err != nil {
		t.Fatal(err)
	}
	socketByAccount := map[string]string{}
	var cookies []*http.Cookie
	g.runtimeFactory = func(c Config) (*Server, error) {
		c.ComputerSocket = socketByAccount[filepath.Base(c.DataDir)]
		c.Engine = testEngine{}
		return NewServer(c)
	}
	for i := 0; i < 2; i++ {
		root := t.TempDir()
		service, err := guest.New(root, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close(context.Background())
		socket := filepath.Join(sockets, string(rune('a'+i))+".sock")
		ln, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		httpServer := &http.Server{Handler: service.Handler()}
		go httpServer.Serve(ln)
		defer httpServer.Close()
		a, err := g.create(context.Background(), fmt.Sprintf("user-%d", i), "", "SyntheticPassword123!", false, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID); err != nil {
			t.Fatal(err)
		}
		socketByAccount[a.ID] = socket
		dataDir := filepath.Join(g.config.DataDir, "accounts", a.ID)
		s, err := g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		cookies = append(cookies, accountCookie(t, g, a))
		bot, err := s.store.CreateBot("fixture", "", "model")
		if err != nil {
			t.Fatal(err)
		}
		servers = append(servers, s)
		bots = append(bots, bot)
		roots = append(roots, root)
		dataDirs = append(dataDirs, dataDir)
	}
	s := servers[0]
	bot := bots[0]
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "synthetic.txt")
	part.Write([]byte("file on own guest disk"))
	form.Close()
	request := httptest.NewRequest("POST", "/api/conversations/"+bot.DMConversationID+"/attachments", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.TLS = &tls.ConnectionState{}
	request.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, request)
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	var a Attachment
	if err = json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	_, path, err := s.store.Attachment(a.ID)
	if err != nil || path != guestAttachmentPrefix+a.ID {
		t.Fatalf("guest metadata %q %v", path, err)
	}
	if _, err = os.Stat(filepath.Join(dataDirs[0], "attachments", a.ID+".upload")); err == nil {
		t.Fatal("host fallback")
	}
	if text, _, err := s.store.ReadAttachmentText(a.ID, bot.DMConversationID); err != nil || text != "file on own guest disk" {
		t.Fatalf("model read %q %v", text, err)
	}
	download := func(index int, id string) *httptest.ResponseRecorder {
		return accountRequest(g, "GET", "/api/attachments/"+id, "", cookies[index])
	}
	if w := download(0, a.ID); w.Code != 200 || w.Body.String() != "file on own guest disk" {
		t.Fatalf("download %d %q", w.Code, w.Body.String())
	}
	if w := download(1, a.ID); w.Code != 404 {
		t.Fatal("other account read attachment")
	}
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "synthetic generation", "generation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`UPDATE runs SET status='running' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	generated, err := s.store.AddAttachment(conv.ID, "generated.txt", "text/plain", strings.NewReader("generated once"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.store.stageRunAttachment(run, conv, generated, "same-operation")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.store.AddAttachment(conv.ID, "generated.txt", "text/plain", strings.NewReader("generated once"))
	if err != nil {
		t.Fatal(err)
	}
	reused, err := s.store.stageRunAttachment(run, conv, duplicate, "same-operation")
	if err != nil || reused.ID != first.ID {
		t.Fatal("generated retry duplicated reference")
	}
	client, _ := computer.New(computer.Config{Socket: servers[0].microVM.Socket()})
	if _, err = client.GetBlob(context.Background(), duplicate.ID); err == nil {
		t.Fatal("duplicate alias leaked quota")
	}
	if _, err = client.GetBlob(context.Background(), first.ID); err != nil {
		t.Fatal("live generated object deleted")
	}
	objects, _ := os.ReadDir(filepath.Join(roots[0], "shared/.tofi/blobs/objects"))
	if len(objects) != 2 {
		t.Fatalf("physical objects %d", len(objects))
	}
	if err = s.store.deleteUnboundAttachment(a.ID); err != nil {
		t.Fatal(err)
	}
	if w := download(0, a.ID); w.Code != 404 {
		t.Fatal("deleted metadata readable")
	}
	objects, _ = os.ReadDir(filepath.Join(roots[0], "shared/.tofi/blobs/objects"))
	if len(objects) != 1 {
		t.Fatal("unbound object not reclaimed")
	}
}
