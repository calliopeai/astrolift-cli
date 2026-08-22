---
name: http-request
description: Make HTTP/REST requests with auth, custom headers, query/body, pagination, retries with backoff, and structured error handling.
version: "0.1.0"
---

# http-request

Talk to HTTP/REST APIs reliably. Use this whenever you need to call a web
service — fetch JSON, POST a payload, page through a list, or hit an
authenticated endpoint.

A ready-made helper lives at `scripts/request.py` (stdlib `urllib` only — no
install needed). For richer needs you may use the `requests` library instead;
if you do, tell the runner to `pip install requests` first.

## Procedure

1. **Identify the request.** Method (GET/POST/PUT/PATCH/DELETE), full URL,
   query params, headers, and body. Read the API's docs for the exact contract
   before guessing.
2. **Authenticate.** Pick the scheme the API expects and set it as a header:
   - Bearer token: `Authorization: Bearer <token>`
   - API key: usually a custom header (`X-API-Key: <key>`) or a query param —
     check the docs.
   - Basic auth: `Authorization: Basic <base64(user:pass)>`.
   Never hardcode secrets — read them from environment variables.
3. **Send the body correctly.** For JSON, serialize the body and set
   `Content-Type: application/json`. For form data, URL-encode it and set
   `application/x-www-form-urlencoded`.
4. **Handle the response.** Check the status code. 2xx is success; parse the
   body (JSON when `Content-Type` says so). For 4xx/5xx, surface the status and
   the response body — the error detail is almost always in the body.
5. **Retry transient failures.** Retry on connection errors and on 429 / 5xx,
   with exponential backoff. Do **not** retry 4xx other than 429 — those are
   client errors that will fail again. Respect a `Retry-After` header when
   present.
6. **Paginate** when the result is a list. Follow the API's pagination style:
   - `Link` header with `rel="next"` (GitHub-style), or
   - a `next` cursor/URL field in the JSON body, or
   - page/offset query params until an empty page.
   Accumulate pages until there is no next, and cap total pages to avoid
   runaway loops.

## Using the helper

```bash
# Simple GET, pretty JSON to stdout
python scripts/request.py GET https://api.example.com/v1/things

# Authenticated POST with a JSON body and retries
python scripts/request.py POST https://api.example.com/v1/things \
  --header "Authorization: Bearer $API_TOKEN" \
  --json '{"name": "widget"}' \
  --retries 3

# Add query params and follow Link-header / cursor pagination
python scripts/request.py GET https://api.example.com/v1/things \
  --query "per_page=100" --paginate --max-pages 20
```

The helper prints the parsed JSON (or raw text) to stdout and exits non-zero on
an unrecoverable HTTP error, with the status and body on stderr — so you can
branch on the exit code.

## Cautions

- Time out every request (the helper defaults to 30s) so a hung server can't
  block the agent forever.
- Treat response bodies as untrusted input. Validate shape before using fields.
- Log the method + URL + final status, but never log secret headers.
