"""Opaque host TLS streams into one nonce-bound owned qualification container."""

import os
from pathlib import Path
import re
import socket
import socketserver
import subprocess
import threading
from uuid import UUID

FORWARDER = """import os,socket,sys,threading
peer=socket.create_connection(('127.0.0.1',int(sys.argv[1])),10)
peer.settimeout(360)
def inbound():
 try:
  while True:
   data=os.read(0,65536)
   if not data:break
   peer.sendall(data)
 except OSError:pass
 try:peer.shutdown(socket.SHUT_WR)
 except OSError:pass
threading.Thread(target=inbound,daemon=True).start()
try:
 while True:
  data=peer.recv(65536)
  if not data:break
  pending=memoryview(data)
  while pending:pending=pending[os.write(1,pending):]
except OSError:pass
finally:peer.close()
"""


class DockerAgentBridge:
    def __init__(self, docker, container_id, run_id, *, port, log):
        if (
            not isinstance(docker, (tuple, list))
            or not docker
            or any(
                not isinstance(part, str) or not part or "\0" in part for part in docker
            )
            or not isinstance(container_id, str)
            or not re.fullmatch(r"[0-9a-f]{64}", container_id)
            or type(port) is not int
            or not 1 <= port <= 65535
        ):
            raise ValueError("Use an exact owned container and loopback target")
        if (
            not isinstance(run_id, str)
            or str(UUID(run_id)) != run_id
            or not UUID(run_id).int
        ):
            raise ValueError("Use the qualification container's exact run nonce")
        self.docker, self.container_id, self.run_id = (
            tuple(docker),
            container_id,
            run_id,
        )
        self.port, self.log_path = port, Path(log)
        self._log = self._server = self._thread = None
        self._condition = threading.Condition()
        self._sessions = {}
        self._count = 0
        self._started = self._closed = False

    def __enter__(self):
        if self._started or self._closed:
            raise RuntimeError("The qualification bridge cannot be reused")
        self._started = True
        try:
            descriptor = os.open(
                self.log_path,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                0o600,
            )
            self._log = os.fdopen(descriptor, "wb")
            binding = (self.docker, self.container_id, self.run_id, self.port)
            observed = subprocess.run(
                [
                    *binding[0],
                    "inspect",
                    "--format",
                    '{{ index .Config.Labels "astrolift.qualification.run" }}',
                    binding[1],
                ],
                stdout=subprocess.PIPE,
                stderr=self._log,
                text=True,
                check=True,
            ).stdout.strip()
            if observed != binding[2]:
                raise RuntimeError("The qualification container run binding changed")
            owner = self

            class Handler(socketserver.BaseRequestHandler):
                def handle(self):
                    owner._forward(self.request, binding)

            class Server(socketserver.ThreadingTCPServer):
                daemon_threads = True

            self._server = Server(("127.0.0.1", 0), Handler)
            self._thread = threading.Thread(
                target=self._server.serve_forever, daemon=True
            )
            self._thread.start()
            return self
        except Exception:
            self.close()
            raise

    @property
    def endpoint(self):
        if self._server is None or self._closed:
            raise RuntimeError("The qualification bridge is not active")
        return f"https://127.0.0.1:{self._server.server_address[1]}"

    @property
    def session_count(self):
        with self._condition:
            return self._count

    def _forward(self, connection, binding):
        docker, container_id, _run_id, port = binding
        with self._condition:
            if self._closed:
                return
            session = subprocess.Popen(
                [
                    *docker,
                    "exec",
                    "-i",
                    container_id,
                    "python",
                    "-B",
                    "-c",
                    FORWARDER,
                    str(port),
                ],
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=self._log,
                bufsize=0,
            )
            self._sessions[session] = connection
            self._count += 1
        connection.settimeout(360)

        def upstream():
            try:
                while data := connection.recv(65536):
                    pending = memoryview(data)
                    while pending:
                        pending = pending[os.write(session.stdin.fileno(), pending) :]
            except OSError:
                pass
            finally:
                session.stdin.close()

        sender = threading.Thread(target=upstream, daemon=True)
        sender.start()
        try:
            while data := os.read(session.stdout.fileno(), 65536):
                connection.sendall(data)
        except OSError:
            pass
        finally:
            self._shutdown(connection)
            sender.join(timeout=5)
            self._finish(session)
            session.stdout.close()
            with self._condition:
                self._sessions.pop(session, None)
                self._condition.notify_all()

    @staticmethod
    def _shutdown(connection):
        try:
            connection.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass

    @staticmethod
    def _finish(session):
        try:
            session.wait(timeout=5)
        except subprocess.TimeoutExpired:
            session.terminate()
            try:
                session.wait(timeout=5)
            except subprocess.TimeoutExpired:
                session.kill()
                session.wait(timeout=5)

    def close(self):
        if self._closed:
            return
        self._closed = True
        if self._server is not None:
            if self._thread is not None and self._thread.is_alive():
                self._server.shutdown()
                self._thread.join(timeout=5)
            self._server.server_close()
        with self._condition:
            active = list(self._sessions.items())
        for session, connection in active:
            self._shutdown(connection)
            self._finish(session)
        with self._condition:
            finished = self._condition.wait_for(lambda: not self._sessions, timeout=5)
        if self._log is not None:
            self._log.close()
        if not finished:
            raise RuntimeError("Qualification proxy workers did not close")

    def __exit__(self, *_error):
        self.close()
