package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// execTimeout bounds each external converter run. A malformed or crafted PDF
// can keep ghostscript or pdftoppm busy indefinitely, which would pin a worker
// and the CPU limit for as long as the client cares to wait.
const execTimeout = 90 * time.Second

// pdfMagic is the header every PDF must start with. Checking it keeps the
// external tools from being handed arbitrary attacker-controlled content.
var pdfMagic = []byte("%PDF-")

func createTempDir() (string, error) {
	return os.MkdirTemp("", "pdfhandler-*")
}

// saveUploadedPDF writes an uploaded file to dest and rejects it unless it
// really looks like a PDF.
func saveUploadedPDF(c *gin.Context, file *multipart.FileHeader, dest string) error {
	if file.Size == 0 {
		return errors.New("uploaded file is empty")
	}
	if err := c.SaveUploadedFile(file, dest); err != nil {
		return fmt.Errorf("saving upload: %w", err)
	}
	return verifyPDF(dest)
}

func verifyPDF(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening saved upload: %w", err)
	}
	defer f.Close()

	head := make([]byte, len(pdfMagic))
	if _, err := io.ReadFull(f, head); err != nil {
		return errors.New("file is not a valid PDF")
	}
	if !bytes.Equal(head, pdfMagic) {
		return errors.New("file is not a valid PDF")
	}
	return nil
}

// allowedImageExts are the raster formats pdfcpu can import. Anything else is
// refused rather than guessed at.
var allowedImageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".tif":  true,
	".tiff": true,
	".webp": true,
}

// extPattern matches a plain extension and nothing else.
var extPattern = regexp.MustCompile(`^\.[A-Za-z0-9]{1,5}$`)

// safeImageExt derives a filesystem-safe extension from an untrusted upload
// name. filepath.Ext returns everything after the final dot, which for a name
// such as "x.a/../../etc/cron.d/evil" still contains path separators — joining
// that onto a temp dir would write outside of it. Base + pattern + allow-list
// close that off.
func safeImageExt(filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	if !extPattern.MatchString(ext) || !allowedImageExts[ext] {
		return "", fmt.Errorf("unsupported image type %q", ext)
	}
	return ext, nil
}

// runTool executes an external converter under both a hard timeout and the
// request context, so a disconnecting client also releases the work. The
// combined output is returned for logging only — see failInternal.
func runTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%s timed out after %s", name, execTimeout)
	}
	if err != nil {
		return out, fmt.Errorf("%s failed: %w", name, err)
	}
	return out, nil
}

// failInternal logs the real cause and answers with a fixed message. Tool
// output and pdfcpu errors embed temp paths and fragments of the uploaded
// document, so they are deliberately never echoed back to the caller.
func failInternal(c *gin.Context, publicMsg string, cause error) {
	log.Printf("%s %s: %v", c.Request.Method, c.Request.URL.Path, cause)
	c.JSON(http.StatusInternalServerError, gin.H{"error": publicMsg})
}

// logErr folds an external tool's combined output into the error that goes to
// the log, so the detail is kept somewhere it cannot reach the client.
func logErr(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return fmt.Errorf("%w (output: %s)", err, strings.TrimSpace(string(out)))
}

func failBadRequest(c *gin.Context, publicMsg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": publicMsg})
}

// failUpload answers an upload that could not be read. An oversize body is the
// common case and gets a 413 saying so, since the generic "file required" that
// the parse failure otherwise produces sends people looking in the wrong place.
func failUpload(c *gin.Context, err error, publicMsg string) {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("upload too large: the limit is %d MB", maxBytes.Limit>>20),
		})
		return
	}
	failBadRequest(c, publicMsg)
}

// zipDir zips all files inside srcDir into a single destFile zip archive.
func zipDir(srcDir, destFile string) error {
	zf, err := os.Create(destFile)
	if err != nil {
		return err
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)
	defer zw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}
