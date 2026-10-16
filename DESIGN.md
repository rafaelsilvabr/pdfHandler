# PDF Handler - Design Document

## 1. Overview

Self-hosted PDF manipulation service running on a Kubernetes homelab.
Inspired by ilovepdf.com, but with full data privacy (files never leave your infrastructure).

---

## 2. Goals

- Expose the most useful PDF operations via a clean web UI
- Run efficiently in a homelab K8s cluster (single node or small cluster)
- Keep the stack simple and maintainable long-term
- No external dependencies at runtime (no cloud APIs, no SaaS)
- Files are ephemeral — never persisted to disk beyond the request lifecycle

---

## 3. Non-Goals (v1)

- User authentication / multi-tenancy
- Persistent storage or job history
- OCR (too heavy for homelab resources)
- PDF editing (form fill, annotation) — complexity vs. value tradeoff
- Async/queue-based processing — overkill for homelab traffic

---

## 4. Language Choice: Go

### Decision
Use Go instead of Python for the backend.

### Tradeoffs

| Concern | Go | Python |
|---|---|---|
| Memory footprint | Low (~10-20 MB idle) | Medium (~50-80 MB idle with FastAPI) |
| Startup time | Fast (<100ms) | Slower (500ms+ for Python + libraries) |
| Concurrency | Native goroutines, no GIL | GIL limits true parallelism |
| PDF ecosystem | Limited (pdfcpu, wrappers) | Excellent (pypdf, pikepdf, pdfminer) |
| Binary distribution | Single static binary | Requires runtime + dependencies |
| Dev speed | Slower than Python | Faster |

### Why Go wins here
Most heavy PDF lifting is done by native binaries (ghostscript, poppler).
Go just orchestrates them. In that role, Go's low memory and fast startup matter more
than Python's richer PDF ecosystem. For a homelab with tight resources, this is the right tradeoff.

---

## 5. Framework Choice: Gin

