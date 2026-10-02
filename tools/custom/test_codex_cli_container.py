#!/usr/bin/env python3
"""Exercise the published image's real Codex with fake, local-only Responses.

Run on a Linux Docker host: test_codex_cli_container.py IMAGE@sha256:DIGEST.
The CLI and its native shell run as the application UID under the image's
filesystem guard. This never reads a real login or contacts a real model.
"""

import argparse
import base64
from datetime import datetime, timezone
import gzip
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import uuid
import zlib


VERSION = "0.160.0"
MODEL = "gpt-5.4"
ACCOUNT = "acct_fake_container_smoke"
HTML = '<!doctype html><html><body><svg xmlns="http://www.w3.org/2000/svg"></svg></body></html>'
PROMPT = "生成 html，内容是 svg 绘制鹈鹕骑自行车 2D 动画，不用进行测试"
MOUNT = "/tmp/sub2api-codex-container-smoke"
TASK = MOUNT + "/task"
WORK = TASK + "/workspace"
FAKE_SECRET = "FAKE_SERVICE_SECRET_ONLY"
FAKE_ENV = "FAKE_PARENT_ENV_ONLY"


class SmokeError(Exception):
    pass


def require(condition, message):
    if not condition:
        raise SmokeError(message)


def fake_token():
    def encode(value):
        return base64.urlsafe_b64encode(json.dumps(value).encode()).decode().rstrip("=")

    claims = {
        "exp": int(time.time()) + 86400,
        "https://api.openai.com/auth": {
            "chatgpt_account_id": ACCOUNT,
            "chatgpt_user_id": "user_fake_container_smoke",
            "chatgpt_plan_type": "pro",
        },
    }
    return encode({"alg": "none"}) + "." + encode(claims) + ".fake_smoke_only"


def tool_command():
    # Do not specify a shell in the tool arguments: this also verifies that
    # the official package's default shell works in the Alpine runtime.
    return "\n".join([
        "set -eu",
        "printf '%s' '" + HTML + "' > result.html",
        "/opt/codex/codex-path/rg --version | head -n 1",
        "/opt/codex/codex-resources/bwrap --version",
        "/usr/local/bin/codex-linux-sandbox --help >/dev/null",
        "test -x /usr/local/bin/codex-execve-wrapper",
        "test -x /usr/local/bin/apply_patch",
        "env",
        "if cat " + MOUNT + "/service-secret.txt >/dev/null 2>&1; then",
        "  echo SERVICE_READABLE; exit 91",
        "else echo SERVICE_READ_BLOCKED; fi",
        "if printf CHANGED > " + MOUNT + "/service-secret.txt 2>/dev/null; then",
        "  echo SERVICE_WRITABLE; exit 92",
        "else echo SERVICE_WRITE_BLOCKED; fi",
        "if cat /proc/self/environ >/dev/null 2>&1; then echo PROC_READABLE; exit 93; fi",
        "echo PROC_READ_BLOCKED",
    ])


def response_events(tool_call):
    if tool_call:
        item = {
            "id": "fc_smoke", "type": "function_call", "status": "completed",
            "call_id": "call_smoke", "name": "exec_command",
            "arguments": json.dumps({"cmd": tool_command(), "max_output_tokens": 3000,
                                     "yield_time_ms": 1000, "login": False}),
        }
    else:
        item = {
            "id": "msg_smoke", "type": "message", "status": "completed", "role": "assistant",
            "content": [{"type": "output_text", "text": HTML, "annotations": []}],
        }
    response = {
        "id": "resp_smoke", "object": "response", "created_at": int(time.time()),
        "status": "completed", "model": MODEL, "output": [item],
        "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2,
                  "input_tokens_details": {"cached_tokens": 0},
                  "output_tokens_details": {"reasoning_tokens": 0}},
    }
    events = [
        {"type": "response.created", "response": dict(response, status="in_progress", output=[])},
        {"type": "response.output_item.added", "output_index": 0,
         "item": dict(item, status="in_progress", content=[])},
    ]
    if not tool_call:
        events.append({"type": "response.output_text.delta", "item_id": item["id"],
                       "output_index": 0, "content_index": 0, "delta": HTML})
    events.extend([
        {"type": "response.output_item.done", "output_index": 0, "item": item},
        {"type": "response.completed", "response": response},
    ])
    return "".join("event: " + event["type"] + "\ndata: " + json.dumps(event) + "\n\n"
                   for event in events).encode()


