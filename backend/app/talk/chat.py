# app/talk/chat.py
"""会話1ターン分の入力の切り詰めと、Claude の出力の整理（DB・HTTP に依存しない純粋な処理）。"""

from collections.abc import Mapping, Sequence
from typing import Any

from app.talk.prompt import (
    MAX_LEARNED,
    MAX_LEARNED_CHARS,
    MAX_MESSAGE_CHARS,
    MAX_MESSAGES,
    MOODS,
    TOPIC_KEYS,
    TOPIC_STATES,
)

MAX_DISPLAY_CHARS = 600
MAX_SUMMARY_CHARS = 400


class InvalidTurn(ValueError):
    """Claude の出力に返事（display）が無い等、使えない。"""


def clip_messages(messages: Sequence[tuple[str, str]]) -> list[tuple[str, str]]:
    """直近 MAX_MESSAGES 発言だけを、1発言 MAX_MESSAGE_CHARS 字までにして使う。"""
    return [(role, text[:MAX_MESSAGE_CHARS]) for role, text in list(messages)[-MAX_MESSAGES:]]


def clean_topics(topics: Mapping[str, Any] | None) -> dict[str, str]:
    topics = topics or {}
    return {k: v if (v := topics.get(k)) in TOPIC_STATES else "todo" for k in TOPIC_KEYS}


def clean_learned(learned: Any) -> list[str]:
    if not isinstance(learned, list):
        return []
    items = [str(x).strip()[:MAX_LEARNED_CHARS] for x in learned if isinstance(x, (str, int, float))]
    return [x for x in items if x][:MAX_LEARNED]


def apply_turn(out: Any, prev_topics: dict[str, str], prev_learned: list[str]) -> dict[str, Any]:
    """Claude の出力を、画面に返す形 {display, mood, topics, learned, summary} に整える。

    - done になったテーマは戻さない（Claude が todo/active を返しても done のまま）
    - learned は最大 MAX_LEARNED 個。Claude が返さなければ前のまま
    - summary は 3つとも done のときだけ
    """
    if not isinstance(out, Mapping):
        raise InvalidTurn("output is not an object")
    display = out.get("display")
    if not isinstance(display, str) or not display.strip():
        raise InvalidTurn("output has no display")

    new_topics = out.get("topics") if isinstance(out.get("topics"), Mapping) else {}
    topics = {}
    for k in TOPIC_KEYS:
        v = new_topics.get(k, prev_topics[k])
        topics[k] = "done" if prev_topics[k] == "done" else (v if v in TOPIC_STATES else prev_topics[k])

    learned = clean_learned(out.get("learned")) or prev_learned[:MAX_LEARNED]
    mood = out.get("mood") if out.get("mood") in MOODS else "curious"
    summary = out.get("summary") if isinstance(out.get("summary"), str) else ""
    if not all(v == "done" for v in topics.values()):
        summary = ""
    return {
        "display": display.strip()[:MAX_DISPLAY_CHARS],
        "mood": mood,
        "topics": topics,
        "learned": learned,
        "summary": summary.strip()[:MAX_SUMMARY_CHARS],
    }
