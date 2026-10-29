"""Render the deployment example with synthetic values, without starting services."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent


class NextcloudConfigTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("docker-compose"), "docker-compose renderer is required")
    def test_php_environment_is_forwarded_by_compose(self):
        fixture = dict(line.split("=", 1) for line in (ROOT / ".env.example").read_text().splitlines() if line and not line.startswith("#"))
        fixture.update(S3_BUCKET="fixture-bucket", S3_HOST="storage.invalid", S3_KEY="fixture-key", S3_SECRET="fixture-secret", REDIS_PASSWORD="fixture-redis")
        with tempfile.TemporaryDirectory(prefix="nextcloud-config-") as tmp:
            envfile = Path(tmp) / ".env"
            envfile.write_text("\n".join(f"{key}={value}" for key, value in fixture.items()))
            result = subprocess.run(
                [shutil.which("docker-compose"), "--env-file", str(envfile), "-f", str(ROOT / "docker-compose.yml"), "config", "--format", "json"],
                env={"PATH": os.environ.get("PATH", os.defpath)}, capture_output=True, text=True,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        forwarded = json.loads(result.stdout)["services"]["nextcloud"]["environment"]
        reads = set(re.findall(r"getenv\('([^']+)'\)", (ROOT / "objectstore.config.php.example").read_text()))
        self.assertEqual(reads - forwarded.keys(), set(), "PHP reads variables absent from the actual container environment")
        for key in reads:
            self.assertEqual(str(forwarded[key]), fixture[key], key)


if __name__ == "__main__":
    unittest.main()
