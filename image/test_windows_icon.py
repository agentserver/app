import io
import struct
import unittest
from pathlib import Path

from PIL import Image, ImageChops


ROOT = Path(__file__).resolve().parents[1]
SOURCE_ICON = ROOT / "image" / "icon.png"
WINDOWS_ICON = ROOT / "packaging" / "windows" / "icon.ico"


def png_entry(icon_path: Path, size: int) -> bytes:
    data = icon_path.read_bytes()
    if data[:4] != b"\x00\x00\x01\x00":
        raise AssertionError("not a Windows ICO file")
    count = struct.unpack_from("<H", data, 4)[0]
    for index in range(count):
        off = 6 + index * 16
        width, height, _, _, _, _, entry_size, entry_offset = struct.unpack_from(
            "<BBBBHHII", data, off
        )
        width = width or 256
        height = height or 256
        if width == size and height == size:
            entry = data[entry_offset : entry_offset + entry_size]
            if not entry.startswith(b"\x89PNG\r\n\x1a\n"):
                raise AssertionError(f"{size}x{size} icon entry is not PNG")
            return entry
    raise AssertionError(f"missing {size}x{size} icon entry")


class WindowsIconTest(unittest.TestCase):
    def test_ico_uses_source_icon_artwork(self):
        with Image.open(SOURCE_ICON) as source:
            expected = source.convert("RGBA").resize((256, 256), Image.Resampling.LANCZOS)

        actual = Image.open(io.BytesIO(png_entry(WINDOWS_ICON, 256))).convert("RGBA")

        diff = ImageChops.difference(actual, expected)
        if diff.getbbox() is not None:
            self.fail("packaging/windows/icon.ico 256px entry does not match image/icon.png")


if __name__ == "__main__":
    unittest.main()
