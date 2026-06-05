from __future__ import annotations

import argparse
import io
import struct
from pathlib import Path

from PIL import Image


WINDOWS_ICON_SIZES = (16, 32, 48, 64, 128, 256)


def png_bytes(source: Image.Image, size: int) -> bytes:
    resized = source.convert("RGBA").resize((size, size), Image.Resampling.LANCZOS)
    out = io.BytesIO()
    resized.save(out, format="PNG")
    return out.getvalue()


def build_icon(source_path: Path, destination_path: Path) -> Path:
    with Image.open(source_path) as source:
        entries = [(size, png_bytes(source, size)) for size in WINDOWS_ICON_SIZES]

    header_size = 6 + len(entries) * 16
    offset = header_size
    directory = bytearray()
    payload = bytearray()

    for size, data in entries:
        width = 0 if size == 256 else size
        directory.extend(
            struct.pack(
                "<BBBBHHII",
                width,
                width,
                0,
                0,
                0,
                32,
                len(data),
                offset,
            )
        )
        payload.extend(data)
        offset += len(data)

    destination_path.parent.mkdir(parents=True, exist_ok=True)
    destination_path.write_bytes(
        struct.pack("<HHH", 0, 1, len(entries)) + bytes(directory) + bytes(payload)
    )
    return destination_path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build packaging/windows/icon.ico from the canonical PNG artwork."
    )
    parser.add_argument("source", type=Path)
    parser.add_argument("destination", type=Path)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    print(build_icon(args.source, args.destination))


if __name__ == "__main__":
    main()
