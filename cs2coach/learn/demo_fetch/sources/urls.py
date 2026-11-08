"""Manual URL source.

Useful when a platform gives you a direct archive/demo URL (browser download
link, private CDN, team archive, ...).  The source only deals with direct file
URLs; it does not scrape arbitrary web pages.
"""

from __future__ import annotations

import hashlib
from pathlib import Path
from typing import Iterable, Optional
from urllib.parse import unquote, urlsplit

from ..errors import SourceError
from ..pipeline import Job
from ..util import safe_name, strip_demo_suffixes


def read_url_file(path: str | Path) -> list[str]:
    file_path = Path(path).expanduser()
    if not file_path.is_file():
        raise SourceError(f"URL 文件不存在: {file_path}")
    urls: list[str] = []
    for line in file_path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        urls.append(line)
    return urls


def jobs(
    *,
    urls: Iterable[str] = (),
    url_file: Optional[str] = None,
    name_prefix: str = "url",
) -> list[Job]:
    all_urls = [str(url).strip() for url in urls if str(url).strip()]
    if url_file:
        all_urls.extend(read_url_file(url_file))
    if not all_urls:
        raise SourceError("至少需要一个 URL 或 --file")
    entries: list[tuple[str, str, str]] = []
    seen: set[str] = set()
    for index, url in enumerate(all_urls, start=1):
        if url in seen:
            continue
        seen.add(url)
        path = unquote(urlsplit(url).path)
        stem = strip_demo_suffixes(Path(path).name) or f"demo_{index}"
        url_hash = hashlib.sha1(url.encode("utf-8")).hexdigest()[:12]
        entries.append((url, stem, url_hash))

    stem_counts: dict[str, int] = {}
    for _, stem, _ in entries:
        stem_counts[stem] = stem_counts.get(stem, 0) + 1

    jobs_out: list[Job] = []
    for url, stem, url_hash in entries:
        name = f"{name_prefix}_{stem}"
        if stem_counts[stem] > 1:
            # Distinct URLs with the same basename must not overwrite each
            # other's output file.
            name = f"{name}_{url_hash[:8]}"
        jobs_out.append(
            Job(
                source="url",
                match_id=safe_name(stem, max_len=80),
                demo_id=url_hash,
                url=url,
                suggested_name=safe_name(name, max_len=100),
                meta={"url": url},
            )
        )
    return jobs_out
