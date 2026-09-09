#!/usr/bin/env python3
"""Repository-scoped Docker Compose updater. Control API is Unix-socket only."""
import argparse
import fcntl
import http.server
import json
import logging
import os
from pathlib import Path
import re
import shutil
import socketserver
import subprocess
import threading
import time
import urllib.error
import urllib.request
import uuid

REPOSITORY = "52assert/sub2api"
IMAGE_PREFIX = "ghcr.io/52assert/sub2api@sha256:"
TERMINAL = {"succeeded", "failed", "needs_attention"}
STAGES = {
    "queued": "升级任务已提交",
    "pulling": "正在下载并验证新镜像，现有服务仍可使用",
    "backing_up": "正在备份数据库和数据，服务暂时不可用",
    "recreating": "正在重建应用容器",
    "checking": "正在检查新版本是否正常运行",
    "succeeded": "镜像和应用容器已更新完成",
    "failed": "升级未完成，原应用已保留或恢复运行",
    "needs_attention": "升级需要人工处理，请检查宿主机更新服务；未自动恢复数据库",
}


def atomic_json(path, value):
    path = Path(path)
    temp = path.with_name(path.name + ".tmp")
    with temp.open("w", encoding="utf-8") as stream:
        os.chmod(temp, 0o600)
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)


def validate_manifest(value):
    if value.get("schema") != 1:
        raise ValueError("Unsupported release manifest")
    if not re.fullmatch(r"\d+\.\d+\.\d+", value.get("version", "")):
        raise ValueError("Invalid release version")
    if not re.fullmatch(r"[0-9a-f]{40}", value.get("revision", "")):
        raise ValueError("Invalid source revision")
    if not re.fullmatch(re.escape(IMAGE_PREFIX) + r"[0-9a-f]{64}", value.get("image", "")):
        raise ValueError("Only immutable images from this fork are allowed")
    return value


