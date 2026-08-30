#!/usr/bin/env python3
"""Expose only DP's /healthz contract without proxying inference traffic."""

import argparse
import http.client
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class HealthHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    upstream_host = "engine"
    upstream_port = 8000

    def do_GET(self):
        if self.path.split("?", 1)[0] == "/healthz":
            self.dp_health()
        else:
            self.send_error(404, "health server only exposes /healthz")

    do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = do_HEAD = lambda self: self.send_error(
        405, "health server does not proxy inference requests"
    )

    def dp_health(self):
        connection = http.client.HTTPConnection(self.upstream_host, self.upstream_port, timeout=2)
        try:
            connection.request("GET", "/health", headers={"Accept": "application/json"})
            response = connection.getresponse()
            response.read(4096)
            if response.status != 200:
                raise RuntimeError(f"upstream status {response.status}")
            payload = json.dumps({"status": "ok"}, separators=(",", ":")).encode()
            self.send_response(200)
        except Exception:
            payload = json.dumps({"status": "error"}, separators=(",", ":")).encode()
            self.send_response(503)
        finally:
            connection.close()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("Connection", "close")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(payload)
        self.close_connection = True

    def log_message(self, message, *args):
        print(f"{self.address_string()} - {message % args}", flush=True)


def address(value):
    host, separator, port = value.rpartition(":")
    if not separator or not host:
        raise argparse.ArgumentTypeError("地址必须为 host:port")
    return host, int(port)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--upstream", type=address, required=True)
    parser.add_argument("--listen", type=address, required=True)
    args = parser.parse_args()
    HealthHandler.upstream_host, HealthHandler.upstream_port = args.upstream
    server = ThreadingHTTPServer(args.listen, HealthHandler)
    server.daemon_threads = True
    server.serve_forever()


if __name__ == "__main__":
    main()
