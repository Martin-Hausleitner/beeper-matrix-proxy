"""Local backup contract tests; all provider commands are synthetic stubs."""
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parent


class BackupContractTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="backup-contract-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        restic = self.bin / "restic"
        restic.write_text("""#!/usr/bin/env python3
import json, os, pathlib, shutil, sys
root = pathlib.Path(os.environ['FIXTURE_ROOT'])
if sys.argv[1] == 'backup':
    src = pathlib.Path(sys.argv[2])
    (root / 'receipt.json').write_text(json.dumps({'staging': str(src), 'mode': src.stat().st_mode & 0o777}))
    shutil.copytree(src, root / 'snapshot')
elif sys.argv[1] == 'restore':
    target = pathlib.Path(sys.argv[sys.argv.index('--target') + 1])
    shutil.copytree(root / 'snapshot', target / 'var/tmp/matrix-stack-backup/run')
""")
        restic.chmod(0o700)
        for name, body in {"pg_dump": "printf 'synthetic database dump\\n'", "aws": "exit 99"}.items():
            stub = self.bin / name
            stub.write_text("#!/bin/sh\n" + body + "\n")
            stub.chmod(0o700)
        absent = str(self.root / "absent")
        self.env = {
            "PATH": str(self.bin) + os.pathsep + os.defpath,
            "FIXTURE_ROOT": str(self.root),
            "RESTIC_REPOSITORY": "synthetic-only",
            "RESTIC_PASSWORD": "synthetic-only",
            "BACKUP_STAGING_ROOT": str(self.root / "staging"),
            "BACKUP_KEEP_STAGING": "true",
            "RESTIC_FORGET": "false",
            "POSTGRES_HOST": "synthetic-only",
            "POSTGRES_DATABASES": "fixture",
            "MDAD_BASE_PATH": absent,
            "NEXTCLOUD_COMPOSE_DIR": absent,
            "BEEPER_PROXY_DIR": absent,
            "MATRIX_ARCHIVE_DIR": absent,
        }

    def run_script(self, name):
        return subprocess.run(
            ["bash", str(SCRIPTS / name), str(self.root / "no.env")],
            env=self.env, capture_output=True, text=True,
            # Deliberately permissive caller: the script must protect plaintext.
            umask=0o022,
        )

    def backup(self):
        result = self.run_script("run-backup.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads((self.root / "receipt.json").read_text())

    def test_checksums_validate_and_do_not_include_themselves(self):
        self.backup()
        snapshot = self.root / "snapshot"
        checksums = (snapshot / "checksums/SHA256SUMS").read_text()
        self.assertNotIn("checksums/SHA256SUMS", checksums)
        result = subprocess.run(["shasum", "-a", "256", "-c", "checksums/SHA256SUMS"], cwd=snapshot, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_restore_discovers_absolute_staging_tree(self):
        self.backup()
        # Independently valid archive isolates discovery from checksum generation.
        snapshot = self.root / "snapshot"
        checksum = subprocess.check_output(["shasum", "-a", "256", "postgres/fixture.dump"], cwd=snapshot, text=True)
        (snapshot / "checksums/SHA256SUMS").write_text(checksum)
        result = self.run_script("restore-check.sh")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("checksums valid", result.stdout)

    def test_staging_and_dump_are_private_under_permissive_umask(self):
        receipt = self.backup()
        self.assertEqual(receipt["mode"], 0o700)
        dump = Path(receipt["staging"]) / "postgres/fixture.dump"
        self.assertEqual(stat.S_IMODE(dump.stat().st_mode), 0o600)


if __name__ == "__main__":
    unittest.main()
