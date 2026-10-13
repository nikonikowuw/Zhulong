package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func createUncompressedTestTarGz(t *testing.T, payloadBytes int) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test_uncompressed_*.tar.gz")
	if err != nil {
		t.Fatalf("create temp archive: %v", err)
	}
	defer tmpFile.Close()

	// Use gzip.NoCompression so the archive file size reflects payloadBytes (> 1 MiB)
	gw, err := gzip.NewWriterLevel(tmpFile, gzip.NoCompression)
	if err != nil {
		t.Fatalf("create gzip writer: %v", err)
	}
	tw := tar.NewWriter(gw)

	manifestContent := []byte(`{
		"id": "e2e_large_pkg",
		"version": "1.0.0",
		"name": "e2e_large_pkg",
		"task_type": "detection",
		"category": "safety",
		"platform": {"arch": "x86_64", "target": "cpu"},
		"resources": {"compute_cost": 20, "memory_mb": 64}
	}`)
	files := map[string][]byte{
		"manifest.json": manifestContent,
		"plugin.so":     []byte("elf_stub"),
		"sample.jpg":    []byte("jpg_stub"),
	}

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write tar content %s: %v", name, err)
		}
	}

	if payloadBytes > 0 {
		hdr := &tar.Header{
			Name: "large_payload.bin",
			Mode: 0644,
			Size: int64(payloadBytes),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write large payload tar header: %v", err)
		}
		chunk := make([]byte, 64*1024)
		remaining := payloadBytes
		for remaining > 0 {
			toWrite := len(chunk)
			if remaining < toWrite {
				toWrite = remaining
			}
			if _, err := tw.Write(chunk[:toWrite]); err != nil {
				t.Fatalf("write large payload chunk: %v", err)
			}
			remaining -= toWrite
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	return tmpFile.Name()
}

func buildMultipartRequest(t *testing.T, archivePath string) (*http.Request, string) {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	fi, err := file.Stat()
	if err != nil {
		file.Close()
		t.Fatalf("stat archive: %v", err)
	}

	pipeReader, pipeWriter := io.Pipe()
	mw := multipart.NewWriter(pipeWriter)

	go func() {
		defer file.Close()
		defer pipeWriter.Close()
		defer mw.Close()

		part, err := mw.CreateFormFile("file", filepath.Base(archivePath))
		if err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/install", pipeReader)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Set approximate Content-Length so middleware ContentLength checks can be exercised
	req.ContentLength = fi.Size() + 1024

	return req, mw.FormDataContentType()
}

func TestUploadRouteEndToEndRealRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()

	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("database migration failed: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := dbStore.Close(); closeErr != nil {
			t.Logf("cleanup dbStore close: %v", closeErr)
		}
	})

	comp := algorithm.NewAlgorithmComponents(dbStore, dataDir, "cpu", zap.NewNop())
	comp.Supervisor = algorithm.NewSupervisor(comp.Store, dataDir, "sleep", "", algorithm.DefaultRecoveryOptions(), zap.NewNop())
	comp.Verifier.RunnerPath = "" // bypass dynamic runner check in test

	assets := http.FS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("shell")},
	})

	// 1. Build real router without auth to test upload flow and body limits
	router := newRouter(RouterParams{
		Logger:          zap.NewNop(),
		DatabaseReady:   readinessStub(true),
		NativeReady:     readinessStub(true),
		ProtectedRoutes: []RouteRegistrar{comp.ProtectedRoutes},
		UploadRoutes:    []RouteRegistrar{comp.UploadRoutes},
		Assets:          assets,
	})

	// Create an archive with > 8 MiB payload (8.5 MiB)
	const largeSize = 8500 * 1024
	archivePath := createUncompressedTestTarGz(t, largeSize)
	defer os.Remove(archivePath)

	fi, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if fi.Size() <= 8*1024*1024 {
		t.Fatalf("expected archive size > 8 MiB, got: %d bytes", fi.Size())
	}

	// 1. Upload > 8 MiB archive through REAL router -> must succeed (201 Created), NOT 413
	req, _ := buildMultipartRequest(t, archivePath)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for > 8 MiB upload on real router, got status %d, body: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Fatalf("expected response code OK, got: %s", resp.Code)
	}

	// 2. Upload with limit set lower than payload -> must return 413 FILE_TOO_LARGE
	comp.Handler.SetMaxUploadLimitForTest(1024) // 1 KB limit
	reqOverLimit, _ := buildMultipartRequest(t, archivePath)
	wOverLimit := httptest.NewRecorder()
	router.ServeHTTP(wOverLimit, reqOverLimit)

	if wOverLimit.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Request Entity Too Large when exceeding limit, got %d, body: %s", wOverLimit.Code, wOverLimit.Body.String())
	}

	var respOverLimit httputil.Response
	if err := json.Unmarshal(wOverLimit.Body.Bytes(), &respOverLimit); err != nil {
		t.Fatalf("unmarshal over-limit response: %v", err)
	}
	if respOverLimit.Code != "FILE_TOO_LARGE" {
		t.Fatalf("expected error code FILE_TOO_LARGE, got: %s", respOverLimit.Code)
	}

	// 3. Reset limit and verify authentication guard on upload route
	comp.Handler.SetMaxUploadLimitForTest(algorithm.MaxUploadSize)
	authStub := authStubService{}
	routerWithAuth := newRouter(RouterParams{
		Logger:          zap.NewNop(),
		DatabaseReady:   readinessStub(true),
		NativeReady:     readinessStub(true),
		AuthService:     authStub,
		ProtectedRoutes: []RouteRegistrar{comp.ProtectedRoutes},
		UploadRoutes:    []RouteRegistrar{comp.UploadRoutes},
		Assets:          assets,
	})

	reqUnauth, _ := buildMultipartRequest(t, archivePath)
	wUnauth := httptest.NewRecorder()
	routerWithAuth.ServeHTTP(wUnauth, reqUnauth)

	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for unauthenticated upload, got %d, body: %s", wUnauth.Code, wUnauth.Body.String())
	}

	// 4. Verify standard API route still rejects body > 1 MiB with PAYLOAD_TOO_LARGE
	// Mount auth routes so /api/v1/auth/init exists as a public route
	routerWithAuthRoutes := newRouter(RouterParams{
		Logger:          zap.NewNop(),
		DatabaseReady:   readinessStub(true),
		NativeReady:     readinessStub(true),
		AuthService:     authStub,
		PublicRoutes:    []RouteRegistrar{auth.NewHandler(authStub)},
		ProtectedRoutes: []RouteRegistrar{comp.ProtectedRoutes},
		UploadRoutes:    []RouteRegistrar{comp.UploadRoutes},
		Assets:          assets,
	})

	oversizedJSON := fmt.Sprintf(`{"padding":"%s"}`, string(make([]byte, 2*1024*1024)))
	reqJSON := httptest.NewRequest(http.MethodPost, "/api/v1/auth/init", bytes.NewReader([]byte(oversizedJSON)))
	reqJSON.Header.Set("Content-Type", "application/json")
	wJSON := httptest.NewRecorder()
	routerWithAuthRoutes.ServeHTTP(wJSON, reqJSON)

	if wJSON.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized API request, got %d", wJSON.Code)
	}
	var respJSON httputil.Response
	if err := json.Unmarshal(wJSON.Body.Bytes(), &respJSON); err == nil {
		if respJSON.Code != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("expected code PAYLOAD_TOO_LARGE on standard route, got %s", respJSON.Code)
		}
	}
}
