#!/usr/bin/env bash
#
# Functional smoke test: drives a running PDF Handler over HTTP and checks each
# endpoint end to end, plus the rejections that only show up in a real request
# (the configured body cap, the response headers, the container's own ghostscript
# and poppler). The Go tests cover the handlers in isolation; this covers the
# thing that actually ships.
#
# Usage: scripts/smoke.sh [base-url]     (default http://127.0.0.1:8080)

set -uo pipefail

BASE="${1:-http://127.0.0.1:8080}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0

# check <name> <expected-status> <curl args...>
check() {
  local name="$1" want="$2"; shift 2
  local got
  got="$(curl -sS -o "$WORK/out" -w '%{http_code}' --max-time 120 "$@" 2>"$WORK/err")"
  if [[ "$got" == "$want" ]]; then
    printf '  ok    %-42s %s\n' "$name" "$got"
    pass=$((pass + 1))
  else
    printf '  FAIL  %-42s got %s, want %s\n' "$name" "$got" "$want"
    [[ -s "$WORK/out" ]] && printf '        body: %s\n' "$(head -c 200 "$WORK/out")"
    [[ -s "$WORK/err" ]] && printf '        err:  %s\n' "$(head -c 200 "$WORK/err")"
    fail=$((fail + 1))
  fi
}

# expect_body <name> <substring> — asserts the last response contained it
expect_body() {
  local name="$1" want="$2"
  if grep -qF "$want" "$WORK/out"; then
    printf '  ok    %-42s contains %s\n' "$name" "$want"
    pass=$((pass + 1))
  else
    printf '  FAIL  %-42s missing %s\n' "$name" "$want"
    printf '        body: %s\n' "$(head -c 200 "$WORK/out")"
    fail=$((fail + 1))
  fi
}

echo "==> Waiting for $BASE"
for i in $(seq 1 30); do
  curl -sf --max-time 2 "$BASE/healthz" >/dev/null 2>&1 && break
  if [[ $i -eq 30 ]]; then
    echo "  FAIL  service never became healthy" >&2
    exit 1
  fi
  sleep 1
done
echo "  ok    healthy after ${i}s"

echo
echo "==> Fixtures"
# A 3-page PDF and a PNG, built with the same tools the container ships.
printf '%%!PS\n/Helvetica findfont 24 scalefont setfont\n72 700 moveto (Page 1) show showpage\n72 700 moveto (Page 2) show showpage\n72 700 moveto (Page 3) show showpage\n' > "$WORK/t.ps"
gs -q -dNOPAUSE -dBATCH -dSAFER -sDEVICE=pdfwrite -sOutputFile="$WORK/a.pdf" "$WORK/t.ps"
cp "$WORK/a.pdf" "$WORK/b.pdf"
pdftoppm -png -r 40 -f 1 -l 1 "$WORK/a.pdf" "$WORK/img"
mv "$WORK/img-1.png" "$WORK/img.png"
echo "  ok    a.pdf b.pdf img.png"

echo
echo "==> Operations"
check "merge"          200 -F "files[]=@$WORK/a.pdf" -F "files[]=@$WORK/b.pdf" "$BASE/api/merge"
check "split"          200 -F "file=@$WORK/a.pdf" -F "span=1"                  "$BASE/api/split"
check "compress"       200 -F "file=@$WORK/a.pdf" -F "quality=screen"          "$BASE/api/compress"
check "rotate"         200 -F "file=@$WORK/a.pdf" -F "degrees=90" -F "pages=1,3" "$BASE/api/rotate"
check "pdf-to-image"   200 -F "file=@$WORK/a.pdf" -F "format=png" -F "dpi=72"  "$BASE/api/pdf-to-image"
check "image-to-pdf"   200 -F "files[]=@$WORK/img.png"                         "$BASE/api/image-to-pdf"
check "preview"        200 -F "file=@$WORK/a.pdf"                              "$BASE/api/preview"
expect_body "preview thumbnails" '"pageCount":3'

