#!/usr/bin/env python3
"""Expose DP's health contract without proxying Dify business traffic."""

import argparse
import http.client
import json
import socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


class HealthHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    http_upstreams = []
    tcp_upstreams = []

    def do_GET(self):
        if self.path.split("?", 1)[0] != "/healthz":
            self.send_error(404, "health server only exposes /healthz")
            return
        try:
            for upstream in self.http_upstreams:
                check_http(upstream)
            for upstream in self.tcp_upstreams:
                check_tcp(upstream)
            status, payload = 200, {"status": "ok"}
        except Exception as error:
            print(f"health check failed: {error}", flush=True)
            status, payload = 503, {"status": "error"}
        content = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(content)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(content)
        self.close_connection = True

    do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = do_HEAD = lambda self: self.send_error(
        405, "health server does not proxy Dify requests"
    )

    def log_message(self, message, *args):
        print(f"{self.address_string()} - {message % args}", flush=True)


def check_http(value):
    parsed = urlsplit(value)
    connection = http.client.HTTPConnection(parsed.hostname, parsed.port or 80, timeout=3)
    try:
        connection.request("GET", parsed.path or "/", headers={"Accept": "application/json"})
        response = connection.getresponse()
        response.read(4096)
        if not 200 <= response.status < 400:
            raise RuntimeError(f"{value} returned {response.status}")
    finally:
        connection.close()


def parse_address(value):
    host, separator, port = value.rpartition(":")
    if not separator or not host:
        raise argparse.ArgumentTypeError("address must be host:port")
    try:
        return host, int(port)
    except ValueError as error:
        raise argparse.ArgumentTypeError("port must be an integer") from error


def check_tcp(value):
    with socket.create_connection(value, timeout=3):
        return


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen", type=parse_address, required=True)
    parser.add_argument("--http", action="append", default=[])
    parser.add_argument("--tcp", action="append", type=parse_address, default=[])
    args = parser.parse_args()
    HealthHandler.http_upstreams = args.http
    HealthHandler.tcp_upstreams = args.tcp
    server = ThreadingHTTPServer(args.listen, HealthHandler)
    server.daemon_threads = True
    server.serve_forever()


if __name__ == "__main__":
    main()

