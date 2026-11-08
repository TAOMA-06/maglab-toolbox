"""5EPlay source (Chinese ladder / 天梯).

The public web API used by the 5E website is enough to list a player's recent
matches and obtain a demo URL.  Demos are kept for about 15 days on 5E; older
matches return an empty ``demo_url``.
"""

from __future__ import annotations

import time
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Any, Iterable, Optional
from urllib.parse import urlsplit

from ..errors import SourceError
from ..net import HttpError, post_json, request_json
from ..pipeline import Job
from ..util import redact_url

GATE = "https://gate.5eplay.com"
ID_TRANSFER_URL = f"{GATE}/userinterface/http/v1/userinterface/idTransfer"
MATCH_LIST_URL = f"{GATE}/crane/http/api/data/match/list"
MATCH_DETAIL_URL = f"{GATE}/crane/http/api/data/match"
HEADERS = {
    "User-Agent": (
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
        "AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/120.0.0.0 Safari/537.36"
    ),
    "Referer": "https://arena.5eplay.com/",
    "Origin": "https://arena.5eplay.com",
}
CHINA_TZ = timezone(timedelta(hours=8))


@dataclass
class FiveEMatch:
    match_id: str
    demo_url: str
    map_name: str
    started_at: int
    ended_at: int
    detail: dict[str, Any]

    @property
    def date(self) -> str:
        # 5E is a Chinese platform; its web UI shows UTC+8 dates.  Using UTC
        # would put a 03:40 match on the previous day.
        if not self.started_at:
            return ""
        return datetime.fromtimestamp(self.started_at, tz=CHINA_TZ).strftime("%Y-%m-%d")

    @property
    def map_label(self) -> str:
        return self.map_name.replace("de_", "") if self.map_name else ""


def parse_user_id(value: str) -> str:
    """Accept a 5E id or a profile URL and return the id segment."""

    text = str(value or "").strip()
    if not text:
        raise SourceError("5E user id 为空")
    if "/" in text or text.startswith("http"):
        parts = [part for part in urlsplit(text).path.split("/") if part]
        if parts:
            return parts[-1]
    return text


def get_uuid(
    user_id: str, *, timeout: float = 20.0, proxy: Optional[str] = None
) -> str:
    user_id = parse_user_id(user_id)
    payload = {"trans": {"domain": user_id}}
    try:
        data = post_json(
            ID_TRANSFER_URL,
            payload,
            headers=HEADERS,
            timeout=timeout,
            proxy=proxy,
        )
    except HttpError as error:
        raise SourceError(f"5E 用户 ID 转换失败: {error}") from error
    uuid = (data.get("data") or {}).get("uuid") if isinstance(data, dict) else None
    if not uuid:
        raise SourceError(
            f"5E 没有找到用户 {user_id!r}。请使用个人主页 URL 最后一段，"
            "例如 https://arena.5eplay.com/data/player/11814738gjdwn7 -> 11814738gjdwn7"
        )
    return str(uuid)


def list_matches(
    uuid: str,
    *,
    days: int = 14,
    limit: int = 20,
    timeout: float = 20.0,
    proxy: Optional[str] = None,
) -> list[dict[str, Any]]:
    now = int(time.time())
    start = now - max(1, days) * 86400
    params = (
        f"match_type=-1&page=1&date=0&start_time={start}&end_time={now}"
        f"&uuid={uuid}&limit={limit}&cs_type=0"
    )
    url = f"{MATCH_LIST_URL}?{params}"
    try:
        data = request_json(url, headers=HEADERS, timeout=timeout, proxy=proxy)
    except HttpError as error:
        raise SourceError(f"5E 比赛列表获取失败: {error}") from error
    if not isinstance(data, dict):
        return []
    if data.get("code") not in (0, None):
        raise SourceError(
            f"5E 比赛列表返回错误: {data.get('message') or data.get('code')}"
        )
    records = data.get("data") or []
    return [record for record in records if isinstance(record, dict)]


