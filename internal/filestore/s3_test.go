package filestore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

type mockS3Object struct {
	data        []byte
	contentType string
	etag        string
	modTime     time.Time
}

type mockS3Server struct {
	mu          sync.Mutex
	buckets     map[string]bool
	objects     map[string]mockS3Object
	rangeHeader []string
	failAll     bool
	server      *httptest.Server
}

func newMockS3Server(t *testing.T, bucket string) *mockS3Server {
	t.Helper()
	m := &mockS3Server{
		buckets: map[string]bool{bucket: true},
		objects: map[string]mockS3Object{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockS3Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	failAll := m.failAll
	m.mu.Unlock()
	if failAll {
		writeS3XMLError(
			w,
			http.StatusInternalServerError,
			"InternalError",
			"simulated S3 outage",
		)
		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	bucket := parts[0]

	m.mu.Lock()
	bucketOK := m.buckets[bucket]
	m.mu.Unlock()
	if !bucketOK {
		writeS3XMLError(
			w,
			http.StatusNotFound,
			"NoSuchBucket",
			"The specified bucket does not exist",
		)
		return
	}

	if len(parts) == 1 || parts[1] == "" {
		if r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><LocationConstraint>us-east-1</LocationConstraint>`))
			return
		}
		if r.Method == http.MethodHead || r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	objKey := bucket + "/" + parts[1]
	switch r.Method {
	case http.MethodPut:
		body, err := readS3PutBody(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sum := md5.Sum(body) //nolint:gosec // S3 ETag simulation only
		etag := hex.EncodeToString(sum[:])
		ct := r.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/octet-stream"
		}
		m.mu.Lock()
		m.objects[objKey] = mockS3Object{
			data:        body,
			contentType: ct,
			etag:        etag,
			modTime:     time.Now().UTC().Truncate(time.Second),
		}
		m.mu.Unlock()
		w.Header().Set("ETag", `"`+etag+`"`)
		w.WriteHeader(http.StatusOK)

	case http.MethodHead:
		m.mu.Lock()
		obj, ok := m.objects[objKey]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
		w.Header().Set("Content-Type", obj.contentType)
		w.Header().Set("ETag", `"`+obj.etag+`"`)
		w.Header().Set("Last-Modified", obj.modTime.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)

	case http.MethodGet:
		m.mu.Lock()
		obj, ok := m.objects[objKey]
		rangeHdr := r.Header.Get("Range")
		if rangeHdr != "" {
			m.rangeHeader = append(m.rangeHeader, rangeHdr)
		}
		m.mu.Unlock()
		if !ok {
			writeS3XMLError(
				w,
				http.StatusNotFound,
				"NoSuchKey",
				"The specified key does not exist",
			)
			return
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", obj.contentType)
		w.Header().Set("ETag", `"`+obj.etag+`"`)
		w.Header().Set("Last-Modified", obj.modTime.Format(http.TimeFormat))
		if rangeHdr == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(obj.data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(obj.data)
			return
		}
		start, end, ok := parseSingleRange(rangeHdr, len(obj.data))
		if !ok {
			writeS3XMLError(
				w,
				http.StatusRequestedRangeNotSatisfiable,
				"InvalidRange",
				"Invalid range",
			)
			return
		}
		slice := obj.data[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf(
			"bytes %d-%d/%d",
			start,
			end,
			len(obj.data),
		))
		w.Header().Set("Content-Length", strconv.Itoa(len(slice)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(slice)

	case http.MethodDelete:
		m.mu.Lock()
		delete(m.objects, objKey)
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func readS3PutBody(r *http.Request) ([]byte, error) {
	shaHeader := r.Header.Get("X-Amz-Content-Sha256")
	if !strings.HasPrefix(shaHeader, "STREAMING-") {
		return io.ReadAll(r.Body)
	}
	br := bufio.NewReader(r.Body)
	var out bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		sizeHex, _, _ := strings.Cut(line, ";")
		n, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return out.Bytes(), nil
		}
		if _, err := io.CopyN(&out, br, n); err != nil {
			return nil, err
		}
		if _, err := br.ReadString('\n'); err != nil {
			return nil, err
		}
	}
}

func parseSingleRange(hdr string, size int) (int, int, bool) {
	if !strings.HasPrefix(hdr, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(hdr, "bytes=")
	startStr, endStr, ok := strings.Cut(spec, "-")
	if !ok || startStr == "" {
		return 0, 0, false
	}
	start, err := strconv.Atoi(startStr)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if endStr != "" {
		parsedEnd, endErr := strconv.Atoi(endStr)
		if endErr != nil || parsedEnd < start {
			return 0, 0, false
		}
		if parsedEnd < end {
			end = parsedEnd
		}
	}
	return start, end, true
}

func writeS3XMLError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(
		w,
		`<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`,
		code,
		msg,
	)
}

func TestS3Driver_PutOpenRangeSeekPresignDelete(t *testing.T) {
	mock := newMockS3Server(t, "zhulong-media")
	driver, err := NewS3Driver(S3Config{
		Endpoint:        mock.server.URL,
		Region:          "us-east-1",
		Bucket:          "zhulong-media",
		Prefix:          "edge-01",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "miniosecret",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewS3Driver: %v", err)
	}

	ctx := context.Background()
	key := "recordings/cam-01/2026-11-22/evt-1.mp4"
	payload := bytes.Repeat([]byte("0123456789abcdef"), 16)
	putErr := driver.Put(
		ctx,
		key,
		bytes.NewReader(payload),
		PutOptions{
			Size:        int64(len(payload)),
			ContentType: "video/mp4",
		},
	)
	if putErr != nil {
		t.Fatalf("Put: %v", putErr)
	}

	info, err := driver.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.SizeBytes != int64(len(payload)) || info.ContentType != "video/mp4" {
		t.Fatalf("unexpected Stat info: %+v", info)
	}

	rc, openInfo, err := driver.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if openInfo.SizeBytes != int64(len(payload)) {
		rc.Close()
		t.Fatalf("unexpected Open info: %+v", openInfo)
	}
	if _, err := rc.Seek(160, io.SeekStart); err != nil {
		rc.Close()
		t.Fatalf("Seek: %v", err)
	}
	buf := make([]byte, 8)
	n, err := io.ReadFull(rc, buf)
	rc.Close()
	if err != nil || n != 8 {
		t.Fatalf("ReadFull after Seek: n=%d err=%v", n, err)
	}
	if string(buf) != "01234567" {
		t.Fatalf("unexpected range bytes: %q", string(buf))
	}

	mock.mu.Lock()
	ranges := append([]string{}, mock.rangeHeader...)
	mock.mu.Unlock()
	if len(ranges) == 0 || !strings.HasPrefix(ranges[0], "bytes=160-") {
		t.Fatalf("expected HTTP Range fetch starting at 160, got %v", ranges)
	}

	presigned, err := driver.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	parsed, err := url.Parse(presigned)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	if !strings.Contains(parsed.Path, "/zhulong-media/edge-01/"+key) {
		t.Fatalf("unexpected presigned path %q", parsed.Path)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("expected X-Amz-Signature in presigned URL %q", presigned)
	}

	if err := driver.ProbeWritable(ctx); err != nil {
		t.Fatalf("ProbeWritable: %v", err)
	}

	if err := driver.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, err := driver.Exists(ctx, key)
	if err != nil || exists {
		t.Fatalf("Exists after Delete = (%v, %v)", exists, err)
	}
	if err := driver.Delete(ctx, key); err != nil {
		t.Fatalf("idempotent Delete failed: %v", err)
	}
}

func TestS3Driver_ErrorClassificationAndHealth(t *testing.T) {
	mock := newMockS3Server(t, "zhulong-media")
	driver, err := NewS3Driver(S3Config{
		Endpoint:        mock.server.URL,
		Region:          "us-east-1",
		Bucket:          "zhulong-media",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "miniosecret",
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatalf("NewS3Driver: %v", err)
	}

	ctx := context.Background()
	health, err := driver.Health(ctx)
	if err != nil || !health.Available || health.Backend != BackendS3 {
		t.Fatalf("Health = (%+v, %v), want Available=true", health, err)
	}

	_, _, err = driver.Open(ctx, "snapshots/cam-01/missing.jpg")
	if got := apperr.CodeOf(err); got != "MEDIA_ENTRY_NOT_FOUND" {
		t.Fatalf("expected MEDIA_ENTRY_NOT_FOUND, got %q (%v)", got, err)
	}

	mock.mu.Lock()
	mock.failAll = true
	mock.mu.Unlock()

	health, err = driver.Health(ctx)
	if err != nil || health.Available {
		t.Fatalf("expected unavailable Health on 5xx, got (%+v, %v)", health, err)
	}
	if strings.Contains(health.Message, "miniosecret") {
		t.Fatalf("health message leaked secret: %q", health.Message)
	}

	_, _, err = driver.Open(ctx, "snapshots/cam-01/missing.jpg")
	if got := apperr.CodeOf(err); got != "MEDIA_ROOT_UNAVAILABLE" {
		t.Fatalf("expected MEDIA_ROOT_UNAVAILABLE on 5xx, got %q (%v)", got, err)
	}
}

func TestS3Driver_RejectsInvalidConfig(t *testing.T) {
	cases := []S3Config{
		{Endpoint: "", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"},
		{Endpoint: "ftp://s3.local", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"},
		{Endpoint: "https://s3.local/extra", Bucket: "b", AccessKeyID: "a", SecretAccessKey: "s"},
		{Endpoint: "https://s3.local", Bucket: "", AccessKeyID: "a", SecretAccessKey: "s"},
		{Endpoint: "https://s3.local", Bucket: "b", AccessKeyID: "", SecretAccessKey: "s"},
	}
	for _, cfg := range cases {
		if _, err := NewS3Driver(cfg); err == nil {
			t.Fatalf("expected NewS3Driver(%+v) to fail", cfg)
		}
	}
}
