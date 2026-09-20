#!/usr/bin/env python3
"""A registry that reproduces Nexus behind path based routing.

It is mounted at /repository/docker-hosted/v2 but returns an upload Location
relative to the registry root, without that prefix. A client that resolves
the Location against the bare origin lands outside the registry and is
answered with 405 -- the failure this test guards against.
"""
import http.server, re, sys, uuid

PREFIX = "/repository/docker-hosted"
BLOBS, UPLOADS = {}, {}


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _send(self, code, headers=None, body=b""):
        self.send_response(code)
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == PREFIX + "/v2/":
            self._send(200, {"Docker-Distribution-Api-Version": "registry/2.0"}, b"{}")
        elif self.path == "/count":
            self._send(200, body=str(len(BLOBS)).encode())
        else:
            self._send(404)

    def do_HEAD(self):
        m = re.match(re.escape(PREFIX) + r"/v2/(.+)/blobs/(sha256:[0-9a-f]+)$", self.path)
        self._send(200 if (m and m.group(2) in BLOBS) else 404)

    def do_POST(self):
        m = re.match(re.escape(PREFIX) + r"/v2/(.+)/blobs/uploads/$", self.path)
        if not m:
            self._send(405)
            return
        uid = uuid.uuid4().hex
        UPLOADS[uid] = m.group(1)
        self._send(202, {"Location": "/v2/%s/blobs/uploads/%s" % (m.group(1), uid),
                         "Docker-Upload-UUID": uid})

    def do_PUT(self):
        path = self.path.split("?")[0]
        if not path.startswith(PREFIX):
            self._send(405)
            return
        data = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if "/blobs/uploads/" in path:
            BLOBS[self.path.split("digest=")[-1]] = data
            self._send(201)
        elif "/manifests/" in path:
            self._send(201)
        else:
            self._send(405)


http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
