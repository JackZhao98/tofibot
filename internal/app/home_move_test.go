package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer/e2b"
)

// fakeGuestFiles answers files.export_chunk from src and files.import_chunk into dst, the way the guest does:
// ordered chunks only, "exists" when the final file is already there.
func fakeGuestFiles(src map[string][]byte, dst map[string][]byte) (guestCall, guestCall) {
	from := func(_ context.Context, name string, args any) (json.RawMessage, error) {
		a := args.(map[string]any)
		data := src[a["path"].(string)]
		off := a["offset"].(int64)
		end := off + int64(a["limit"].(int))
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		out, _ := json.Marshal(guestWireChunk{Data: base64.StdEncoding.EncodeToString(data[off:end]), Next: end, Size: int64(len(data)), EOF: end == int64(len(data)), Version: "v1"})
		return out, nil
	}
	parts := map[string][]byte{}
	to := func(_ context.Context, name string, args any) (json.RawMessage, error) {
		a := args.(map[string]any)
		path := a["path"].(string)
		off := a["offset"].(int64)
		if off == 0 {
			if _, ok := dst[path]; ok {
				return json.RawMessage(`{"status":"exists"}`), nil
			}
			parts[path] = nil
		}
		if int64(len(parts[path])) != off {
			return nil, errors.New("import chunk out of order")
		}
		b, _ := base64.StdEncoding.DecodeString(a["data_base64"].(string))
		parts[path] = append(parts[path], b...)
		if int64(len(parts[path])) == a["total_size"].(int64) {
			dst[path] = parts[path]
		}
		return json.RawMessage(`{"status":"ok"}`), nil
	}
	return from, to
}

func TestCopyGuestFileStreamsEveryPageInOrder(t *testing.T) {
	big := bytes.Repeat([]byte("0123456789abcdef"), (1<<20)/16*3+7) // three pages and a bit
	src := map[string][]byte{"/workspace/home/uploads/.tofi-move-x/part.aa": big}
	dst := map[string][]byte{}
	from, to := fakeGuestFiles(src, dst)
	if err := copyGuestFile(context.Background(), from, to, "/workspace/home/uploads/.tofi-move-x/part.aa"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst["/workspace/home/uploads/.tofi-move-x/part.aa"], big) {
		t.Fatalf("copied %d bytes, want %d", len(dst["/workspace/home/uploads/.tofi-move-x/part.aa"]), len(big))
	}
}

func TestCopyGuestFileRefusesAStalePart(t *testing.T) {
	path := "/workspace/home/uploads/.tofi-move-x/part.aa"
	from, to := fakeGuestFiles(map[string][]byte{path: []byte("new")}, map[string][]byte{path: []byte("old")})
	if err := copyGuestFile(context.Background(), from, to, path); err == nil {
		t.Fatal("a part already on the target must fail the move, not be kept")
	}
}

func TestHomeMoveStateOneAtATimeAndNamedFailure(t *testing.T) {
	var m homeMoveState
	if m.view() != nil {
		t.Fatal("no move: no view")
	}
	if !m.begin("e2b") || m.begin("vm") {
		t.Fatal("only one move at a time")
	}
	if v := m.view(); v == nil || v.Phase != "moving" || v.Target != "e2b" {
		t.Fatalf("moving view = %+v", v)
	}
	m.end(homeMoveError{"target_disk_full"})
	if v := m.view(); v == nil || v.Phase != "failed" || v.ErrorCode != "target_disk_full" {
		t.Fatalf("failed view = %+v", v)
	}
	if !m.begin("e2b") {
		t.Fatal("a failed move can be tried again")
	}
	m.end(nil)
	if m.view() != nil {
		t.Fatal("a finished move leaves no view")
	}
	if got := homeMoveCode(fmt.Errorf("wrapped: %w", &e2b.Error{Code: "invalid_key"})); got != "invalid_key" {
		t.Fatalf("an E2B refusal names its own reason, got %q", got)
	}
	if homeMoveCode(errors.New("other")) != "move_failed" {
		t.Fatal("an unnamed error is move_failed")
	}
}
