from __future__ import annotations

import base64
import hashlib
import json
from uuid import uuid4

import allure
import pytest


pytestmark = [
    allure.epic("Feed System"),
    allure.feature("登录态 Redis 缓存"),
]


def bearer(token: str) -> dict[str, str]:
    return {"Authorization": f"Bearer {token}"}


def cache_keys(account_id: object) -> tuple[str, str]:
    return f"auth:access:{account_id}", f"auth:access:state:{account_id}"


def assert_current_cache(redis_command, account_id: object, token: str) -> int:
    key, state_key = cache_keys(account_id)
    expected_hash = hashlib.sha256(token.encode()).hexdigest()
    cached = redis_command("MGET", key, state_key)
    assert cached == [expected_hash, "ready"], cached
    pttl = redis_command("PTTL", key)
    payload = token.split(".")[1]
    claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    lifetime_ms = (claims["exp"] - claims["iat"]) * 1000
    assert max(1, lifetime_ms - 5000) <= pttl <= lifetime_ms + 1000
    assert 0 < redis_command("PTTL", state_key) <= lifetime_ms + 1000
    allure.attach(
        json.dumps({"key": key, "hash": expected_hash, "state": "ready", "pttl_ms": pttl}, ensure_ascii=False),
        name="Redis 缓存检查",
        attachment_type=allure.attachment_type.JSON,
    )
    return pttl


@allure.story("登录写入缓存")
@allure.title("不同账号使用独立缓存，哈希和 TTL 与签发的 token 对应")
def test_login_populates_independent_caches(create_account, login_account, redis_command):
    first = login_account(create_account())
    second = login_account(create_account())
    assert first["account_id"] != second["account_id"]
    with allure.step("分别检查两个账号的哈希、ready 状态和过期时间"):
        for login in (first, second):
            assert_current_cache(redis_command, login["account_id"], login["token"])


@allure.story("刷新维护缓存")
@allure.title("刷新替换缓存哈希，新 token 可用且旧 token 被拒绝")
def test_refresh_replaces_cached_token(api_base_url, http, create_account, login_account, redis_command):
    login = login_account(create_account())
    account_id, old_token = login["account_id"], login["token"]
    assert_current_cache(redis_command, account_id, old_token)
    with allure.step("使用 refresh token 获取新 access token"):
        refreshed = http.post(
            f"{api_base_url}/account/refresh",
            json={"refresh_token": login["refresh_token"]}, timeout=10,
        )
        assert refreshed.status_code == 200, refreshed.text
        token = refreshed.json()["token"]
        assert token != old_token
        assert_current_cache(redis_command, account_id, token)
    with allure.step("确认新旧 token 的鉴权结果"):
        assert http.get(f"{api_base_url}/account/me", headers=bearer(old_token), timeout=10).status_code == 401
        assert http.get(f"{api_base_url}/account/me", headers=bearer(token), timeout=10).status_code == 200


@allure.story("改名维护缓存")
@allure.title("改名后的缓存对应新 token，并撤销旧 token")
def test_rename_replaces_cached_token(api_base_url, http, create_account, login_account, redis_command):
    login = login_account(create_account())
    renamed = http.post(
        f"{api_base_url}/account/rename",
        json={"new_username": "cache_rename_" + uuid4().hex[:12]},
        headers=bearer(login["token"]), timeout=10,
    )
    assert renamed.status_code == 200, renamed.text
    token = renamed.json()["token"]
    assert token != login["token"]
    assert_current_cache(redis_command, login["account_id"], token)
    assert http.get(f"{api_base_url}/account/me", headers=bearer(login["token"]), timeout=10).status_code == 401
    assert http.get(f"{api_base_url}/account/me", headers=bearer(token), timeout=10).status_code == 200


@allure.story("数据库回滚后修复缓存")
@allure.title("改名重名导致事务回滚后，原会话继续有效并修复更新中标记")
def test_duplicate_rename_repairs_cache(api_base_url, http, create_account, login_account, redis_command):
    account, other = create_account(), create_account()
    login = login_account(account)
    duplicate = http.post(
        f"{api_base_url}/account/rename", json={"new_username": other["username"]},
        headers=bearer(login["token"]), timeout=10,
    )
    assert duplicate.status_code == 409, duplicate.text
    profile = http.get(f"{api_base_url}/account/me", headers=bearer(login["token"]), timeout=10)
    assert profile.status_code == 200, profile.text
    assert profile.json()["username"] == account["username"]
    assert_current_cache(redis_command, login["account_id"], login["token"])


