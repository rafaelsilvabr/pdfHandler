package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"pdfhandler/handlers"

	"github.com/gin-gonic/gin"
)

// newRouter wires the routes and middleware. Kept separate from main so tests
// can drive the real router — middleware included — through httptest.
func newRouter() (*gin.Engine, error) {
	maxUploadBytes, err := uploadLimitFromEnv()
	if err != nil {
		return nil, err
	}
	r := gin.Default()

	// Bounds what a multipart form buffers in RAM. The hard cap on the request
	// itself is bodyLimit below — this only decides where the bytes land.
	r.MaxMultipartMemory = 8 << 20 // 8 MB

	// Gin trusts every proxy by default, which lets any client forge the client
	// IP through X-Forwarded-For. Behind the nginx ingress the real address is
	// the ingress pod's, so trust nothing and read RemoteAddr.
	if err := r.SetTrustedProxies(nil); err != nil {
		return nil, err
	}

	r.Use(securityHeaders(), bodyLimit(maxUploadBytes))

	r.Static("/static", "./static")
	r.StaticFile("/", "./static/index.html")
	r.StaticFile("/about", "./static/index.html")

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	{
		api.GET("/config", func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			c.JSON(http.StatusOK, gin.H{"maxUploadBytes": maxUploadBytes})
		})
		api.POST("/merge", handlers.Merge)
		api.POST("/split", handlers.Split)
		api.POST("/compress", handlers.Compress)
		api.POST("/rotate", handlers.Rotate)
		api.POST("/pdf-to-image", handlers.PDFToImage)
		api.POST("/image-to-pdf", handlers.ImageToPDF)
		api.POST("/protect", handlers.Protect)
		api.POST("/unlock", handlers.Unlock)
		api.POST("/preview", handlers.Preview)
	}

	return r, nil
}

func main() {
	r, err := newRouter()
	if err != nil {
		log.Fatal(err)
	}

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	// Explicit timeouts: the zero-value http.Server Gin's Run() builds has none,
	// so a slow client can hold a connection open indefinitely. WriteTimeout is
	// generous because compressing a large PDF is genuinely slow.
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	log.Printf("PDF Handler running on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// bodyLimit caps the whole request body. Without it an upload is bounded only
// by disk, since anything past MaxMultipartMemory is spooled to a temp file.
func bodyLimit(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		c.Next()
	}
}

// securityHeaders applies the baseline response headers. The UI loads local
// assets, uses inline styles for panel visibility, and renders base64 previews
// and blob-backed image uploads.
func securityHeaders() gin.HandlerFunc {
	const csp = "default-src 'self'; " +
		"img-src 'self' data: blob:; " +
		"style-src 'self' 'unsafe-inline'; " +
		"script-src 'self' 'unsafe-inline'; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"

	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// Uploaded documents are private: never let a proxy or the browser keep a copy.
		if c.Request.Method == http.MethodPost {
			h.Set("Cache-Control", "no-store")
		} else {
			// Revalidate UI assets after deployment so clients receive the code
			// that understands the active server configuration.
			h.Set("Cache-Control", "no-cache")
		}
		c.Next()
	}
}
