package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSafeImageExt(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
		wantErr  bool
	}{
		{"plain jpg", "holiday.jpg", ".jpg", false},
		{"uppercase is normalised", "SCAN.PNG", ".png", false},
		{"tiff", "fax.tiff", ".tiff", false},
		{"webp", "sticker.webp", ".webp", false},
		{"dotted name keeps last ext", "my.holiday.photo.png", ".png", false},

		// filepath.Ext returns everything after the final dot, so on these the
		// dot falls before a separator and the "extension" carries the rest of
		// the path with it. Joining that onto the temp dir would write outside
		// it — Base strips the directory first, then the pattern rejects what
		// is left because it is not a plain extension.
		{"traversal via ext", "x.a/../../../../tmp/pwned", "", true},
		{"absolute path in ext", "x./etc/cron.d/job", "", true},
		{"null byte", "x.pn\x00g", "", true},

		// Base has already discarded the directory part here, so what remains
		// is an ordinary filename with a legitimate extension. The handler
		// names the file itself and only borrows the extension, so accepting
		// this is safe — TestImageToPDFIgnoresFilenamePath proves it.
		{"traversal stripped by Base", "x.a/../../evil.png", ".png", false},
		{"leading path stripped by Base", "../../../../etc/photo.jpg", ".jpg", false},

		{"not an image type", "payload.svg", "", true},
		{"executable", "run.sh", "", true},
		{"no extension", "README", "", true},
		{"dot only", "trailing.", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeImageExt(tt.filename)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("safeImageExt(%q) = %q, want error", tt.filename, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("safeImageExt(%q) returned unexpected error: %v", tt.filename, err)
			}
			if got != tt.want {
				t.Errorf("safeImageExt(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestVerifyPDF(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		return p
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"real pdf", write("good.pdf", pdfBytes(t, 1)), false},
		{"plain text", write("text.pdf", []byte("this is not a PDF at all")), true},
		{"empty", write("empty.pdf", nil), true},
		{"shorter than the magic", write("short.pdf", []byte("%PD")), true},
		{"magic not at the start", write("late.pdf", []byte("xx%PDF-1.7")), true},
		{"html disguised", write("page.pdf", []byte("<html><body>hi</body></html>")), true},
		{"missing file", filepath.Join(dir, "nope.pdf"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyPDF(tt.path)
			if tt.wantErr && err == nil {
				t.Errorf("verifyPDF(%s) = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("verifyPDF(%s) = %v, want nil", tt.name, err)
			}
		})
	}
}

// Every endpoint that accepts a PDF must refuse content that is not one, before
// it reaches ghostscript, poppler or pdfcpu.
func TestRejectsNonPDFUpload(t *testing.T) {
	r := testRouter()
	junk := []byte("GIF89a not a pdf")

	endpoints := []string{
		"/api/split", "/api/compress", "/api/rotate",
		"/api/pdf-to-image", "/api/protect", "/api/unlock", "/api/preview",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			rec := post(t, r, ep,
				[]upload{{field: "file", filename: "evil.pdf", data: junk}},
				map[string]string{"password": "hunter2"},
			)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s with non-PDF content = %d, want %d", ep, rec.Code, http.StatusBadRequest)
			}
		})
	}

	t.Run("/api/merge", func(t *testing.T) {
		rec := post(t, r, "/api/merge", []upload{
			{field: "files[]", filename: "a.pdf", data: pdfBytes(t, 1)},
			{field: "files[]", filename: "b.pdf", data: junk},
		}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("merge with one non-PDF = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

func TestRotateRejectsBadParameters(t *testing.T) {
	r := testRouter()
	pdf := pdfBytes(t, 3)

	tests := []struct {
		name   string
		fields map[string]string
		want   int
	}{
		{"shell metacharacters in pages", map[string]string{"pages": "1;rm -rf /"}, http.StatusBadRequest},
		{"command substitution", map[string]string{"pages": "1,$(id)"}, http.StatusBadRequest},
		{"path traversal in pages", map[string]string{"pages": "../../etc/passwd"}, http.StatusBadRequest},
		{"pdfcpu keyword", map[string]string{"pages": "even"}, http.StatusBadRequest},
		{"negative page", map[string]string{"pages": "-3"}, http.StatusBadRequest},
		{"degrees not a quarter turn", map[string]string{"degrees": "45"}, http.StatusBadRequest},
		{"degrees zero", map[string]string{"degrees": "0"}, http.StatusBadRequest},
		{"degrees not a number", map[string]string{"degrees": "ninety"}, http.StatusBadRequest},

		{"single page", map[string]string{"pages": "2"}, http.StatusOK},
		{"page list", map[string]string{"pages": "1,3"}, http.StatusOK},
		{"page range", map[string]string{"pages": "1-3"}, http.StatusOK},
		{"all pages", map[string]string{"pages": "all"}, http.StatusOK},
		{"negative quarter turn", map[string]string{"degrees": "-90"}, http.StatusOK},
		{"180", map[string]string{"degrees": "180"}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, r, "/api/rotate",
				[]upload{{field: "file", filename: "in.pdf", data: pdf}}, tt.fields)
			if rec.Code != tt.want {
				t.Errorf("rotate %v = %d, want %d (body: %s)",
					tt.fields, rec.Code, tt.want, strings.TrimSpace(rec.Body.String()))
			}
		})
	}
}

func TestSplitRejectsBadSpan(t *testing.T) {
	r := testRouter()
	pdf := pdfBytes(t, 2)

	for _, span := range []string{"-5", "0", "abc", "99999999", "1e3"} {
		t.Run("span="+span, func(t *testing.T) {
			rec := post(t, r, "/api/split",
				[]upload{{field: "file", filename: "in.pdf", data: pdf}},
				map[string]string{"span": span})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("split span=%q = %d, want %d", span, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestProtectRequiresUsablePassword(t *testing.T) {
	r := testRouter()
	pdf := pdfBytes(t, 1)

	for _, ep := range []string{"/api/protect", "/api/unlock"} {
		t.Run(ep+" empty", func(t *testing.T) {
			rec := post(t, r, ep,
				[]upload{{field: "file", filename: "in.pdf", data: pdf}},
				map[string]string{"password": ""})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s with empty password = %d, want %d", ep, rec.Code, http.StatusBadRequest)
			}
		})
		t.Run(ep+" oversized", func(t *testing.T) {
			rec := post(t, r, ep,
				[]upload{{field: "file", filename: "in.pdf", data: pdf}},
				map[string]string{"password": strings.Repeat("a", maxPasswordLen+1)})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s with %d-char password = %d, want %d",
					ep, maxPasswordLen+1, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestMergeRequiresAtLeastTwoFiles(t *testing.T) {
	r := testRouter()
	rec := post(t, r, "/api/merge",
		[]upload{{field: "files[]", filename: "only.pdf", data: pdfBytes(t, 1)}}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("merge with one file = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// Filenames whose extension is not an allow-listed image type are refused
// outright. The traversal case matters because filepath.Ext on its own returns
// everything after the final dot, separators included.
func TestImageToPDFRejectsBadExtensions(t *testing.T) {
	r := testRouter()

	hostile := []string{
		"x.a/../../../../tmp/pwned", // the "extension" is a whole path
		"x./etc/cron.d/job",
		"payload.sh",
		"vector.svg",
		"noextension",
	}

	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			rec := post(t, r, "/api/image-to-pdf",
				[]upload{{field: "files[]", filename: name, data: pngBytes(t)}}, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("image-to-pdf with filename %q = %d, want %d",
					name, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// A filename that ends in a real image extension is accepted even when it is
// dressed up as a path, because the handler names the temp file itself and
// takes nothing from the client but the extension. This is the test that would
// fail if that ever changed: the canary path must stay empty.
func TestImageToPDFIgnoresFilenamePath(t *testing.T) {
	r := testRouter()

	canaryDir := t.TempDir()
	canary := filepath.Join(canaryDir, "pwned.png")

	// e.g. "../../../../tmp/TestXxx/001/pwned.png" — resolves to the canary if
	// the handler ever joins the client's filename onto its temp dir.
	name := "../../../../" + strings.TrimPrefix(canary, "/")

	rec := post(t, r, "/api/image-to-pdf",
		[]upload{{field: "files[]", filename: name, data: pngBytes(t)}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("image-to-pdf with filename %q = %d, want 200 (body: %s)",
			name, rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(canary); err == nil {
		t.Fatalf("the upload was written to %s — the client's filename reached the path", canary)
	}
}

// Ghostscript output and pdfcpu errors quote temp paths and fragments of the
// uploaded document; they belong in the log, not in the response.
func TestErrorResponsesDoNotLeakInternals(t *testing.T) {
	r := testRouter()
	pdf := pdfBytes(t, 1)

	// Encrypt, then try to unlock with the wrong password — the pdfcpu error
	// behind this one is the most detailed in the codebase.
	enc := post(t, r, "/api/protect",
		[]upload{{field: "file", filename: "in.pdf", data: pdf}},
		map[string]string{"password": "correct-horse"})
	if enc.Code != http.StatusOK {
		t.Fatalf("protect = %d, want 200", enc.Code)
	}

	rec := post(t, r, "/api/unlock",
		[]upload{{field: "file", filename: "in.pdf", data: enc.Body.Bytes()}},
		map[string]string{"password": "wrong"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unlock with wrong password = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	body := rec.Body.String()
	for _, leak := range []string{"/tmp", "pdfhandler-", "input.pdf", os.TempDir()} {
		if strings.Contains(body, leak) {
			t.Errorf("error response leaks %q: %s", leak, body)
		}
	}
}

func TestFailUploadMapsOversizeTo413(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/compress", nil)

	failUpload(c, &http.MaxBytesError{Limit: 25 << 20}, "PDF file required")

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("failUpload with MaxBytesError = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if !strings.Contains(rec.Body.String(), "upload too large: the limit is 25 MB") {
		t.Errorf("413 body should include the actual limit, got: %s", rec.Body.String())
	}
}

func TestFailUploadPassesThroughOtherErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/compress", nil)

	failUpload(c, http.ErrMissingFile, "PDF file required")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("failUpload with a non-size error = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// zipDir is how split and pdf-to-image return multiple files; entries must be
// relative to the source dir, not absolute paths from the host.
func TestZipDirUsesRelativeNames(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	dest := filepath.Join(t.TempDir(), "out.zip")
	if err := zipDir(src, dest); err != nil {
		t.Fatalf("zipDir: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("opening zip: %v", err)
	}

	if len(zr.File) != 2 {
		t.Fatalf("zip has %d entries, want 2", len(zr.File))
	}
	for _, f := range zr.File {
		if filepath.IsAbs(f.Name) || strings.Contains(f.Name, "..") {
			t.Errorf("zip entry %q should be a plain relative name", f.Name)
		}
	}
}

// decodeJSON is a small helper for the endpoints that answer with JSON.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
	}
}