# protect/unlock only mean something as a pair
curl -sS -o "$WORK/enc.pdf" --max-time 60 -F "file=@$WORK/a.pdf" -F "password=s3cret" "$BASE/api/protect"
check "protect produced a PDF" 200 -F "file=@$WORK/a.pdf" -F "password=s3cret"  "$BASE/api/protect"
check "unlock round-trip"      200 -F "file=@$WORK/enc.pdf" -F "password=s3cret" "$BASE/api/unlock"
check "unlock wrong password"  500 -F "file=@$WORK/enc.pdf" -F "password=nope"   "$BASE/api/unlock"

echo
echo "==> Input rejection"
echo "definitely not a pdf" > "$WORK/fake.pdf"
check "non-PDF content"        400 -F "file=@$WORK/fake.pdf"                     "$BASE/api/compress"
: > "$WORK/empty.pdf"
check "empty file"             400 -F "file=@$WORK/empty.pdf"                    "$BASE/api/preview"
check "traversal in filename"  400 -F "files[]=@$WORK/img.png;filename=x.a/../../../../tmp/pwned" "$BASE/api/image-to-pdf"
check "disallowed image type"  400 -F "files[]=@$WORK/img.png;filename=x.svg"    "$BASE/api/image-to-pdf"
check "degrees not a quarter"  400 -F "file=@$WORK/a.pdf" --form-string "degrees=45"        "$BASE/api/rotate"
check "shell chars in pages"   400 -F "file=@$WORK/a.pdf" --form-string 'pages=1;rm -rf /'  "$BASE/api/rotate"
check "traversal in pages"     400 -F "file=@$WORK/a.pdf" --form-string 'pages=../../etc/passwd' "$BASE/api/rotate"
check "negative span"          400 -F "file=@$WORK/a.pdf" --form-string "span=-5"           "$BASE/api/split"
check "missing password"       400 -F "file=@$WORK/a.pdf" --form-string "password="         "$BASE/api/protect"
check "merge needs two files"  400 -F "files[]=@$WORK/a.pdf"                                "$BASE/api/merge"

echo
echo "==> Body limit"
# Read the deployed setting so this also works with MAX_UPLOAD_MB overrides.
check "upload configuration" 200 "$BASE/api/config"
upload_limit="$(sed -n 's/.*"maxUploadBytes":\([0-9]*\).*/\1/p' "$WORK/out")"
if [[ ! "$upload_limit" =~ ^[1-9][0-9]*$ ]]; then
  echo "  FAIL  invalid upload configuration" >&2
  exit 1
fi
head -c "$((upload_limit + 1048576))" /dev/zero > "$WORK/big.pdf"
check "upload above configured limit" 413 -F "file=@$WORK/big.pdf" "$BASE/api/compress"
expect_body "413 explains the limit" "limit is $((upload_limit / 1048576)) MB"
rm -f "$WORK/big.pdf"

echo
echo "==> Responses"
check "UI is served"           200 "$BASE/"
headers="$(curl -sS -D - -o /dev/null --max-time 10 "$BASE/healthz")"
for h in "Content-Security-Policy" "X-Content-Type-Options: nosniff" "X-Frame-Options: DENY" "Referrer-Policy: no-referrer"; do
  if grep -qi "$h" <<<"$headers"; then
    printf '  ok    %-42s present\n' "${h%%:*}"
    pass=$((pass + 1))
  else
    printf '  FAIL  %-42s missing\n' "${h%%:*}"
    fail=$((fail + 1))
  fi
done

# Ghostscript and pdfcpu errors quote temp paths; they must stay in the log.
curl -sS -o "$WORK/out" --max-time 30 -F "file=@$WORK/enc.pdf" -F "password=nope" "$BASE/api/unlock"
if grep -qE '/tmp/|pdfhandler-[0-9]|input\.pdf' "$WORK/out"; then
  printf '  FAIL  %-42s leaks internal paths: %s\n' "error body" "$(head -c 150 "$WORK/out")"
  fail=$((fail + 1))
else
  printf '  ok    %-42s no internal paths\n' "error body"
  pass=$((pass + 1))
fi

echo
echo "======================================"
printf 'passed: %d   failed: %d\n' "$pass" "$fail"
echo "======================================"
[[ $fail -eq 0 ]] || exit 1