### Decision
Use [Gin](https://github.com/gin-gonic/gin) as the HTTP framework.

### Alternatives considered

| Framework | Notes |
|---|---|
| `net/http` (stdlib) | No multipart helpers, more boilerplate for file uploads |
| **Gin** | Multipart/form-data helpers built-in, well-documented, stable |
| Echo | Similar to Gin, slightly less community adoption |
| Fiber | Fast but uses fasthttp (not net/http compatible) |

### Why Gin
Built-in `c.SaveUploadedFile()`, `c.FileAttachment()`, and `c.MultipartForm()` make
file upload/download handling trivial. Minimal overhead compared to stdlib.

---

## 6. PDF Processing Libraries

### Decision
Use `pdfcpu` as the primary PDF library, with ghostscript and poppler-utils as subprocess tools for specific operations.

### Library breakdown

| Operation | Tool | Rationale |
|---|---|---|
| Merge | `pdfcpu` | Pure Go, no subprocess needed |
| Split | `pdfcpu` | Pure Go, no subprocess needed |
| Rotate | `pdfcpu` | Pure Go, no subprocess needed |
| Protect (encrypt) | `pdfcpu` | Pure Go, AES-256 encryption |
| Unlock (decrypt) | `pdfcpu` | Pure Go |
| Image to PDF | `pdfcpu` | Pure Go image import |
| Compress | `ghostscript` | Better compression ratios than pdfcpu's optimize |
| PDF to Image | `poppler-utils` (pdftoppm) | Best-in-class PDF rendering quality |

### Why not pure Go for everything

`pdfcpu`'s `OptimizeFile` does structural compression but does not re-encode
image streams. Ghostscript re-encodes embedded images (JPEG, downsampling),
which yields 50-80% size reduction on typical PDFs. The subprocess cost is
acceptable since compress is expected to be infrequent.

`pdftoppm` (poppler) produces higher-fidelity image output than any pure-Go
PDF renderer. Given that poppler is already in the container for ghostscript
dependencies, there is no extra cost to including it.

### pdfcpu version
Target `v0.7.0+`. This version uses `github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model`
for the `Configuration` struct (used in protect/unlock). Earlier versions used
`github.com/pdfcpu/pdfcpu/pkg/pdfcpu` directly — if a version mismatch occurs,
this is the first place to check.

---

## 7. Frontend Choice: Single-page HTML (no framework)

### Decision
Serve a single-page workspace from Go, with local HTML, CSS and JavaScript files.
No React, no Node.js build step, no separate frontend container.

### Tradeoffs

| Approach | Pros | Cons |
|---|---|---|
| **Vanilla HTML/JS** | Zero build step, one container, simple deploy | Less component reuse, manual DOM manipulation |
| React (separate container) | Component model, better scalability | Extra container, npm build pipeline, more K8s complexity |
| HTMX | Server-driven UI, minimal JS | Less familiar, adds template engine |

### Why vanilla HTML
This is a homelab tool, not a product. The UI has 8 discrete operations with
simple forms. Vanilla JS is sufficient and eliminates a full frontend build
pipeline and a second container. Less moving parts = fewer failure points in K8s.

### Visual direction

A warm cream workspace uses compact left navigation and a grouped toolkit with
switchable list and block layouts. A narrow guide column provides context alongside the toolkit;
tool workspaces split document previews from options and processing. On smaller
screens, the columns stack and navigation moves behind a menu.

Design tokens live in `static/styles.css`: International Klein Blue (`#002FA7`)
is the only accent, borders are 1px, and controls use small, consistent corner
radii. SF Pro is selected locally when available, with native system fallbacks.
There are no remote fonts, icon libraries, gradients or drop shadows. Hover,
focus and active states are explicit, and reduced-motion preferences are honored.

The catalog supports search and category filters in both layouts. The desktop
sidebar can be hidden from the top bar; sidebar and layout preferences are stored
locally. An About page at `/about` uses the same shell and supports direct links,
reloads, and browser history. Uploads and selectable previews
work with a keyboard. Pending preview requests are canceled when files or tools
change, and download and image object URLs are released when no longer needed.

---

## 8. File Handling Strategy

### Decision
Process files entirely in-memory using OS temp directories. Never persist files beyond the HTTP request.

### Flow
```
Request in → save to /tmp/pdfhandler-* → process → stream response → delete temp dir
```

### Tradeoffs

| Strategy | Pros | Cons |
|---|---|---|
| **Temp dir + defer cleanup** | Simple, no persistence risk, GDPR-friendly | Large files may stress /tmp (RAM-backed on some systems) |
| Persistent storage (PVC) | Handles large files, enables job history | Data privacy risk, more K8s config, out of v1 scope |
| In-memory (bytes.Buffer) | No disk I/O | pdfcpu and ghostscript require file paths, not streams |

### Why temp dirs
pdfcpu and ghostscript both require file paths as input/output — they cannot
operate on `io.Reader`/`io.Writer` streams. Temp dirs are the minimum viable
approach that satisfies their API requirements.

Cleanup is always deferred at the start of each handler, so files are removed
even if the handler returns an error early.

### Max file size
Capped at 100 MiB per request by default via `http.MaxBytesReader`.
`MAX_UPLOAD_MB` selects a positive whole number of MiB at startup; invalid values
fail startup. `MaxMultipartMemory` remains an independent 8 MiB buffering
threshold. The total request cap includes all files and multipart overhead.
`GET /api/config` exposes the active byte limit to the UI, including its About
page and file selection checks. Reverse proxy limits must be adjusted separately
when increasing the application limit.

---

## 9. API Design

### Convention
All endpoints are `POST /api/<operation>`.
Files are uploaded via `multipart/form-data`.
Successful responses stream the result file directly (binary body).
Errors return `{"error": "..."}` JSON with an appropriate HTTP status code.

### Why streaming instead of returning a download URL
A download URL approach would require persistent storage (PVC or object store)
and a cleanup job. Streaming is simpler and keeps files entirely in-memory.

### Endpoints

| Endpoint | Form fields | Returns |
|---|---|---|
| `POST /api/merge` | `files[]` (2+ PDFs) | `merged.pdf` |
| `POST /api/split` | `file` (1 PDF), `span` (int, pages per chunk) | `split.zip` |
| `POST /api/compress` | `file` (1 PDF), `quality` (screen/ebook/printer/prepress) | `compressed.pdf` |
| `POST /api/rotate` | `file` (1 PDF), `degrees` (90/180/270), `pages` (all or "1,3,5") | `rotated.pdf` |
| `POST /api/pdf-to-image` | `file` (1 PDF), `format` (png/jpg), `dpi` (72/150/300) | `images.zip` |
| `POST /api/image-to-pdf` | `files[]` (1+ images) | `output.pdf` |
| `POST /api/protect` | `file` (1 PDF), `password` | `protected.pdf` |
| `POST /api/unlock` | `file` (1 PDF), `password` | `unlocked.pdf` |
| `POST /api/preview` | `file` (1 PDF), `limit` (int, max 50) | JSON: page count + base64 thumbnails |
| `GET /healthz` | — | JSON: `{"status":"ok"}` (liveness/readiness probe) |

### Input handling

Every endpoint takes an untrusted file and hands it to ghostscript, poppler or
pdfcpu, so the request is narrowed before any of that runs:

- **Type check** — uploads must start with the `%PDF-` magic bytes; image
  uploads must carry an allow-listed extension (JPG/PNG/TIFF/WebP). The
  extension is taken from `filepath.Base` and pattern-matched, because
  `filepath.Ext` on a crafted filename can still contain path separators.
- **Parameter validation** — `degrees` must be a non-zero multiple of 90,
  `pages` must be numbers or ranges, `span`/`limit` must be positive and
  bounded, `quality`/`format`/`dpi` are allow-listed. Rejected, not coerced.
- **Size** — the whole request body is capped at 100 MiB by default (configurable
  using `MAX_UPLOAD_MB`) via
  `http.MaxBytesReader`; `MaxMultipartMemory` alone only decides how much is
  buffered in RAM before the rest spills to disk. Oversize uploads get a 413.
- **Time** — every ghostscript/poppler run is bounded by a 90 s timeout tied to
  the request context, so a crafted PDF cannot pin a worker indefinitely.
- **Error responses** — tool output and pdfcpu errors go to the log only. They
  embed temp paths and fragments of the uploaded document, so responses carry a
  fixed message instead.

---

## 10. Kubernetes Design

### Namespace
`pdfhandler-ns` — isolated from other homelab workloads.

### Single deployment
Backend and frontend are served from the same Go binary and the same container.
No need for a second deployment or a reverse proxy between them.

### Service: NodePort
Type `NodePort` on port `30880` to allow direct `hostIP:30880` access
without requiring Ingress to be configured or DNS to be set up.
This is the primary access method for homelab convenience.

### Ingress: nginx
`pdf.home.lab` hostname via nginx-ingress for clean URL access.
Requires local DNS entry or `/etc/hosts` to point `pdf.home.lab` to the cluster IP.
`nginx.ingress.kubernetes.io/proxy-body-size: "100m"` matches the default
`MAX_UPLOAD_MB=100`; update both settings when changing the limit (nginx's
default is 1 MB).

### Tradeoff: NodePort vs LoadBalancer
`LoadBalancer` type requires a cloud provider or MetalLB in homelab.
`NodePort` works everywhere with zero extra setup — preferred for portability.

### Resource requests/limits

| Resource | Request | Limit |
|---|---|---|
| CPU | 250m | 1000m |
| Memory | 128Mi | 512Mi |

These are conservative. Ghostscript can spike CPU during compression —
the 1 CPU limit prevents it from starving other workloads. Memory limit
of 512 Mi accounts for large PDFs held in temp dirs.

### Image pull policy
`IfNotPresent` — assumes the image is built and loaded into the cluster locally
(e.g., `docker build` + `kind load` or `k3s ctr images import`).

---

## 11. Container Design

### Multi-stage Dockerfile

```
Stage 1 (golang:1.21-alpine) — compile the Go binary
Stage 2 (debian:bookworm-slim) — runtime with ghostscript + poppler-utils
```

### Why debian instead of alpine for the runtime stage
Alpine uses musl libc. Ghostscript and poppler have known compatibility issues
with musl in edge cases. Debian slim avoids these issues with minimal extra size.

### Why not distroless
Distroless images do not include a shell or system package manager.
Ghostscript and poppler-utils must be installed at build time, which is
straightforward in a Debian-based image. Distroless would require a custom
base image or a more complex multi-stage setup — not worth it for a homelab tool.

---

## 12. Project Structure

```
pdfHandler/
├── main.go                  # Gin server, middleware, route registration
├── main_test.go             # Middleware: headers, body limit, healthz, UI
├── go.mod
├── go.sum
├── handlers/
│   ├── util.go              # Validation, sandboxing, error and zip helpers
│   ├── merge.go
│   ├── split.go
│   ├── compress.go
│   ├── rotate.go
│   ├── pdf_to_image.go
│   ├── image_to_pdf.go
│   ├── preview.go
│   ├── protect.go           # Covers both protect and unlock
│   ├── testutil_test.go     # Fixture builders and the multipart request helper
│   ├── validation_test.go   # Rejection rules and error-leak checks
│   └── operations_test.go   # Happy paths for each operation
├── scripts/
│   └── smoke.sh             # Functional test against a running instance
├── static/
│   ├── index.html           # Workspace markup and option panels
│   ├── styles.css           # Tokens, responsive layouts and interaction states
│   └── app.js               # Navigation, tool catalog and document workflows
├── .github/workflows/
│   └── ci.yml               # Build/test, govulncheck, container smoke test
├── Dockerfile
├── DESIGN.md                # This file
└── k8s/
    ├── namespace.yaml
    ├── deployment.yaml
    ├── service.yaml          # NodePort 30880
    └── ingress.yaml          # pdf.home.lab via nginx
```

### Testing strategy

Two layers, split by what each can actually catch.

The Go tests drive the handlers through `httptest`, which makes the validation
and rejection rules cheap to assert exhaustively — every bad `pages` value, every
hostile filename, every non-PDF upload. Fixtures are real PDFs built through
pdfcpu at test time rather than checked-in blobs, so they exercise the same
parser the handlers use and there is nothing binary to review in a diff.

What that cannot reach is the artifact that ships: the image build, the bundled
ghostscript and poppler, the response headers as an HTTP client sees them, and
the deployed body cap over a socket. Unit tests also check the body cap with
small custom limits. `scripts/smoke.sh` covers the deployed application, and
CI runs it under the same constraints as the Deployment — non-root, read-only
root filesystem, capabilities dropped — so a change that quietly needs to write
outside `/tmp` fails the build instead of the cluster.

---

## 13. Known Limitations and Future Considerations

| Limitation | Impact | Possible future fix |
|---|---|---|
| No auth | Anyone on the network can use it | Add basic auth via nginx-ingress annotation |
| No rate limiting | Many concurrent compress jobs can still saturate the CPU limit | Add Gin middleware; per-request timeouts already bound each job |
| Ingress serves plain HTTP | `protect`/`unlock` passwords cross the LAN in the clear | Terminate TLS at the ingress (commented block in `k8s/ingress.yaml`); the Tailscale route already has HTTPS |
| No load/soak testing | Behaviour under sustained concurrent use is unmeasured | Drive the smoke suite in parallel against a deployed instance |
| Single replica | No HA | Scale to 2+ replicas (stateless, safe to do) |
| No progress feedback | Long compress jobs appear frozen | Add SSE or WebSocket progress endpoint |
| ghostscript subprocess | Extra process fork per compress request | Acceptable at homelab scale |