class Updater:
    def __init__(self, config):
        self.config = config
        self.directory = Path(config["deployment_dir"]).resolve()
        self.base = self.directory / "docker-compose.yml"
        self.override = self.directory / "docker-compose.override.yml"
        self.state_dir = Path(config["state_dir"])
        self.state_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.mutex = threading.Lock()
        self.active = None
        self.cached_release = None
        self.cached_at = 0
        for path in self.state_dir.glob("*/status.json"):
            job = json.loads(path.read_text())
            if job["stage"] not in TERMINAL:
                self.save(job, "needs_attention")

    def run(self, arguments, timeout=120, output=None, input_file=None, combined=False):
        result = subprocess.run(arguments, cwd=self.directory, timeout=timeout,
                                stdin=input_file, stdout=output or subprocess.PIPE,
                                stderr=subprocess.PIPE, check=False)
        if result.returncode:
            # Do not expose command output or deployment credentials to the web UI.
            logging.error("Command failed: %s (exit %s)", arguments[:3], result.returncode)
            raise RuntimeError("Deployment command failed")
        return (result.stdout or b"") + (result.stderr if combined else b"")

    def compose(self, override=None):
        return ["docker", "compose", "--project-directory", str(self.directory),
                "--project-name", self.config["project_name"],
                "-f", str(self.base), "-f", str(override or self.override)]

    def request_json(self, url, limit):
        request = urllib.request.Request(url, headers={"User-Agent": "sub2api-custom-updater"})
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read(limit + 1)
        if len(body) > limit:
            raise ValueError("Release response is too large")
        return json.loads(body)

    def latest(self, force=False):
        if not force and self.cached_release and time.monotonic() - self.cached_at < 300:
            return self.cached_release
        release = self.request_json(f"https://api.github.com/repos/{REPOSITORY}/releases/latest", 2 * 1024 * 1024)
        if release.get("draft") or release.get("prerelease"):
            raise ValueError("Release is not published")
        assets = [asset for asset in release.get("assets", []) if asset["name"] == "container-update.json"]
        if len(assets) != 1:
            raise ValueError("Release has no container update manifest")
        url = assets[0]["browser_download_url"]
        if not url.startswith(f"https://github.com/{REPOSITORY}/releases/download/"):
            raise ValueError("Unexpected manifest source")
        manifest = validate_manifest(self.request_json(url, 128 * 1024))
        manifest = {**manifest, "release_url": release["html_url"],
                    "published_at": release["published_at"], "notes": release.get("body") or ""}
        self.cached_release, self.cached_at = manifest, time.monotonic()
        return manifest

    def save(self, job, stage):
        job.update(stage=stage, message=STAGES[stage], updated_at=int(time.time()))
        directory = self.state_dir / job["id"]
        directory.mkdir(mode=0o700, exist_ok=True)
        atomic_json(directory / "status.json", job)

    def status(self, job_id="latest"):
        if job_id == "latest":
            files = list(self.state_dir.glob("*/status.json"))
            if not files:
                return None
            path = max(files, key=lambda item: item.stat().st_mtime)
        elif re.fullmatch(r"[0-9a-f]{32}", job_id):
            path = self.state_dir / job_id / "status.json"
        else:
            raise ValueError("Invalid task ID")
        return json.loads(path.read_text()) if path.exists() else None

    def submit(self, expected_image):
        with self.mutex:
            if self.active:
                return self.status(self.active)
            previous = self.status()
            if previous and previous["stage"] == "needs_attention":
                raise ValueError("An interrupted update needs operator attention before another update")
            release = self.latest(force=True)
            if expected_image != release["image"]:
                raise ValueError("Release changed; refresh available updates")
            job = {"id": uuid.uuid4().hex, "version": release["version"],
                   "revision": release["revision"], "image": release["image"],
                   "created_at": int(time.time())}
            self.save(job, "queued")
            self.active = job["id"]
            threading.Thread(target=self.execute, args=(job,), daemon=True).start()
            return dict(job)

    def override_config(self, image):
        return {"services": {"sub2api": {
            "image": image,
            "environment": {"SUB2API_UPDATER_SOCKET": "/run/sub2api-updater/agent.sock"},
            "volumes": [{"type": "bind", "source": self.config["socket_dir"],
                         "target": "/run/sub2api-updater", "read_only": True}],
        }}}

    def inspect_container(self):
        raw = self.run(["docker", "inspect", self.config["container_name"], "--format",
                        '{{json .State}}'])
        return json.loads(raw)

    def verify_image(self, job):
        raw = self.run(["docker", "image", "inspect", job["image"], "--format", '{{json .Config.Labels}}'])
        labels = json.loads(raw)
        expected = {"org.opencontainers.image.source": f"https://github.com/{REPOSITORY}",
                    "org.opencontainers.image.revision": job["revision"],
                    "org.opencontainers.image.version": job["version"]}
        if any(labels.get(key) != value for key, value in expected.items()):
            raise ValueError("Image metadata does not match the published release")

    def backup(self, directory):
        for source in (self.base, self.directory / ".env", self.override):
            if source.exists():
                shutil.copy2(source, directory / source.name)
                os.chmod(directory / source.name, 0o600)
        # Preserve the actual binary too: legacy page updates can be newer than
        # the old image, so simply restarting that image is not a valid rollback.
        self.run(["docker", "cp", f'{self.config["container_name"]}:/app/sub2api', str(directory / "sub2api.previous")])
        database = directory / "database.dump"
        with database.open("wb") as stream:
            self.run(["docker", "exec", self.config["postgres_container"], "sh", "-c",
                      'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc'],
                     timeout=600, output=stream)
        if database.stat().st_size == 0:
            raise RuntimeError("Empty database backup")
        with database.open("rb") as stream:
            self.run(["docker", "exec", "-i", self.config["postgres_container"],
                      "pg_restore", "--list"], timeout=120, input_file=stream)
        self.run(["tar", "-czf", str(directory / "data.tar.gz"), "-C", str(self.directory), "data"], timeout=600)

    def verify_running(self, job):
        state = self.inspect_container()
        if not state.get("Running") or state.get("Health", {}).get("Status") != "healthy":
            raise RuntimeError("New application is not healthy")
        actual_image = self.run(["docker", "inspect", self.config["container_name"], "--format", '{{.Config.Image}}']).decode().strip()
        version = self.run(["docker", "exec", self.config["container_name"], "/app/sub2api", "-version"], combined=True).decode()
        if f'Sub2API {job["version"]} (commit: {job["revision"]},' not in version:
            raise RuntimeError("Running binary does not match the selected build")
        if actual_image != job["image"]:
            raise RuntimeError("Container is not running the requested image")
        self.verify_image(job)
        self.run(["docker", "exec", self.config["container_name"], "wget", "-q", "-O", "-", "http://127.0.0.1:8080/health"])

    def execute(self, job):
        directory = self.state_dir / job["id"]
        candidate = directory / "candidate-compose.json"
        stopped = False
        replacing = False
        try:
            if self.override.exists():
                existing = json.loads(self.override.read_text())
                old_image = existing.get("services", {}).get("sub2api", {}).get("image")
                if existing != self.override_config(old_image):
                    raise ValueError("Existing Compose override contains unmanaged settings")
            self.save(job, "pulling")
            self.run(["docker", "pull", job["image"]], timeout=1200)
            self.verify_image(job)
            atomic_json(candidate, self.override_config(job["image"]))
            self.run(self.compose(candidate) + ["config", "--quiet"])
            # The pull/config checks finish while the old application is live.
            old = self.run(["docker", "inspect", self.config["container_name"], "--format",
                            '{{.Config.Image}}']).decode().strip()
            job["previous_image"] = old
            self.save(job, "backing_up")
            stopped = True  # A timed-out stop may still have reached the daemon.
            self.run(["docker", "stop", "--time", "60", self.config["container_name"]], timeout=90)
            self.backup(directory)
            self.save(job, "recreating")
            atomic_json(self.override, self.override_config(job["image"]))
            replacing = True
            self.run(self.compose() + ["up", "-d", "--no-deps", "--wait", "--wait-timeout", "180", "sub2api"], timeout=240)
            self.save(job, "checking")
            self.verify_running(job)
            self.save(job, "succeeded")
        except Exception:
            logging.exception("Update %s failed at %s", job["id"], job.get("stage"))
            if stopped and not replacing:
                try:
                    self.run(["docker", "start", self.config["container_name"]])
                except Exception:
                    self.save(job, "needs_attention")
                else:
                    self.save(job, "failed")
            else:
                self.save(job, "needs_attention" if replacing else "failed")
        finally:
            with self.mutex:
                self.active = None


