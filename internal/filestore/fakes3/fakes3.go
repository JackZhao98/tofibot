// Package fakes3 is an in-process S3 stand-in for tests: path-style
// PUT/GET/HEAD/DELETE/list on one bucket, conditional put, Range, one access
// key, and switches to fail or open the bucket. It is not a conformance suite.
package fakes3

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type object struct {
	data []byte
	meta http.Header
}

type Server struct {
	*httptest.Server
	Bucket, AccessKey string

	mu      sync.Mutex
	objects map[string]object
	// Down answers every request with 503.
	Down bool
	// Public lets requests with no Authorization header read objects.
	Public bool
	// Paths logs "METHOD /path" of every request (no query, no headers).
	Paths []string
	// NoConditional ignores If-None-Match; NoRange ignores Range.
	NoConditional, NoRange bool
}

func New(bucket, accessKey string) *Server {
	s := &Server{Bucket: bucket, AccessKey: accessKey, objects: map[string]object{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *Server) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for k := range s.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) Object(key string) ([]byte, http.Header, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	return o.data, o.meta, ok
}

// Delete removes an object directly, as an outside actor would.
func (s *Server) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
}

// Set writes an object directly, as another writer would.
func (s *Server) Set(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = object{data: data, meta: http.Header{}}
}

func fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	down, public := s.Down, s.Public
	s.Paths = append(s.Paths, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	if down {
		fail(w, 503, "ServiceUnavailable")
		return
	}
	auth := r.Header.Get("Authorization")
	switch {
	case auth == "" && !(public && (r.Method == http.MethodGet || r.Method == http.MethodHead)):
		fail(w, 403, "AccessDenied")
		return
	case auth != "" && !strings.Contains(auth, "Credential="+s.AccessKey+"/"):
		fail(w, 403, "InvalidAccessKeyId")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")
	if bucket != s.Bucket {
		fail(w, 404, "NoSuchBucket")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		if r.URL.Query().Has("location") {
			fmt.Fprint(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></LocationConstraint>`)
			return
		}
		s.list(w, r.URL.Query().Get("prefix"))
		return
	}
	switch r.Method {
	case http.MethodPut:
		if inm := r.Header.Get("If-None-Match"); inm == "*" && !s.NoConditional {
			if _, ok := s.objects[key]; ok {
				fail(w, 412, "PreconditionFailed")
				return
			}
		}
		body, err := readBody(r)
		if err != nil {
			fail(w, 400, "IncompleteBody")
			return
		}
		meta := http.Header{}
		for _, h := range []string{"Content-Type", "Content-Disposition", "X-Amz-Meta-Sha256"} {
			meta.Set(h, r.Header.Get(h))
		}
		s.objects[key] = object{data: body, meta: meta}
		w.Header().Set("ETag", `"fake"`)
	case http.MethodGet, http.MethodHead:
		o, ok := s.objects[key]
		if !ok {
			fail(w, 404, "NoSuchKey")
			return
		}
		for k, v := range o.meta {
			w.Header()[k] = v
		}
		w.Header().Set("ETag", `"fake"`)
		w.Header().Set("Last-Modified", time.Unix(1700000000, 0).UTC().Format(http.TimeFormat))
		if s.NoRange {
			r.Header.Del("Range")
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(o.data))
	case http.MethodDelete:
		delete(s.objects, key)
		w.WriteHeader(204)
	default:
		fail(w, 405, "MethodNotAllowed")
	}
}

func (s *Server) list(w http.ResponseWriter, prefix string) {
	type contents struct {
		Key  string
		Size int
	}
	out := struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		Name     string
		KeyCount int
		Contents []contents
	}{Name: s.Bucket}
	for k, o := range s.objects {
		if strings.HasPrefix(k, prefix) {
			out.Contents = append(out.Contents, contents{k, len(o.data)})
		}
	}
	sort.Slice(out.Contents, func(i, j int) bool { return out.Contents[i].Key < out.Contents[j].Key })
	out.KeyCount = len(out.Contents)
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(out)
}

// readBody returns the payload, decoding aws-chunked streaming signatures
// (what minio-go sends over plain HTTP).
func readBody(r *http.Request) ([]byte, error) {
	if !strings.Contains(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING") {
		return io.ReadAll(r.Body)
	}
	br := bufio.NewReader(r.Body)
	var out []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeHex, _, _ := strings.Cut(strings.TrimSpace(line), ";")
		n, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
		chunk := make([]byte, n)
		if _, err = io.ReadFull(br, chunk); err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if _, err = br.Discard(2); err != nil {
			return nil, err
		}
	}
}
