#!/usr/bin/env python3
"""Minimal, dependency-free HTTP client for agents (stdlib ``urllib``).

Supports the everyday cases: any method, custom headers, query params, a
JSON or raw body, retries with exponential backoff on transient failures
(connection errors, 429, 5xx), and Link-header / JSON-cursor pagination.

Prints the parsed JSON (or raw text) to stdout. On an unrecoverable HTTP
error it prints the status + body to stderr and exits non-zero, so the
caller can branch on the exit code.

Examples::

    python request.py GET https://api.example.com/things
    python request.py POST https://api.example.com/things \\
        --header "Authorization: Bearer $TOKEN" --json '{"a": 1}' --retries 3
    python request.py GET https://api.example.com/things --paginate --max-pages 20
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

# Status codes worth retrying: rate limit + server-side errors.
_RETRYABLE_STATUS = {429, 500, 502, 503, 504}


class HTTPResult:
    """One HTTP response: status, headers, decoded body."""

    def __init__(self, status: int, headers: dict[str, str], body: bytes):
        self.status = status
        self.headers = headers
        self.body = body

    def text(self) -> str:
        return self.body.decode("utf-8", errors="replace")

    def json(self):
        return json.loads(self.text()) if self.body else None

    def is_json(self) -> bool:
        return "application/json" in self.headers.get("Content-Type", "").lower()


def _merge_query(url: str, query: list[str]) -> str:
    """Merge ``key=value`` query items into ``url``'s query string."""
    if not query:
        return url
    parsed = urllib.parse.urlsplit(url)
    pairs = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    for item in query:
        key, _, value = item.partition("=")
        pairs.append((key, value))
    new_query = urllib.parse.urlencode(pairs)
    return urllib.parse.urlunsplit(parsed._replace(query=new_query))


def _parse_headers(header_args: list[str]) -> dict[str, str]:
    headers: dict[str, str] = {}
    for raw in header_args:
        if ":" not in raw:
            raise ValueError(f"bad --header {raw!r} (expected 'Name: value')")
        name, _, value = raw.partition(":")
        headers[name.strip()] = value.strip()
    return headers


def _do_request(
    method: str,
    url: str,
    *,
    headers: dict[str, str],
    body: bytes | None,
    timeout: float,
) -> HTTPResult:
    """Issue a single request; raise urllib.error.HTTPError on 4xx/5xx."""
    req = urllib.request.Request(url, data=body, method=method.upper())
    for name, value in headers.items():
        req.add_header(name, value)
    with urllib.request.urlopen(req, timeout=timeout) as resp:  # noqa: S310 (caller-supplied URL is intentional)
        return HTTPResult(resp.status, dict(resp.headers.items()), resp.read())


def request(
    method: str,
    url: str,
    *,
    headers: dict[str, str] | None = None,
    body: bytes | None = None,
    timeout: float = 30.0,
    retries: int = 0,
    backoff: float = 0.5,
) -> HTTPResult:
    """Issue a request, retrying transient failures with backoff.

    Retries connection errors and 429/5xx responses up to ``retries``
    times. A ``Retry-After`` header (seconds) overrides the computed
    backoff. Non-retryable HTTP errors are re-raised immediately.
    """
    headers = dict(headers or {})
    attempt = 0
    while True:
        try:
            return _do_request(method, url, headers=headers, body=body, timeout=timeout)
        except urllib.error.HTTPError as exc:
            if exc.code not in _RETRYABLE_STATUS or attempt >= retries:
                # Wrap the error body into a result-bearing exception path:
                # surface status + body to the caller.
                detail = exc.read().decode("utf-8", errors="replace") if exc.fp else ""
                raise _ApiError(exc.code, detail) from exc
            delay = _retry_after(exc.headers) or backoff * (2**attempt)
        except urllib.error.URLError as exc:
            if attempt >= retries:
                raise _ApiError(0, f"connection error: {exc.reason}") from exc
            delay = backoff * (2**attempt)
        attempt += 1
        time.sleep(delay)


def _retry_after(headers) -> float | None:
    value = headers.get("Retry-After") if headers else None
    if value and value.isdigit():
        return float(value)
    return None


class _ApiError(Exception):
    """An HTTP error carrying the status code and response body."""

    def __init__(self, status: int, body: str):
        super().__init__(f"HTTP {status}")
        self.status = status
        self.body = body


def _next_url(result: HTTPResult) -> str | None:
    """Find the next-page URL: Link header rel=next, then JSON next/cursor."""
    link = result.headers.get("Link", "")
    for part in link.split(","):
        if 'rel="next"' in part:
            start = part.find("<")
            end = part.find(">")
            if 0 <= start < end:
                return part[start + 1 : end]
    if result.is_json():
        data = result.json()
        if isinstance(data, dict):
            for key in ("next", "next_url", "nextPageUrl"):
                nxt = data.get(key)
                if isinstance(nxt, str) and nxt:
                    return nxt
    return None


def _emit(result: HTTPResult) -> None:
    if result.is_json():
        json.dump(result.json(), sys.stdout, indent=2, ensure_ascii=False)
        sys.stdout.write("\n")
    else:
        sys.stdout.write(result.text())
        if not result.text().endswith("\n"):
            sys.stdout.write("\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Minimal stdlib HTTP client.")
    parser.add_argument("method", help="HTTP method (GET, POST, …)")
    parser.add_argument("url", help="request URL")
    parser.add_argument("--header", "-H", action="append", default=[], help="'Name: value'")
    parser.add_argument("--query", "-q", action="append", default=[], help="'key=value'")
    parser.add_argument("--json", dest="json_body", help="JSON body string")
    parser.add_argument("--data", dest="raw_body", help="raw body string")
    parser.add_argument("--timeout", type=float, default=30.0)
    parser.add_argument("--retries", type=int, default=0)
    parser.add_argument("--paginate", action="store_true", help="follow next-page links")
    parser.add_argument("--max-pages", type=int, default=50)
    args = parser.parse_args(argv)

    try:
        headers = _parse_headers(args.header)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    body: bytes | None = None
    if args.json_body is not None:
        body = args.json_body.encode("utf-8")
        headers.setdefault("Content-Type", "application/json")
    elif args.raw_body is not None:
        body = args.raw_body.encode("utf-8")

    url = _merge_query(args.url, args.query)

    try:
        pages = 0
        while url:
            result = request(
                args.method,
                url,
                headers=headers,
                body=body,
                timeout=args.timeout,
                retries=args.retries,
            )
            _emit(result)
            pages += 1
            if not args.paginate or pages >= args.max_pages:
                break
            url = _next_url(result)
            body = None  # only the first request carries the body
    except _ApiError as exc:
        print(f"request failed: HTTP {exc.status}\n{exc.body}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
