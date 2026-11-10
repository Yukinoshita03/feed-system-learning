from concurrent.futures import ThreadPoolExecutor

import allure
import pytest
import requests

from test_comments import call

pytestmark = [allure.feature("关注 CRUD")]


@pytest.fixture
def social_users(create_account, login_account):
    users = [login_account(create_account()) for _ in range(4)]
    return [(user, {"Authorization": "Bearer " + user["token"]}) for user in users]


@pytest.mark.parametrize("endpoint", ["follow", "unfollow", "isFollowing", "getAllFollowers", "getAllVloggers"])
def test_social_requires_login(api_base_url, http, endpoint):
    call(http, api_base_url, "/social/" + endpoint, {"vlogger_id": 1}, status=401)


def test_follow_lifecycle_identity_and_public_data(api_base_url, http, social_users):
    (a, actor), (b, target), (c, other), _ = social_users
    body = {"vlogger_id": b["account_id"], "follower_id": c["account_id"]}
    for _ in range(2):
        result = call(http, api_base_url, "/social/follow", body, actor)
        assert result == {"is_following": True, "following_count": 1, "follower_count": 1}
    assert call(http, api_base_url, "/social/isFollowing", body, actor)["is_following"]
    assert not call(http, api_base_url, "/social/isFollowing", body, other)["is_following"]
    following = call(http, api_base_url, "/social/getAllVloggers", {}, actor)
    assert [u["id"] for u in following["vloggers"]] == [b["account_id"]]
    follower = call(http, api_base_url, "/social/getAllFollowers", {}, target)
    assert follower["total"] == 1 and follower["followers"][0]["id"] == a["account_id"]
    assert not ({"password", "access_token_hash", "refresh_token_hash"} & follower["followers"][0].keys())
    # 已登录用户可读取另一个账号的公开关系列表。
    assert call(http, api_base_url, "/social/getAllFollowers", {"vlogger_id": b["account_id"]}, other)["total"] == 1
    for _ in range(2):
        result = call(http, api_base_url, "/social/unfollow", body, actor)
        assert result == {"is_following": False, "following_count": 0, "follower_count": 0}
    assert call(http, api_base_url, "/social/getAllFollowers", {}, target)["followers"] == []


def test_following_pagination_and_no_reorder_on_retries(api_base_url, http, social_users):
    (_, actor), *targets = social_users
    ids = [user["account_id"] for user, _ in targets]
    for target_id in ids:
        call(http, api_base_url, "/social/follow", {"vlogger_id": target_id}, actor)
    call(http, api_base_url, "/social/follow", {"vlogger_id": ids[0]}, actor)
    first = call(http, api_base_url, "/social/getAllVloggers", {"limit": 2}, actor)
    assert first["total"] == 3 and first["has_more"]
    second = call(http, api_base_url, "/social/getAllVloggers", {"limit": 2, "before_id": first["next_id"]}, actor)
    assert not second["has_more"]
    assert [u["id"] for u in first["vloggers"] + second["vloggers"]] == ids[::-1]


def test_follow_cannot_target_self_or_missing_user(api_base_url, http, social_users):
    user, headers = social_users[0]
    for path in ("follow", "unfollow"):
        call(http, api_base_url, "/social/" + path, {"vlogger_id": user["account_id"]}, headers, 400)
        call(http, api_base_url, "/social/" + path, {"vlogger_id": 4294967295}, headers, 404)
    call(http, api_base_url, "/social/isFollowing", {"vlogger_id": 4294967295}, headers, 404)
    call(http, api_base_url, "/social/getAllFollowers", {"vlogger_id": 4294967295}, headers, 404)


@pytest.mark.parametrize("body", [{}, {"vlogger_id": 0}, {"vlogger_id": -1}, {"vlogger_id": "bad"}])
def test_invalid_follow_target(api_base_url, http, social_users, body):
    _, headers = social_users[0]
    for path in ("follow", "unfollow", "isFollowing"):
        call(http, api_base_url, "/social/" + path, body, headers, 400)


@pytest.mark.parametrize("body", [{"limit": 101}, {"limit": -1}, {"before_id": -1}])
def test_invalid_social_page(api_base_url, http, social_users, body):
    _, headers = social_users[0]
    for path in ("getAllFollowers", "getAllVloggers"):
        call(http, api_base_url, "/social/" + path, body, headers, 400)


def test_mutual_concurrent_follows_do_not_deadlock_or_duplicate(api_base_url, http, social_users):
    (a, ah), (b, bh), *_ = social_users
    pairs = [(b["account_id"], ah), (a["account_id"], bh)] * 8
    with ThreadPoolExecutor(max_workers=8) as pool:
        results = list(pool.map(lambda pair: call(requests, api_base_url, "/social/follow", {"vlogger_id": pair[0]}, pair[1]), pairs))
    assert all(r["following_count"] == 1 and r["follower_count"] in (0, 1) for r in results)
    for headers in (ah, bh):
        assert call(http, api_base_url, "/social/getAllFollowers", {}, headers)["total"] == 1
        assert call(http, api_base_url, "/social/getAllVloggers", {}, headers)["total"] == 1
