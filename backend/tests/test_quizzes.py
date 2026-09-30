"""D3（問題取得）・D4（回答・採点）を DB 付きで確かめる（design.md 8.6 の確認項目）。

問題は fake 生成器（runner.fake_generate_quiz）で作るので、正解は 1問目から順に 0, 1, 3。
"""

from uuid import uuid4

from tests.conftest import cli_headers, commit_body, make_user, register_repo, web_headers

CORRECT = [0, 1, 3]


def create_quiz(client, user) -> tuple[str, str]:
    repo_id = register_repo(client, user)
    r = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    assert r.status_code == 201, r.text
    return repo_id, r.json()["quiz_id"]


def question_ids(client, headers, quiz_id) -> list[str]:
    r = client.get(f"/api/v1/quizzes/{quiz_id}", headers=headers)
    assert r.status_code == 200, r.text
    return [q["question_id"] for q in r.json()["questions"]]


def answer(client, headers, quiz_id, question_id, index):
    return client.post(
        f"/api/v1/quizzes/{quiz_id}/answers",
        json={"question_id": question_id, "selected_index": index},
        headers=headers,
    )


# ---- D3 -----------------------------------------------------------------------


def test_get_quiz_hides_answers(client, fake_auth, user):
    repo_id, quiz_id = create_quiz(client, user)

    r = client.get(f"/api/v1/quizzes/{quiz_id}", headers=web_headers(fake_auth, user))
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["quiz_id"] == quiz_id
    assert body["status"] == "ready"
    assert body["commit"]["repository_id"] == repo_id
    assert body["commit"]["repository_name"] == "demo-repo"
    assert body["commit"]["diff"] == commit_body(repo_id)["diff"]
    assert [q["position"] for q in body["questions"]] == [1, 2, 3]
    assert body["progress"] == {"solved_count": 0, "total_count": 3}
    for q in body["questions"]:
        assert set(q) == {"question_id", "position", "question", "choices", "hint", "solved"}
        assert q["solved"] is False


def test_get_quiz_requires_web_login(client, fake_auth, user):
    _, quiz_id = create_quiz(client, user)
    # X-User-Id だけでは問題・差分を取得できない。
    assert client.get(f"/api/v1/quizzes/{quiz_id}", headers=cli_headers(user)).status_code == 401
    assert client.get(f"/api/v1/quizzes/{quiz_id}").status_code == 401


def test_get_other_users_or_unknown_quiz_is_404(client, fake_auth, db, user):
    _, quiz_id = create_quiz(client, user)
    other = make_user(db)
    assert client.get(f"/api/v1/quizzes/{quiz_id}", headers=web_headers(fake_auth, other)).status_code == 404
    assert client.get(f"/api/v1/quizzes/{uuid4()}", headers=web_headers(fake_auth, user)).status_code == 404


# ---- D4 -----------------------------------------------------------------------


def test_wrong_then_right_answer(client, fake_auth, db, user):
    _, quiz_id = create_quiz(client, user)
    headers = web_headers(fake_auth, user)
    q1 = question_ids(client, headers, quiz_id)[0]
    hint, explanation = db.execute(
        "select hint, explanation from public.questions where id = %s", (q1,)
    ).fetchone()

    r = answer(client, headers, quiz_id, q1, CORRECT[0] + 1)
    assert r.status_code == 200, r.text
    assert r.json() == {
        "question_id": q1,
        "correct": False,
        "feedback": hint,
        "progress": {"solved_count": 0, "total_count": 3},
        "passed": False,
    }

    # 不正解のあとは再回答できる。
    r = answer(client, headers, quiz_id, q1, CORRECT[0])
    assert r.status_code == 200, r.text
    assert r.json()["correct"] is True
    assert r.json()["feedback"] == explanation
    assert r.json()["progress"] == {"solved_count": 1, "total_count": 3}

    # 再読み込みしても進捗が残る。
    body = client.get(f"/api/v1/quizzes/{quiz_id}", headers=headers).json()
    assert [q["solved"] for q in body["questions"]] == [True, False, False]
    assert body["progress"]["solved_count"] == 1


def test_all_correct_passes_and_unblocks_push(client, fake_auth, db, user):
    repo_id, quiz_id = create_quiz(client, user)
    headers = web_headers(fake_auth, user)
    qids = question_ids(client, headers, quiz_id)

    results = [answer(client, headers, quiz_id, qid, idx).json() for qid, idx in zip(qids, CORRECT)]
    assert [r["passed"] for r in results] == [False, False, True]
    assert results[-1]["progress"] == {"solved_count": 3, "total_count": 3}

    status, passed_at = db.execute(
        "select status, passed_at from public.commits where id = %s", (quiz_id,)
    ).fetchone()
    assert status == "passed"
    assert passed_at is not None
    assert client.get(f"/api/v1/quizzes/{quiz_id}", headers=headers).json()["status"] == "passed"

    r = client.post(
        "/api/v1/push/check",
        json={"repository_id": repo_id, "commit_shas": [commit_body(repo_id)["commit_sha"]]},
        headers=cli_headers(user),
    )
    assert r.json() == {"allowed": True, "pending_commits": []}


def test_solved_question_cannot_be_changed(client, fake_auth, db, user):
    _, quiz_id = create_quiz(client, user)
    headers = web_headers(fake_auth, user)
    q1 = question_ids(client, headers, quiz_id)[0]
    assert answer(client, headers, quiz_id, q1, CORRECT[0]).json()["correct"] is True

    # 同じ選択肢の再送（二重送信）は保存済みの結果を返し、正解数を二重に数えない。
    r = answer(client, headers, quiz_id, q1, CORRECT[0])
    assert r.status_code == 200
    assert r.json()["correct"] is True
    assert r.json()["progress"]["solved_count"] == 1

    # 別の選択肢への変更は 409。誤答で上書きしない。
    assert answer(client, headers, quiz_id, q1, CORRECT[0] + 1).status_code == 409
    row = db.execute("select selected_index, is_correct from public.answers where question_id = %s", (q1,)).fetchone()
    assert row == (CORRECT[0], True)


def test_answer_other_users_quiz_is_404(client, fake_auth, db, user):
    _, quiz_id = create_quiz(client, user)
    q1 = question_ids(client, web_headers(fake_auth, user), quiz_id)[0]
    other = make_user(db)
    assert answer(client, web_headers(fake_auth, other), quiz_id, q1, CORRECT[0]).status_code == 404
    assert db.execute("select count(*) from public.answers").fetchone() == (0,)


def test_answer_question_of_another_quiz_is_404(client, fake_auth, user):
    repo_id, quiz_id = create_quiz(client, user)
    headers = web_headers(fake_auth, user)
    r = client.post(
        "/api/v1/commits",
        json=commit_body(repo_id, sha="3" * 40),
        headers=cli_headers(user),
    )
    other_quiz_q1 = question_ids(client, headers, r.json()["quiz_id"])[0]
    assert answer(client, headers, quiz_id, other_quiz_q1, 0).status_code == 404


def test_answer_requires_web_login(client, fake_auth, user):
    _, quiz_id = create_quiz(client, user)
    q1 = question_ids(client, web_headers(fake_auth, user), quiz_id)[0]
    assert answer(client, cli_headers(user), quiz_id, q1, 0).status_code == 401


def test_answer_index_out_of_range_is_422(client, fake_auth, user):
    _, quiz_id = create_quiz(client, user)
    headers = web_headers(fake_auth, user)
    q1 = question_ids(client, headers, quiz_id)[0]
    assert answer(client, headers, quiz_id, q1, 4).status_code == 422
