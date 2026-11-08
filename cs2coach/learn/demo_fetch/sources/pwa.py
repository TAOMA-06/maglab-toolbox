"""Perfect World Arena (PWA / 完美世界电竞) source.

PWA signs demo requests with a compiled native helper, so this source is an
optional integration with the ``cs-demo-downloader`` package rather than a
reimplementation.  Install it in the Python environment running this tool:

    uv pip install --python learn/.venv/bin/python cs-demo-downloader

The package ships a signer for Python 3.9-3.12 on macOS/Linux/Windows; use a
compatible interpreter if your default Python is newer.
"""

from __future__ import annotations

import os
from typing import Iterable, Optional

from ..errors import SourceError
from ..pipeline import Job


def _import_downloader():
    try:
        from cs_demo_downloader.core.downloader_pwa import (  # type: ignore
            build_download_headers,
            get_all_demo_urls,
        )
    except ImportError as error:
        raise SourceError(
            "PWA 下载需要可选依赖 cs-demo-downloader（含 PWA 签名器）。"
            " 安装: uv pip install --python learn/.venv/bin/python cs-demo-downloader"
        ) from error
    return build_download_headers, get_all_demo_urls


def jobs(
    *,
    steamids: Iterable[str] = (),
    token: Optional[str] = None,
    limit: int = 20,
    quiet: bool = False,
) -> list[Job]:
    token = token or os.environ.get("PWA_ACCESS_TOKEN", "")
    if not token:
        raise SourceError(
            "PWA 需要 access token。传 --token，或设置环境变量 PWA_ACCESS_TOKEN。"
            " 获取方式见 learn/demo_fetch/README.md。"
        )
    steamids = [str(item).strip() for item in steamids if str(item).strip()]
    if not steamids:
        raise SourceError("PWA 需要 --steamid（SteamID64）")
    build_download_headers, get_all_demo_urls = _import_downloader()

    jobs_out: list[Job] = []
    for steamid in steamids:
        try:
            demo_urls = get_all_demo_urls(steamid, token, size=limit)
        except Exception as error:  # noqa: BLE001 - third-party error surface
            raise SourceError(
                f"PWA 获取 {steamid} 的 demo 列表失败: {error}"
            ) from error
        if not isinstance(demo_urls, dict):
            demo_urls = dict(demo_urls or {})
        try:
            headers = build_download_headers(steamid)
        except Exception as error:  # noqa: BLE001
            raise SourceError(f"PWA 构造下载签名失败: {error}") from error
        if not quiet:
            print(f"  PWA {steamid}: {len(demo_urls)} 场候选", flush=True)
        for match_id, url in demo_urls.items():
            if not url:
                continue
            jobs_out.append(
                Job(
                    source="pwa",
                    match_id=str(match_id),
                    url=str(url),
                    headers={
                        str(key): str(value) for key, value in (headers or {}).items()
                    },
                    account=steamid,
                    suggested_name=f"pwa_{match_id}",
                    meta={"steamid": steamid, "match_id": str(match_id)},
                )
            )
    return jobs_out[: max(1, limit)]
