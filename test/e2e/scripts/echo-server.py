"""Minimal OpenAI-compatible echo backend for the cross-cluster E2E.

Serves HTTPS on 8443 with the certificate mounted at /certs. Every POST is
answered with a chat.completion whose assistant message content is a JSON
document describing what the backend received (path, headers, body model),
so tests can assert on what actually arrived after all gateway hops.
"""

import json
import ssl
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CERT = "/certs/server.crt"
KEY = "/certs/server.key"
PORT = 8443


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        self._send(200, {"status": "ok"})

    def do_POST(self):
        raw = self._read_body()
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            body = {}
        model = body.get("model") or "echo"
        received = {
            "path": self.path,
            "model": model,
            "headers": {k.lower(): v for k, v in self.headers.items()},
        }
        self._send(200, {
            "id": "chatcmpl-echo",
            "object": "chat.completion",
            "created": 0,
            "model": model,
            "choices": [{
                "index": 0,
                "message": {"role": "assistant", "content": json.dumps(received)},
                "finish_reason": "stop",
            }],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        })

    # Envoy re-frames a body mutated by ext_proc as chunked, so both framings
    # must be handled.
    def _read_body(self):
        if self.headers.get("transfer-encoding", "").lower() == "chunked":
            chunks = []
            while True:
                size = int(self.rfile.readline().split(b";")[0].strip() or b"0", 16)
                if size == 0:
                    while self.rfile.readline() not in (b"\r\n", b"\n", b""):
                        pass  # trailers
                    break
                chunks.append(self.rfile.read(size))
                self.rfile.readline()  # CRLF after chunk
            return b"".join(chunks)
        length = int(self.headers.get("content-length", 0))
        return self.rfile.read(length) if length else b""

    def _send(self, status, payload):
        out = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, fmt, *args):  # keep container logs quiet
        pass


if __name__ == "__main__":
    server = ThreadingHTTPServer(("", PORT), Handler)
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(CERT, KEY)
    server.socket = ctx.wrap_socket(server.socket, server_side=True)
    server.serve_forever()
