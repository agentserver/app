#!/usr/bin/env python3
import gzip
import io
import os
import sys
import tarfile
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PROMPT_SOURCE = ROOT / "packaging/windows/driver-codex-prompts/prompts-codex/AGENTS.md"
ARCHIVE_ENTRY = "prompts-codex/AGENTS.md"

REQUIRED = (
    "# Agentserver Driver Workspace",
    "Use the `multiagent` skill",
    "`mcp_servers.driver`",
    "use the installed Superpower skills",
)
FORBIDDEN = (
    "# Multi-Agent Driver",
    "## Core tools",
    "mcp__driver__list_agents",
    "## Permissions skill",
)


def checked_prompt() -> bytes:
    data = PROMPT_SOURCE.read_bytes()
    text = data.decode("utf-8")
    missing = [token for token in REQUIRED if token not in text]
    if missing:
        raise SystemExit(f"{PROMPT_SOURCE} missing required prompt text: {missing}")
    forbidden = [token for token in FORBIDDEN if token in text]
    if forbidden:
        raise SystemExit(f"{PROMPT_SOURCE} still contains verbose Loom prompt text: {forbidden}")
    if not data.endswith(b"\n"):
        data += b"\n"
    return data


def tar_info(name: str, mode: int, size: int = 0, typeflag: bytes = tarfile.REGTYPE) -> tarfile.TarInfo:
    info = tarfile.TarInfo(name)
    info.mode = mode
    info.size = size
    info.type = typeflag
    info.mtime = 0
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    return info


def write_archive(out_path: Path, data: bytes) -> None:
    out_path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = out_path.with_name(out_path.name + ".part")
    try:
        with tmp_path.open("wb") as raw:
            with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz:
                with tarfile.open(fileobj=gz, mode="w", format=tarfile.GNU_FORMAT) as tf:
                    tf.addfile(tar_info("prompts-codex", 0o755, typeflag=tarfile.DIRTYPE))
                    tf.addfile(tar_info(ARCHIVE_ENTRY, 0o644, size=len(data)), io.BytesIO(data))
        os.replace(tmp_path, out_path)
    finally:
        tmp_path.unlink(missing_ok=True)


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: package-driver-codex-prompts.py OUT.tar.gz", file=sys.stderr)
        return 2
    out_path = Path(argv[1])
    data = checked_prompt()
    write_archive(out_path, data)
    print(f"{out_path}: packaged {ARCHIVE_ENTRY} ({len(data)} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