class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, format_string, *args):
        logging.info(format_string, *args)

    def respond(self, status, body):
        data = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        try:
            if self.path in ("/v1/release", "/v1/release?force=true"):
                value = self.server.updater.latest(force="?force" in self.path)
            elif self.path.startswith("/v1/jobs/"):
                value = self.server.updater.status(self.path[len("/v1/jobs/"):])
            else:
                self.respond(404, {"error": "Unknown endpoint"})
                return
            self.respond(200, value)
        except Exception:
            logging.exception("Read request failed")
            self.respond(503, {"error": "更新服务暂时无法获取版本或任务状态，请稍后重试"})

    def do_POST(self):
        try:
            if self.path != "/v1/jobs":
                self.respond(404, {"error": "Unknown endpoint"})
                return
            size = int(self.headers.get("Content-Length", "0"))
            if not 0 < size <= 1024:
                raise ValueError("Invalid request size")
            request = json.loads(self.rfile.read(size))
            job = self.server.updater.submit(request["image"])
            self.respond(202, job)
        except Exception:
            logging.exception("Update request rejected")
            self.respond(409, {"error": "无法提交更新，请刷新版本信息或检查之前的升级任务"})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--status", action="store_true")
    parser.add_argument("--verify-recovery", action="store_true", help="Verify the interrupted target is healthy before clearing its block")
    parser.add_argument("--install-latest", action="store_true", help="One-time operator bootstrap")
    args = parser.parse_args()
    os.umask(0o077)
    logging.basicConfig(level=logging.INFO)
    config = json.loads(Path(args.config).read_text())
    Path(config["state_dir"]).mkdir(parents=True, exist_ok=True, mode=0o700)
    if args.status:
        paths = list(Path(config["state_dir"]).glob("*/status.json"))
        print(max(paths, key=lambda p: p.stat().st_mtime).read_text() if paths else "null")
        return
    # Only one daemon/operator job may own the deployment at a time.
    with (Path(config["state_dir"]) / "agent.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        updater = Updater(config)
        directory = Path(config["socket_dir"])
        directory.mkdir(parents=True, exist_ok=True)
        os.chown(directory, 0, config.get("socket_gid", 1000))
        os.chmod(directory, 0o750)
        if args.verify_recovery:
            job = updater.status()
            if not job or job["stage"] != "needs_attention":
                raise SystemExit("No interrupted update to verify")
            updater.verify_running(job)
            updater.save(job, "succeeded")
            print(json.dumps(job, ensure_ascii=False))
            return
        if args.install_latest:
            previous = updater.status()
            if previous and previous["stage"] == "needs_attention":
                raise SystemExit("Resolve the interrupted update before installing another build")
            release = updater.latest(force=True)
            job = {"id": uuid.uuid4().hex, "version": release["version"],
                   "revision": release["revision"], "image": release["image"], "created_at": int(time.time())}
            updater.save(job, "queued")
            updater.execute(job)
            print(json.dumps(job, ensure_ascii=False))
            raise SystemExit(0 if job["stage"] == "succeeded" else 1)
        socket_path = directory / "agent.sock"
        socket_path.unlink(missing_ok=True)
        with Server(str(socket_path), Handler) as server:
            server.updater = updater
            os.chown(socket_path, 0, config.get("socket_gid", 1000))
            os.chmod(socket_path, 0o660)
            server.serve_forever()


if __name__ == "__main__":
    main()
