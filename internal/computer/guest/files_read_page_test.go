package guest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesReadPagesThroughALargeFile(t *testing.T) {
	s := newTestService(t)
	dir, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	const lines = 5000
	var want strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&want, "line %05d %s\n", i, strings.Repeat("x", 180))
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(want.String()), 0660); err != nil {
		t.Fatal(err)
	}
	if want.Len() <= MaxFileBytes*2 {
		t.Fatalf("fixture too small: %d", want.Len())
	}

	var got []string
	offset, pages := 1, 0
	for offset > 0 {
		pages++
		if pages > 100 {
			t.Fatal("paging did not finish")
		}
		res, err := s.readFile(context.Background(), testBot, fileArgs{Path: "big.txt", Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		if res["total_bytes"].(int64) != int64(want.Len()) || res["total_lines"].(int) != lines {
			t.Fatalf("totals = %v %v", res["total_bytes"], res["total_lines"])
		}
		got = append(got, strings.Split(res["content"].(string), "\n")...)
		next, more := res["next_offset"].(int)
		if res["truncated"].(bool) != more {
			t.Fatalf("truncated=%v next_offset present=%v", res["truncated"], more)
		}
		if !more {
			break
		}
		if next <= offset {
			t.Fatalf("next_offset %d does not advance past %d", next, offset)
		}
		offset = next
	}
	if pages < 3 {
		t.Fatalf("pages = %d, want the file to need several", pages)
	}
	if len(got) != lines || got[0] != strings.Split(want.String(), "\n")[0] || got[lines-1] != strings.Split(want.String(), "\n")[lines-1] {
		t.Fatalf("pages rebuilt %d lines, want %d", len(got), lines)
	}
}

func TestFilesReadSmallFileHasNoNextOffset(t *testing.T) {
	s := newTestService(t)
	dir, _ := s.botDir(testBot)
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("a\nb\nc\n"), 0660); err != nil {
		t.Fatal(err)
	}
	res, err := s.readFile(context.Background(), testBot, fileArgs{Path: "small.txt", Offset: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res["content"] != "b" || res["next_offset"] != 3 || res["total_lines"] != 3 {
		t.Fatalf("result = %v", res)
	}
	res, _ = s.readFile(context.Background(), testBot, fileArgs{Path: "small.txt"})
	if _, has := res["next_offset"]; has || res["truncated"] != false {
		t.Fatalf("whole read = %v", res)
	}
}