class LocalResponses(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self):
        self.requests = []
        self.tool_sent = False
        self.lock = threading.Lock()
        super().__init__(("127.0.0.1", 0), Handler)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, status, data, content_type="application/json"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_CONNECT(self):
        # The CLI's HTTP(S) proxies all point here. Only localhost is exempt;
        # catalog/telemetry/plugin requests cannot reach a real upstream.
        self.reply(502, b'{"error":"external traffic blocked by smoke fixture"}')

    def do_GET(self):
        if self.path.startswith("/"):
            self.reply(200, b'{"models":[],"items":[],"config":{}}')
        else:
            self.reply(502, b'{"error":"external traffic blocked by smoke fixture"}')

    def do_POST(self):
        if not self.path.startswith("/"):
            self.reply(502, b'{"error":"external traffic blocked by smoke fixture"}')
            return
        length = int(self.headers.get("Content-Length", "0"))
        if length < 0 or length > 12 * 1024 * 1024:
            self.reply(413, b'{}')
            return
        raw = self.rfile.read(length)
        try:
            encoding = self.headers.get("Content-Encoding", "").lower()
            if encoding == "gzip":
                raw = gzip.decompress(raw)
            elif encoding == "deflate":
                raw = zlib.decompress(raw)
            elif encoding not in ("", "identity"):
                raise ValueError("unsupported request compression")
            body = json.loads(raw)
        except (ValueError, OSError, zlib.error):
            self.reply(400, b'{"error":"invalid fixture request"}')
            return
        if not self.path.endswith("/responses"):
            self.reply(200, b'{}')
            return
        with self.server.lock:
            self.server.requests.append({"headers": {key.lower(): value for key, value in self.headers.items()},
                                         "body": body})
            tool_call = not self.server.tool_sent
            self.server.tool_sent = True
        self.reply(200, response_events(tool_call), "text/event-stream")


def cli_args(url):
    return [
        "exec", "--json", "--ephemeral", "--ignore-user-config", "--ignore-rules",
        "--skip-git-repo-check", "--color", "never", "--cd", WORK, "--model", MODEL,
        "--output-last-message", TASK + "/final-message.txt", "--sandbox", "danger-full-access",
        "-c", 'approval_policy="never"', "-c", 'model_reasoning_effort="high"',
        "-c", 'web_search="disabled"', "-c", 'shell_environment_policy.inherit="none"',
        "-c", 'shell_environment_policy.set={PATH="/usr/local/bin:/usr/bin:/bin:/opt/codex/codex-path",'
              'LANG="C.UTF-8",HOME="' + WORK + '",TMPDIR="' + WORK + '"}',
        "-c", 'chatgpt_base_url="' + url + '/backend-api/"',
        "-c", 'model_provider="smoke"',
        "-c", 'model_providers.smoke={name="Local smoke",wire_api="responses",'
              'requires_openai_auth=true,supports_websockets=false,base_url="' + url + '/backend-api/codex"}',
        "-",
    ]