def get_match_detail(
    match_id: str,
    *,
    timeout: float = 20.0,
    proxy: Optional[str] = None,
) -> dict[str, Any]:
    url = f"{MATCH_DETAIL_URL}/{match_id}"
    try:
        data = request_json(url, headers=HEADERS, timeout=timeout, proxy=proxy)
    except HttpError as error:
        raise SourceError(f"5E 比赛详情获取失败 ({match_id}): {error}") from error
    if not isinstance(data, dict):
        return {}
    if data.get("code") not in (0, None):
        return {}
    detail = data.get("data")
    return detail if isinstance(detail, dict) else {}


def _to_int(value: Any) -> int:
    try:
        return int(value or 0)
    except (TypeError, ValueError):
        return 0


def match_from_detail(match_id: str, detail: dict[str, Any]) -> Optional[FiveEMatch]:
    main = detail.get("main")
    if not isinstance(main, dict):
        return None
    demo_url = str(main.get("demo_url") or "").strip()
    if demo_url.startswith("//"):
        demo_url = "https:" + demo_url
    elif demo_url.startswith("/"):
        demo_url = "https://arena.5eplay.com" + demo_url
    started_at = _to_int(main.get("start_time"))
    ended_at = _to_int(main.get("end_time"))
    return FiveEMatch(
        match_id=match_id,
        demo_url=demo_url,
        map_name=str(main.get("map") or ""),
        started_at=started_at,
        ended_at=ended_at,
        detail=detail,
    )


def jobs(
    *,
    users: Iterable[str] = (),
    match_ids: Iterable[str] = (),
    days: int = 14,
    limit: int = 20,
    maps: Iterable[str] = (),
    timeout: float = 20.0,
    proxy: Optional[str] = None,
    delay: float = 1.0,
    quiet: bool = False,
) -> list[Job]:
    wanted_maps = {
        str(item).lower().removeprefix("de_") for item in maps if str(item).strip()
    }
    jobs_out: list[Job] = []
    pairs: list[tuple[str, str]] = []  # (account, match_id)

    for user in users:
        uuid = get_uuid(user, timeout=timeout, proxy=proxy)
        records = list_matches(
            uuid, days=days, limit=limit, timeout=timeout, proxy=proxy
        )
        if not quiet:
            print(f"  5E {parse_user_id(user)}: {len(records)} 场候选", flush=True)
        for record in records:
            match_id = str(record.get("match_id") or "").strip()
            if match_id:
                pairs.append((parse_user_id(user), match_id))
        if delay > 0:
            time.sleep(delay)

    for match_id in match_ids:
        pairs.append(("", str(match_id).strip()))

    # Preserve order while removing duplicate match ids.
    seen: set[str] = set()
    deduped_pairs: list[tuple[str, str]] = []
    for account, match_id in pairs:
        if not match_id or match_id in seen:
            continue
        seen.add(match_id)
        deduped_pairs.append((account, match_id))

    for account, match_id in deduped_pairs:
        detail = get_match_detail(match_id, timeout=timeout, proxy=proxy)
        match = match_from_detail(match_id, detail)
        if match is None:
            if not quiet:
                print(f"  - 5E {match_id} 没有详情，跳过", flush=True)
            continue
        if wanted_maps and match.map_label.lower() not in wanted_maps:
            continue
        if not match.demo_url:
            if not quiet:
                print(
                    f"  - 5E {match_id} ({match.map_label or '?'}) 没有 demo URL"
                    "（可能已过 15 天保存期）",
                    flush=True,
                )
            continue
        jobs_out.append(
            Job(
                source="5e",
                match_id=match_id,
                url=match.demo_url,
                map_name=match.map_label,
                date=match.date,
                account=account,
                suggested_name=f"5e_{match_id}_{match.map_label}_{match.date}",
                meta={
                    "demo_url": redact_url(match.demo_url),
                    "start_time": match.started_at,
                    "end_time": match.ended_at,
                },
            )
        )
        if len(jobs_out) >= max(1, limit):
            break
        if delay > 0:
            time.sleep(delay)
    return jobs_out[: max(1, limit)]
