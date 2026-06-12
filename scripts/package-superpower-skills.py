#!/usr/bin/env python3
import gzip
import os
import stat
import sys
import tarfile
from pathlib import Path


SKIP_DIRS = {
    ".git",
    ".mypy_cache",
    ".pytest_cache",
    "__pycache__",
    "node_modules",
}

SKIP_FILES = {
    ".DS_Store",
    ".env",
}


def should_skip(path: Path) -> bool:
    if path.name in SKIP_DIRS or path.name in SKIP_FILES:
        return True
    return any(part in SKIP_DIRS for part in path.parts)


def add_path(tf: tarfile.TarFile, src: Path, arcname: str) -> None:
    st = src.stat()
    info = tarfile.TarInfo(arcname)
    info.mtime = 0
    info.uid = 0
    info.gid = 0
    info.uname = ""
    info.gname = ""
    if src.is_dir():
        info.type = tarfile.DIRTYPE
        info.mode = 0o755
        tf.addfile(info)
        return
    info.size = st.st_size
    info.mode = stat.S_IMODE(st.st_mode) or 0o644
    with src.open("rb") as f:
        tf.addfile(info, f)


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: package-superpower-skills.py <output.tar.gz>", file=sys.stderr)
        return 2
    default_source = Path(__file__).resolve().parents[1] / "packaging" / "superpowers" / "skills"
    source = Path(os.environ.get("SUPERPOWER_SKILLS_DIR", default_source)).expanduser()
    output = Path(sys.argv[1])
    if not source.is_dir():
        print(f"ERROR: superpower skills dir not found: {source}", file=sys.stderr)
        return 2

    skill_dirs = [
        p for p in sorted(source.iterdir(), key=lambda x: x.name)
        if p.is_dir() and p.name != ".system" and (p / "SKILL.md").is_file()
    ]
    if not skill_dirs:
        print(f"ERROR: no superpower skills found in {source}", file=sys.stderr)
        return 2

    output.parent.mkdir(parents=True, exist_ok=True)
    tmp = output.with_suffix(output.suffix + ".part")
    if tmp.exists():
        tmp.unlink()

    count = 0
    with tmp.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz:
            with tarfile.open(fileobj=gz, mode="w") as tf:
                for skill_dir in skill_dirs:
                    entries = [skill_dir]
                    entries.extend(
                        p for p in sorted(skill_dir.rglob("*"), key=lambda x: x.relative_to(source).as_posix())
                        if not p.is_symlink() and not should_skip(p.relative_to(source))
                    )
                    for path in entries:
                        rel = path.relative_to(source).as_posix()
                        add_path(tf, path, rel)
                        count += 1
    tmp.replace(output)
    print(f"built {output} from {source} ({len(skill_dirs)} skills, {count} entries)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