def validate_run(process, requests, token, fixture):
    require(process.returncode == 0, "native Codex did not exit successfully")
    events = []
    for line in process.stdout.splitlines():
        if line.startswith("{"):
            try:
                events.append(json.loads(line))
            except ValueError:
                raise SmokeError("native Codex emitted invalid JSON events") from None
    commands = [event["item"] for event in events if event.get("type") == "item.completed"
                and event.get("item", {}).get("type") == "command_execution"]
    require(commands and all(command.get("status") == "completed" and command.get("exit_code") == 0
                             for command in commands), "native shell tool did not complete successfully")
    output = "".join(command.get("aggregated_output", "") for command in commands)
    for marker in ("SERVICE_READ_BLOCKED", "SERVICE_WRITE_BLOCKED", "PROC_READ_BLOCKED", "ripgrep ", "bubblewrap "):
        require(marker in output, "missing native tool or isolation marker: " + marker.strip())
    for forbidden in (FAKE_ENV, token, "CODEX_HOME=", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY="):
        require(forbidden not in output, "native tool inherited a parent credential/environment variable")
    require((fixture / "task/workspace/result.html").is_file(), "native shell produced no HTML artifact")
    require((fixture / "task/workspace/result.html").read_text() == HTML, "native HTML artifact differs")
    require((fixture / "task/final-message.txt").read_text().strip() == HTML, "native final message differs")
    require((fixture / "service-secret.txt").read_text() == FAKE_SECRET, "outside fixture file was modified")
    require(len(requests) >= 2, "native CLI did not complete the tool result round trip")
    for request in requests:
        headers, body = request["headers"], request["body"]
        require(headers.get("authorization") == "Bearer " + token, "native OAuth Bearer header differs")
        require(headers.get("chatgpt-account-id") == ACCOUNT, "native OAuth account header differs")
        require(headers.get("originator") == "codex_cli_rs", "native originator differs")
        require(headers.get("user-agent", "").startswith("codex_cli_rs/" + VERSION + " "), "native User-Agent differs")
        require(body.get("model") == MODEL, "native model differs")
        require(body.get("reasoning", {}).get("effort") == "high", "native reasoning effort differs")
    tool_names = {tool.get("name") for tool in requests[0]["body"].get("tools", [])}
    require("exec_command" in tool_names, "native CLI did not advertise its shell tool")
    require(PROMPT in json.dumps(requests[0]["body"].get("input"), ensure_ascii=False), "native prompt differs")


def run_smoke(image):
    docker = shutil.which("docker")
    require(docker, "Docker executable is unavailable")
    token = fake_token()
    name = "sub2api-codex-smoke-" + uuid.uuid4().hex[:12]
    process = None
    server = LocalResponses()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    url = "http://127.0.0.1:" + str(server.server_port)
    try:
        with tempfile.TemporaryDirectory(prefix="sub2api-codex-smoke-") as directory:
            fixture = Path(directory)
            for child in ("task/codex", "task/workspace", "task/tmp", "task/runtime"):
                (fixture / child).mkdir(parents=True, exist_ok=True)
            auth = {
                "auth_mode": "chatgptAuthTokens", "OPENAI_API_KEY": None,
                "tokens": {"id_token": token, "access_token": token, "refresh_token": "", "account_id": ACCOUNT},
                "last_refresh": datetime.now(timezone.utc).isoformat(),
            }
            (fixture / "task/codex/auth.json").write_text(json.dumps(auth))
            (fixture / "task/codex/auth.json").chmod(0o600)
            (fixture / "task/codex/installation_id").write_text(str(uuid.uuid4()))
            (fixture / "service-secret.txt").write_text(FAKE_SECRET)
            volume = str(fixture) + ":" + MOUNT
            setup = [docker, "run", "--rm", "--name", name + "-setup", "--network", "none", "--user", "0:0",
                     "--volume", volume, "--entrypoint", "/bin/sh", image, "-c",
                     'chown -R 1000:1000 "$1" && chmod 700 "$1/task"', "sh", MOUNT]
            try:
                result = subprocess.run(setup, capture_output=True, text=True, timeout=45)
                require(result.returncode == 0, "container fixture setup failed: " + result.stderr[-1500:])
                environment = {
                    "PATH": "/usr/local/bin:/usr/bin:/bin:/opt/codex/codex-path", "LANG": "C.UTF-8", "TERM": "dumb",
                    "HOME": TASK, "CODEX_HOME": TASK + "/codex", "TMPDIR": TASK + "/tmp",
                    "XDG_RUNTIME_DIR": TASK + "/runtime", "CODEX_INTERNAL_ORIGINATOR_OVERRIDE": "codex_cli_rs",
                    "HTTP_PROXY": url, "HTTPS_PROXY": url, "ALL_PROXY": url, "NO_PROXY": "127.0.0.1,localhost,::1",
                    "PROBE_PARENT_SECRET": FAKE_ENV,
                }
                command = [docker, "run", "--rm", "--interactive", "--name", name, "--network", "host",
                           "--user", "1000:1000",
                           "--volume", volume, "--workdir", WORK, "--entrypoint", "/usr/bin/env", image, "-i"]
                command.extend(key + "=" + value for key, value in environment.items())
                command.extend(["/usr/local/bin/codex-test-sandbox", "/usr/local/bin/codex", TASK, "--"])
                command.extend(cli_args(url))
                process = subprocess.run(command, input=PROMPT, capture_output=True, text=True, timeout=120)
            finally:
                # Killing a timed-out Docker client does not kill its container.
                for container in (name, name + "-setup"):
                    subprocess.run([docker, "rm", "-f", container], capture_output=True, timeout=20, check=False)
                restore = subprocess.run(
                    [docker, "run", "--rm", "--network", "none", "--user", "0:0", "--volume", volume,
                     "--entrypoint", "/bin/sh", image, "-c", 'chown -R "$1:$2" "$3"', "sh",
                     str(os.getuid()), str(os.getgid()), MOUNT],
                    capture_output=True, text=True, timeout=45,
                )
                require(restore.returncode == 0, "container fixture ownership cleanup failed")
            validate_run(process, server.requests, token, fixture)
    except (SmokeError, subprocess.TimeoutExpired) as error:
        diagnostic = (process.stderr + "\n" + process.stdout)[-6000:] if process is not None else ""
        for secret in (token, FAKE_ENV, FAKE_SECRET):
            diagnostic = diagnostic.replace(secret, "[redacted fake fixture]")
        raise SmokeError(str(error) + ("\n" + diagnostic if diagnostic else "")) from None
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image", help="published immutable IMAGE@sha256:DIGEST")
    args = parser.parse_args()
    if args.image.startswith("-") or not re.fullmatch(r"[^@\s]+@sha256:[a-fA-F0-9]{64}", args.image):
        parser.error("the image must use an immutable sha256 digest")
    try:
        run_smoke(args.image)
    except (SmokeError, OSError) as error:
        print("Codex container smoke failed: " + str(error), file=sys.stderr)
        return 1
    print("Codex container smoke passed: native CLI/tool, OAuth headers, HTML artifact, and filesystem isolation.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
