"""Check Linux sources and build and inspect a temporary Debian package."""

from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parent.parent

for source in sorted((ROOT / "linux").rglob("*.py")):
    compile(source.read_bytes(), str(source), "exec")

with tempfile.TemporaryDirectory(prefix="mountbridge-check-") as output:
    subprocess.run([sys.executable, str(ROOT / "linux/package.py"), "--output", output], check=True)
    packages = list(Path(output).glob("*.deb"))
    if len(packages) != 1:
        raise SystemExit("Expected exactly one Debian package")
    for field, expected in (("Package", "salanghe-mountbridge"), ("Architecture", "all")):
        actual = subprocess.check_output(["dpkg-deb", "--field", packages[0], field], text=True).strip()
        if actual != expected:
            raise SystemExit(f"Invalid Debian {field}: {actual}")
