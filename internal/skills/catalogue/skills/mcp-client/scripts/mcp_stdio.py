#!/usr/bin/env python3
"""Minimal MCP stdio client (JSON-RPC 2.0 over a subprocess, stdlib only).

Launches an MCP server as a child process and speaks newline-delimited
JSON-RPC over its stdin/stdout. Runs the required handshake
(``initialize`` then the ``notifications/initialized`` notification) and
then either lists tools or calls one.

This covers the **stdio transport**. For HTTP/SSE-transport servers use
the official ``mcp`` Python SDK; the request shapes are identical.

Examples::

    python mcp_stdio.py --server "uvx mcp-server-time" list-tools
    python mcp_stdio.py --server "uvx mcp-server-time" \\
        call get_current_time --args '{"timezone": "UTC"}'
"""

from __future__ import annotations

import argparse
import json
import shlex
import subprocess
import sys

PROTOCOL_VERSION = "2024-11-05"
CLIENT_INFO = {"name": "astrolift-mcp-client", "version": "0.1.0"}


class MCPError(RuntimeError):
    """An MCP server returned a JSON-RPC error, or the session broke."""


class StdioMCPClient:
    """A JSON-RPC client driving an MCP server over its stdio."""

    def __init__(self, command: str, *, timeout: float = 30.0):
        self._argv = shlex.split(command)
        self._timeout = timeout
        self._proc: subprocess.Popen[str] | None = None
        self._next_id = 0

    def __enter__(self) -> StdioMCPClient:
        self._proc = subprocess.Popen(  # noqa: S603 (caller-supplied server command is intentional)
            self._argv,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )
        self._handshake()
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def close(self) -> None:
        if self._proc is None:
            return
        try:
            if self._proc.stdin and not self._proc.stdin.closed:
                self._proc.stdin.close()
            self._proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self._proc.kill()
        finally:
            self._proc = None

    # -- low-level JSON-RPC ------------------------------------------------

    def _send(self, message: dict) -> None:
        assert self._proc and self._proc.stdin
        self._proc.stdin.write(json.dumps(message) + "\n")
        self._proc.stdin.flush()

    def _read_message(self) -> dict:
        assert self._proc and self._proc.stdout
        line = self._proc.stdout.readline()
        if not line:
            stderr = self._proc.stderr.read() if self._proc.stderr else ""
            raise MCPError(f"server closed the connection. stderr:\n{stderr}")
        return json.loads(line)

    def _request(self, method: str, params: dict | None = None) -> dict:
        """Send a request and return its ``result``, skipping notifications."""
        self._next_id += 1
        req_id = self._next_id
        self._send({"jsonrpc": "2.0", "id": req_id, "method": method, "params": params or {}})
        # Read until we get the response matching our id (servers may
        # interleave notifications, which carry no id).
        while True:
            msg = self._read_message()
            if msg.get("id") != req_id:
                continue
            if "error" in msg:
                raise MCPError(f"{method} -> {msg['error']}")
            return msg.get("result", {})

    def _notify(self, method: str, params: dict | None = None) -> None:
        self._send({"jsonrpc": "2.0", "method": method, "params": params or {}})

    # -- MCP protocol ------------------------------------------------------

    def _handshake(self) -> dict:
        result = self._request(
            "initialize",
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": CLIENT_INFO,
            },
        )
        # Tell the server the handshake is complete (notification, no reply).
        self._notify("notifications/initialized")
        return result

    def list_tools(self) -> list[dict]:
        return self._request("tools/list").get("tools", [])

    def call_tool(self, name: str, arguments: dict | None = None) -> dict:
        return self._request("tools/call", {"name": name, "arguments": arguments or {}})


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Minimal MCP stdio client.")
    parser.add_argument("--server", required=True, help="command that starts the MCP server")
    parser.add_argument("--timeout", type=float, default=30.0)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("list-tools", help="list the server's tools")
    call = sub.add_parser("call", help="call a tool")
    call.add_argument("tool", help="tool name")
    call.add_argument("--args", default="{}", help="JSON arguments object")
    args = parser.parse_args(argv)

    try:
        with StdioMCPClient(args.server, timeout=args.timeout) as client:
            if args.action == "list-tools":
                payload = client.list_tools()
            else:
                tool_args = json.loads(args.args)
                payload = client.call_tool(args.tool, tool_args)
    except (MCPError, OSError, json.JSONDecodeError) as exc:
        print(f"mcp error: {exc}", file=sys.stderr)
        return 1

    json.dump(payload, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
