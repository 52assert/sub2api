import json
import http.client
import socket
import threading
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from agent import IMAGE_PREFIX, REPOSITORY, Updater, validate_manifest, Server, Handler

RELEASE = {"schema": 1, "version": "0.2.3", "revision": "a" * 40,
           "image": IMAGE_PREFIX + "b" * 64}


class FakeUpdater(Updater):
    def __init__(self, config, failure=None):
        super().__init__(config)
        self.commands = []
        self.failure = failure
        self.old_running = True
        self.new_running = False

    def latest(self, force=False):
        return dict(RELEASE)

    def run(self, arguments, timeout=120, output=None, input_file=None, combined=False):
        self.commands.append(arguments)
        if arguments[:2] == ["docker", "pull"] and self.failure == "pull":
            raise RuntimeError("Cannot pull")
        if arguments[:3] == ["docker", "image", "inspect"]:
            return json.dumps({"org.opencontainers.image.source": f"https://github.com/{REPOSITORY}",
                               "org.opencontainers.image.version": RELEASE["version"],
                               "org.opencontainers.image.revision": "wrong" if self.failure == "metadata" else RELEASE["revision"]}).encode()
        if arguments[:2] == ["docker", "inspect"]:
            return (RELEASE["image"] if self.new_running else "old-image").encode()
        if arguments[:2] == ["docker", "stop"]:
            self.old_running = False
        if arguments[:2] == ["docker", "start"]:
            self.old_running = True
        if arguments[:2] == ["docker", "compose"] and "up" in arguments:
            self.new_running = True
            if self.failure == "launch":
                raise RuntimeError("New version did not start")
        if arguments[-1] == "-version":
            revision = "wrong" if self.failure == "binary" else RELEASE["revision"]
            return f'Sub2API {RELEASE["version"]} (commit: {revision}, built: now)'.encode()
        return b""

    def backup(self, directory):
        if self.failure == "backup":
            raise RuntimeError("Backup failed")
        (directory / "database.dump").write_text("test backup")

    def inspect_container(self):
        return {"Running": True, "Health": {"Status": "unhealthy" if self.failure == "health" else "healthy"}}


class AgentTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        self.config = {"deployment_dir": str(root), "state_dir": str(root / "state"),
                       "socket_dir": str(root / "run"), "container_name": "sub2api",
                       "postgres_container": "postgres", "project_name": "sub2api-test"}

    def execute(self, failure=None):
        updater = FakeUpdater(self.config, failure)
        job = {"id": "c" * 32, **RELEASE}
        updater.save(job, "queued")
        with self.assertLogs(level="ERROR") if failure else patch("logging.exception"):
            updater.execute(job)
        return updater, job

    def test_success_pins_digest_and_only_recreates_application(self):
        updater, job = self.execute()
        self.assertEqual(job["stage"], "succeeded")
        effective = json.loads(updater.override.read_text())
        self.assertEqual(effective["services"]["sub2api"]["image"], RELEASE["image"])
        self.assertEqual(set(effective["services"]), {"sub2api"})
        up = next(cmd for cmd in updater.commands if "up" in cmd)
        self.assertIn("--no-deps", up)
        self.assertIn("--wait", up)
        self.assertEqual(up[-1], "sub2api")
        self.assertEqual(updater.status()["id"], job["id"])

    def test_pull_or_metadata_failure_does_not_stop_original(self):
        for failure in ("pull", "metadata"):
            with self.subTest(failure=failure):
                updater, job = self.execute(failure)
                self.assertEqual(job["stage"], "failed")
                self.assertTrue(updater.old_running)
                self.assertFalse(any(cmd[:2] == ["docker", "stop"] for cmd in updater.commands))
                self.assertFalse(updater.override.exists())

    def test_backup_failure_restarts_original_container(self):
        updater, job = self.execute("backup")
        self.assertEqual(job["stage"], "failed")
        self.assertTrue(updater.old_running)
        self.assertFalse(updater.new_running)

    def test_failed_new_build_never_implicitly_restores_database(self):
        for failure in ("launch", "health", "binary"):
            with self.subTest(failure=failure):
                updater, job = self.execute(failure)
                self.assertEqual(job["stage"], "needs_attention")
                self.assertFalse(any("pg_restore" in cmd for cmd in updater.commands))
                self.assertTrue((updater.state_dir / job["id"] / "database.dump").exists())

    def test_interrupted_job_is_durable_and_blocks_new_updates(self):
        updater = FakeUpdater(self.config)
        updater.save({"id": "d" * 32, **RELEASE}, "recreating")
        restarted = FakeUpdater(self.config)
        self.assertEqual(restarted.status()["stage"], "needs_attention")
        with self.assertRaises(ValueError):
            restarted.submit(RELEASE["image"])

    def test_duplicate_request_reuses_active_job(self):
        updater = FakeUpdater(self.config)
        updater.active = "e" * 32
        updater.save({"id": updater.active, **RELEASE}, "pulling")
        self.assertEqual(updater.submit(RELEASE["image"])["id"], updater.active)

    def test_stale_release_rejected_before_deployment(self):
        updater = FakeUpdater(self.config)
        with self.assertRaises(ValueError):
            updater.submit(IMAGE_PREFIX + "f" * 64)
        self.assertEqual(updater.commands, [])

    def test_only_this_repository_and_immutable_images_are_accepted(self):
        self.assertEqual(validate_manifest(dict(RELEASE)), RELEASE)
        for image in ("alpine:latest", "ghcr.io/52assert/sub2api:custom", IMAGE_PREFIX + "z" * 64,
                      "ghcr.io/other/sub2api@sha256:" + "a" * 64):
            with self.subTest(image=image), self.assertRaises(ValueError):
                validate_manifest({**RELEASE, "image": image})
        with self.assertRaises(ValueError):
            FakeUpdater(self.config).status("../../config")

    def test_unmanaged_override_is_never_overwritten(self):
        override = Path(self.config["deployment_dir"]) / "docker-compose.override.yml"
        original = '{"services":{"sub2api":{"ports":["9090:8080"]}}}'
        override.write_text(original)
        updater = FakeUpdater(self.config)
        job = {"id": "c" * 32, **RELEASE}
        updater.save(job, "queued")
        with self.assertLogs(level="ERROR"):
            updater.execute(job)
        self.assertEqual(job["stage"], "failed")
        self.assertEqual(override.read_text(), original)
        self.assertEqual(updater.commands, [])

    def test_unix_api_reports_durable_status_and_rejects_bad_requests(self):
        updater = FakeUpdater(self.config)
        job = {"id": "c" * 32, **RELEASE}
        updater.save(job, "succeeded")
        path = str(Path(self.temporary.name) / "api.sock")
        with Server(path, Handler) as server:
            server.updater = updater
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                for method, endpoint, body, expected in (
                    ("GET", "/v1/jobs/latest", None, 200),
                    ("GET", "/v1/release", None, 200),
                    ("POST", "/v1/jobs", b"{}", 409),
                    ("GET", "/unknown", None, 404),
                ):
                    connection = http.client.HTTPConnection("localhost")
                    connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                    connection.sock.connect(path)
                    with patch("logging.exception"):
                        connection.request(method, endpoint, body=body)
                        response = connection.getresponse()
                        value = json.loads(response.read())
                    self.assertEqual(response.status, expected)
                    if endpoint == "/v1/jobs/latest":
                        self.assertEqual(value["stage"], "succeeded")
                    connection.close()
            finally:
                server.shutdown()
                thread.join()

    def test_backup_uses_consistent_dump_and_preserves_actual_binary(self):
        updater = FakeUpdater(self.config)
        directory = Path(self.config["state_dir"]) / "backup-test"
        directory.mkdir()
        original_run = updater.run

        def run(arguments, **kwargs):
            if kwargs.get("output"):
                kwargs["output"].write(b"PGDMP-test")
            return original_run(arguments, **kwargs)

        updater.run = run
        Updater.backup(updater, directory)
        self.assertTrue(any(cmd[:3] == ["docker", "cp", "sub2api:/app/sub2api"] for cmd in updater.commands))
        self.assertTrue(any("pg_restore" in cmd and "--list" in cmd for cmd in updater.commands))


if __name__ == "__main__":
    unittest.main()
