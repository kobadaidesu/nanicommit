"""DB を使わない入力・生成結果の検証。"""

import asyncio

import pytest
from pydantic import ValidationError

from app.auth import parse_bearer, parse_user_id
from app.learning.errors import (
    GenerationTimeout,
    GenerationUnavailable,
    InsufficientContext,
    InvalidGeneratedQuiz,
)
from app.learning.runner import fake_generate_quiz, run_generation
from app.schemas import CommitIn, GeneratedQuiz, PushCheckIn

REPO = "550e8400-e29b-41d4-a716-446655440000"
SHA = "0226edda3ed64eb7821ee827df5a63c8f9d21012"


def valid_commit(**overrides) -> dict:
    return {"repository_id": REPO, "commit_sha": SHA, "branch": None, "message": "m", "files": ["a"], "diff": "d", **overrides}


def valid_question(**overrides) -> dict:
    return {
        "question": "q",
        "choices": ["a", "b", "c", "d"],
        "correct_index": 0,
        "hint": "h",
        "explanation": "e",
        **overrides,
    }


def test_commit_accepts_sha1_and_sha256():
    CommitIn.model_validate(valid_commit())
    CommitIn.model_validate(valid_commit(commit_sha="a" * 64))


@pytest.mark.parametrize(
    "overrides",
    [
        {"commit_sha": SHA.upper()},
        {"commit_sha": SHA[:39]},
        {"files": ["a", "a"]},
        {"files": [""]},
        {"branch": ""},
        {"extra": 1},
    ],
)
def test_commit_rejects_invalid(overrides):
    with pytest.raises(ValidationError):
        CommitIn.model_validate(valid_commit(**overrides))


def test_push_check_requires_at_least_one_sha():
    with pytest.raises(ValidationError):
        PushCheckIn.model_validate({"repository_id": REPO, "commit_shas": []})


@pytest.mark.parametrize(
    "questions",
    [
        [valid_question()] * 2,
        [valid_question()] * 4,
        [valid_question(choices=["a", "b", "c"])] * 3,
        [valid_question(choices=["a", "a", "c", "d"])] * 3,
        [valid_question(correct_index=4)] * 3,
        [valid_question(hint="")] * 3,
    ],
)
def test_generated_quiz_rejects_invalid(questions):
    with pytest.raises(ValidationError):
        GeneratedQuiz.model_validate({"questions": questions})


def test_parse_user_id():
    assert parse_user_id("3f6c1a2e-8b4d-4e7a-9c1f-2d5b8e0a7c34") is not None
    assert parse_user_id("3F6C1A2E-8B4D-4E7A-9C1F-2D5B8E0A7C34") is None
    assert parse_user_id("3f6c1a2e8b4d4e7a9c1f2d5b8e0a7c34") is None
    assert parse_user_id("nope") is None
    assert parse_user_id(None) is None


def test_parse_bearer():
    assert parse_bearer("Bearer abc") == "abc"
    assert parse_bearer("bearer abc") == "abc"
    assert parse_bearer("Basic abc") is None
    assert parse_bearer("Bearer ") is None
    assert parse_bearer(None) is None


def run(generator, timeout=1.0):
    return asyncio.run(run_generation(generator, message="m", files=["a"], diff="d", timeout=timeout))


def test_fake_generator_is_valid_for_edge_inputs():
    for files, message in [([], ""), (["README.md"], "WIP"), (["a"] * 1, "Initial commit")]:
        quiz = asyncio.run(fake_generate_quiz(message=message, files=files, diff=""))
        assert len(quiz.questions) == 3


def test_run_generation_accepts_dict():
    async def gen(**_):
        return {"questions": [valid_question()] * 3}

    assert len(run(gen).questions) == 3


def test_run_generation_rejects_invalid_result():
    async def gen(**_):
        return {"questions": [valid_question()]}

    with pytest.raises(InvalidGeneratedQuiz):
        run(gen)


def test_run_generation_timeout():
    async def gen(**_):
        await asyncio.sleep(1)

    with pytest.raises(GenerationTimeout):
        run(gen, timeout=0.01)


def test_run_generation_passes_generation_errors_through():
    async def gen(**_):
        raise InsufficientContext("empty")

    with pytest.raises(InsufficientContext):
        run(gen)


def test_run_generation_wraps_unexpected_errors():
    async def gen(**_):
        raise RuntimeError("boom")

    with pytest.raises(GenerationUnavailable):
        run(gen)
