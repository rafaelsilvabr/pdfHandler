package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func TestHealthz(t *testing.T) {
	r, err := newRouter()
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", rec.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	if got["status"] != "ok" {
		t.Errorf(`status = %q, want "ok"`, got["status"])
	}
}

// UI assets are local. Nothing should be loadable from another origin,
// and the page must not be framable.
func TestSecurityHeaders(t *testing.T) {
	r, err := newRouter()
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy is missing")
	}
	for _, directive := range []string{
		"default-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP is missing %q: %s", directive, csp)
		}
	}
}

// Uploaded documents are private; a POST response must not be cached by the
// browser or an intermediate proxy.
func TestPostResponsesAreNoStore(t *testing.T) {
	r, err := newRouter()
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/merge", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control on a POST = %q, want %q", got, "no-store")
	}
}

// bodyLimit is the only thing standing between an upload and the container's
// disk: MaxMultipartMemory just decides how much is held in RAM first.
func TestBodyLimitTruncatesOversizeRequests(t *testing.T) {
	const limit = 64

	r := gin.New()
	r.Use(bodyLimit(limit))

	var readErr error
	r.POST("/x", func(c *gin.Context) {
		_, readErr = io.ReadAll(c.Request.Body)
		c.Status(http.StatusOK)
	})

	t.Run("over the limit", func(t *testing.T) {
		readErr = nil
		body := bytes.Repeat([]byte("a"), limit*4)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body)))

		if readErr == nil {
			t.Fatalf("reading a %d-byte body under a %d-byte limit succeeded", len(body), limit)
		}
		var maxBytes *http.MaxBytesError
		if !errors.As(readErr, &maxBytes) {
			t.Fatalf("got %v, want a *http.MaxBytesError so handlers can answer 413", readErr)
		}
	})

	t.Run("under the limit", func(t *testing.T) {
		readErr = nil
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("small")))

		if readErr != nil {
			t.Fatalf("a body under the limit failed to read: %v", readErr)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}

func TestUploadConfigMatchesDeployment(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{{"", 100 << 20}, {"250", 250 << 20}} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("MAX_UPLOAD_MB", tc.value)
			r, err := newRouter()
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
			var config struct {
				MaxUploadBytes int64 `json:"maxUploadBytes"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &config); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusOK || config.MaxUploadBytes != tc.want {
				t.Fatalf("config response = %d %s; want limit %d", rec.Code, rec.Body.String(), tc.want)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("upload settings must not be cached across deployments")
			}
		})
	}
}

func TestConfiguredUploadLimitAcrossHandlers(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "test.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("degrees", "45"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	post := func(r http.Handler, endpoint string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
		req.Header.Set("Content-Type", w.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	t.Setenv("MAX_UPLOAD_MB", "1")
	r, err := newRouter()
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"merge", "split", "compress", "rotate", "pdf-to-image", "image-to-pdf", "protect", "unlock", "preview"} {
		t.Run(operation, func(t *testing.T) {
			rec := post(r, "/api/"+operation)
			if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "limit is 1 MB") {
				t.Fatalf("oversize upload = %d %s; want 413 with configured limit", rec.Code, rec.Body.String())
			}
		})
	}

	// The same upload fits when an admin raises the limit. The handler's own
	// degrees validation proves multipart parsing succeeded before PDF processing.
	t.Setenv("MAX_UPLOAD_MB", "2")
	r, err = newRouter()
	if err != nil {
		t.Fatal(err)
	}
	rec := post(r, "/api/rotate")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "degrees must") {
		t.Fatalf("upload under raised limit = %d %s; want degrees validation", rec.Code, rec.Body.String())
	}
}

// Static assets are served from disk relative to the working directory, so this
// also catches a container that was built without ./static.
func TestServesUI(t *testing.T) {
	r, err := newRouter()
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}

	for _, tc := range []struct {
		path        string
		contentType string
		contains    []string
	}{
		{"/", "text/html", []string{"<html", `/static/styles.css`, `/static/app.js`}},
		{"/about", "text/html", []string{`id="about-view"`, `id="about-title"`, `/static/app.js`}},
		{"/static/styles.css", "text/css", nil},
		{"/static/app.js", "javascript", nil},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tc.path, rec.Code)
			}
			if !strings.Contains(rec.Header().Get("Content-Type"), tc.contentType) {
				t.Errorf("GET %s content type = %q, want %s", tc.path, rec.Header().Get("Content-Type"), tc.contentType)
			}
			if rec.Body.Len() == 0 {
				t.Errorf("GET %s returned an empty asset", tc.path)
			}
			if rec.Header().Get("Cache-Control") != "no-cache" {
				t.Errorf("GET %s must revalidate assets after deployment", tc.path)
			}
			for _, want := range tc.contains {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("GET %s is missing %q", tc.path, want)
				}
			}
		})
	}
}
