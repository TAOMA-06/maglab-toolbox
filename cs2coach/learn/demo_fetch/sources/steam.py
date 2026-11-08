"""Steam official matchmaking (官匹/优先) share-code source.

Valve's Web API can iterate share codes, but the actual replay URL has to be
resolved through the Game Coordinator.  This source stays resolver-agnostic:
pass a command that prints the demo URL for a share code, e.g.

    python3 -m demo_fetch steam \\
      --share-code CSGO-xxxxx-xxxxx-xxxxx-xxxxx-xxxxx \\
      --resolver-cmd '/path/to/resolver {code}'

A resolver such as ``cs2-demo-fetcher`` / ``cs-demo-manager`` can be wrapped in
a tiny script that prints one URL to stdout.
"""

from __future__ import annotations

import re
import shlex
import subprocess
from typing import Iterable, Optional

from ..errors import SourceError
from ..pipeline import Job
from ..util import safe_name

SHARE_CODE_CHARS = "ABCDEFGHJKLMNOPQRSTUVWXYZabcdefhijkmnopqrstuvwxyz23456789"
BITMASK64 = 2**64 - 1
URL_RE = re.compile(
    r"https?://[^\s\"'<>]+?\.dem(?:\.(?:bz2|gz|zst))?(?:\?[^\s\"'<>]*)?", re.IGNORECASE
)


def _swap_endianness(number: int) -> int:
    result = 0
    for shift in range(0, 144, 8):
        result = (result << 8) + ((number >> shift) & 0xFF)
    return result


def decode_share_code(code: str) -> dict[str, int]:
    """Decode a CS2/CS:GO match share code into ids + token."""

    cleaned = str(code or "").strip()
    if not re.match(r"^(CSGO)?(-?[%s]{5}){5}$" % SHARE_CODE_CHARS, cleaned):
        raise SourceError(f"非法 share code: {code!r}")
    compact = re.sub(r"CSGO\-|\-", "", cleaned)[::-1]
    value = 0
    for character in compact:
        value = value * len(SHARE_CODE_CHARS) + SHARE_CODE_CHARS.index(character)
    value = _swap_endianness(value)
    return {
        "matchid": value & BITMASK64,
        "outcomeid": (value >> 64) & BITMASK64,
        "token": (value >> 128) & 0xFFFF,
    }


def resolve_with_command(
    share_code: str, command: str, *, timeout: float = 60.0
) -> str:
    parts = shlex.split(command.replace("{code}", share_code))
    if not parts:
        raise SourceError("--resolver-cmd 为空")
    try:
        completed = subprocess.run(
            parts, capture_output=True, text=True, timeout=timeout
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise SourceError(f"resolver 执行失败: {error}") from error
    output = "\n".join(part for part in (completed.stdout, completed.stderr) if part)
    match = URL_RE.search(output)
    if not match:
        raise SourceError(
            f"resolver 没有输出 demo URL（退出码 {completed.returncode}）: "
            f"{output.strip()[-500:]}"
        )
    return match.group(0)


def jobs(
    *,
    share_codes: Iterable[str],
    resolver_cmd: Optional[str] = None,
    limit: Optional[int] = None,
    timeout: float = 60.0,
) -> list[Job]:
    codes = [str(code).strip() for code in share_codes if str(code).strip()]
    if not codes:
        raise SourceError("至少需要一个 --share-code")
    if not resolver_cmd:
        raise SourceError(
            "Steam 官匹 demo URL 需要 Game Coordinator 解析。请用 --resolver-cmd 传入一个"
            "接收 {code} 并打印 demo URL 的命令（例如 cs2-demo-fetcher / cs-demo-manager 的包装脚本）。"
        )
    jobs_out: list[Job] = []
    for code in codes:
        decoded = decode_share_code(code)
        url = resolve_with_command(code, resolver_cmd, timeout=timeout)
        match_id = str(decoded["matchid"])
        jobs_out.append(
            Job(
                source="steam",
                match_id=match_id,
                url=url,
                suggested_name=safe_name(f"steam_{match_id}", max_len=100),
                meta={
                    "share_code": "<redacted>",
                    "matchid": str(decoded["matchid"]),
                    "outcomeid": str(decoded["outcomeid"]),
                    "token": "<redacted>",
                },
            )
        )
    return jobs_out[:limit] if limit else jobs_out