@allure.story("退出清除缓存")
@allure.title("退出删除缓存，并拒绝旧 access token 与 refresh token")
def test_logout_removes_cache_and_revokes_tokens(api_base_url, http, create_account, login_account, redis_command):
    login = login_account(create_account())
    assert_current_cache(redis_command, login["account_id"], login["token"])
    response = http.post(f"{api_base_url}/account/logout", headers=bearer(login["token"]), timeout=10)
    assert response.status_code == 200, response.text
    assert redis_command("EXISTS", *cache_keys(login["account_id"])) == 0
    assert http.get(f"{api_base_url}/account/me", headers=bearer(login["token"]), timeout=10).status_code == 401
    assert http.post(
        f"{api_base_url}/account/refresh", json={"refresh_token": login["refresh_token"]}, timeout=10,
    ).status_code == 401
    assert redis_command("EXISTS", *cache_keys(login["account_id"])) == 0


@allure.story("修改密码清除缓存")
@allure.title("改密清除旧缓存，新密码登录后缓存属于新会话")
def test_password_change_removes_cached_session(api_base_url, http, create_account, login_account, redis_command):
    account = create_account()
    login = login_account(account)
    assert_current_cache(redis_command, login["account_id"], login["token"])
    new_password = "Changed-" + uuid4().hex[:16]
    response = http.post(
        f"{api_base_url}/account/changePassword",
        json={"username": account["username"], "old_password": account["password"], "new_password": new_password},
        timeout=10,
    )
    assert response.status_code == 200, response.text
    assert redis_command("EXISTS", *cache_keys(login["account_id"])) == 0
    assert http.get(f"{api_base_url}/account/me", headers=bearer(login["token"]), timeout=10).status_code == 401
    assert http.post(
        f"{api_base_url}/account/refresh", json={"refresh_token": login["refresh_token"]}, timeout=10,
    ).status_code == 401
    new_login = login_account({"username": account["username"], "password": new_password})
    assert_current_cache(redis_command, new_login["account_id"], new_login["token"])


@allure.story("重新登录替换缓存")
@allure.title("重新登录替换旧会话，旧 token 不能注销新会话")
def test_relogin_preserves_new_session_on_stale_logout(api_base_url, http, create_account, login_account, redis_command):
    account = create_account()
    old, new = login_account(account), login_account(account)
    assert old["token"] != new["token"]
    assert_current_cache(redis_command, new["account_id"], new["token"])
    stale_logout = http.post(f"{api_base_url}/account/logout", headers=bearer(old["token"]), timeout=10)
    assert stale_logout.status_code == 401, stale_logout.text
    assert http.get(f"{api_base_url}/account/me", headers=bearer(new["token"]), timeout=10).status_code == 200
    assert_current_cache(redis_command, new["account_id"], new["token"])


@allure.story("缓存未命中时回填")
@allure.title("删除缓存后通过 MySQL 回填，正常命中不重置 TTL")
def test_cache_miss_refills_without_extending_hits(api_base_url, http, create_account, login_account, redis_command):
    login = login_account(create_account())
    keys = cache_keys(login["account_id"])
    with allure.step("只删除本测试账号的缓存，再通过接口触发回填"):
        redis_command("DEL", *keys)
        profile = http.get(f"{api_base_url}/account/me", headers=bearer(login["token"]), timeout=10)
        assert profile.status_code == 200, profile.text
        ttl = assert_current_cache(redis_command, login["account_id"], login["token"])
    with allure.step("缩短测试缓存 TTL，确认后续命中不续期"):
        short_ttl = max(1, min(20_000, ttl // 2))
        for key in keys:
            assert redis_command("PEXPIRE", key, short_ttl) == 1
        assert http.get(f"{api_base_url}/account/me", headers=bearer(login["token"]), timeout=10).status_code == 200
        assert 0 < redis_command("PTTL", keys[0]) <= short_ttl


@pytest.mark.parametrize("state", ["missing", "updating"], ids=["legacy-cache", "unfinished-update"])
@allure.story("拒绝不可信旧缓存")
@allure.title("状态为 {state} 的旧缓存不会使已撤销的 token 重新有效")
def test_untrusted_old_cache_does_not_authorize_token(
    state, api_base_url, http, create_account, login_account, redis_command,
):
    account = create_account()
    old, current = login_account(account), login_account(account)
    key, state_key = cache_keys(current["account_id"])
    old_hash = hashlib.sha256(old["token"].encode()).hexdigest()
    with allure.step("仅针对本测试账号模拟旧缓存或未完成的更新"):
        redis_command("SET", key, old_hash, "EX", 60)
        if state == "missing":
            redis_command("DEL", state_key)
        else:
            redis_command("SET", state_key, "updating:pytest-" + uuid4().hex)
    with allure.step("旧 token 被拒绝，同时从数据库修复缓存"):
        response = http.get(f"{api_base_url}/account/me", headers=bearer(old["token"]), timeout=10)
        assert response.status_code == 401, response.text
        assert_current_cache(redis_command, current["account_id"], current["token"])
        assert http.get(f"{api_base_url}/account/me", headers=bearer(current["token"]), timeout=10).status_code == 200
