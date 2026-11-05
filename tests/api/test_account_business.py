from __future__ import annotations

import base64
from io import BytesIO

import allure
import pytest
import requests


def bearer(token: object) -> dict[str, str]:
    assert isinstance(token, str) and token
    return {"Authorization": f"Bearer {token}"}


@allure.epic("Feed System")
@allure.feature("账号业务接口")
@allure.story("公开资料查询")
@allure.title("可按账号 ID 和用户名查询公开资料")
def test_find_account_by_id_and_username(
    api_base_url: str,
    http: requests.Session,
    create_account,
    login_account,
) -> None:
    account = create_account()
    login = login_account(account)
    account_id = login["account_id"]

    with allure.step("按 ID 查询账号资料"):
        by_id = http.post(
            f"{api_base_url}/account/findByID",
            json={"id": account_id},
            timeout=10,
        )
        assert by_id.status_code == 200, by_id.text
        assert by_id.json()["id"] == account_id
        assert by_id.json()["username"] == account["username"]

    with allure.step("按用户名查询同一账号"):
        by_username = http.post(
            f"{api_base_url}/account/findByUsername",
            json={"username": account["username"]},
            timeout=10,
        )
        assert by_username.status_code == 200, by_username.text
        assert by_username.json()["id"] == account_id

    with allure.step("确认公开资料不含密码和登录凭证"):
        for payload in (by_id.json(), by_username.json()):
            assert "password" not in payload
            assert "access_token_hash" not in payload
            assert "refresh_token_hash" not in payload

    with allure.step("不存在的用户名返回 404"):
        missing = http.post(
            f"{api_base_url}/account/findByUsername",
            json={"username": "missing_" + account["username"]},
            timeout=10,
        )
        assert missing.status_code == 404


@allure.epic("Feed System")
@allure.feature("账号业务接口")
@allure.story("修改密码")
@allure.title("修改密码后旧访问和刷新 token 均失效")
def test_change_password_revokes_old_tokens(
    api_base_url: str,
    http: requests.Session,
    create_account,
    login_account,
) -> None:
    account = create_account()
    login = login_account(account)
    old_password = account["password"]
    new_password = "New-" + account["password"]

    with allure.step("错误旧密码被拒绝，但不修改账号"):
        wrong_old_password = http.post(
            f"{api_base_url}/account/changePassword",
            json={
                "username": account["username"],
                "old_password": "wrong-password",
                "new_password": new_password,
            },
            timeout=10,
        )
        assert wrong_old_password.status_code == 400

    with allure.step("使用正确旧密码修改密码"):
        changed = http.post(
            f"{api_base_url}/account/changePassword",
            json={
                "username": account["username"],
                "old_password": old_password,
                "new_password": new_password,
            },
            timeout=10,
        )
        assert changed.status_code == 200, changed.text

    with allure.step("检查旧会话已被撤销"):
        old_access = http.get(
            f"{api_base_url}/account/me",
            headers=bearer(login["token"]),
            timeout=10,
        )
        assert old_access.status_code == 401

        old_refresh = http.post(
            f"{api_base_url}/account/refresh",
            json={"refresh_token": login["refresh_token"]},
            timeout=10,
        )
        assert old_refresh.status_code == 401

    with allure.step("新密码可以登录，旧密码不能登录"):
        old_login = http.post(
            f"{api_base_url}/account/login",
            json={"username": account["username"], "password": old_password},
            timeout=10,
        )
        assert old_login.status_code == 401

        new_login = http.post(
            f"{api_base_url}/account/login",
            json={"username": account["username"], "password": new_password},
            timeout=10,
        )
        assert new_login.status_code == 200, new_login.text


@allure.epic("Feed System")
@allure.feature("账号业务接口")
@allure.story("修改用户名")
@allure.title("改名需要登录、检查重名并轮换访问 token")
def test_rename_requires_auth_and_rotates_access_token(
    api_base_url: str,
    http: requests.Session,
    create_account,
    login_account,
) -> None:
    account = create_account()
    other_account = create_account()
    login = login_account(account)

    with allure.step("未登录时禁止改名"):
        unauthorized = http.post(
            f"{api_base_url}/account/rename",
            json={"new_username": "no_auth_rename"},
            timeout=10,
        )
        assert unauthorized.status_code == 401

    with allure.step("不能改成已存在的用户名"):
        duplicate = http.post(
            f"{api_base_url}/account/rename",
            json={"new_username": other_account["username"]},
            headers=bearer(login["token"]),
            timeout=10,
        )
        assert duplicate.status_code == 409

    renamed_username = "renamed_" + account["username"]
    with allure.step("改名成功后返回新 token，旧 token 失效"):
        renamed = http.post(
            f"{api_base_url}/account/rename",
            json={"new_username": renamed_username},
            headers=bearer(login["token"]),
            timeout=10,
        )
        assert renamed.status_code == 200, renamed.text
        new_token = renamed.json()["token"]
        assert new_token

        old_token_response = http.get(
            f"{api_base_url}/account/me",
            headers=bearer(login["token"]),
            timeout=10,
        )
        assert old_token_response.status_code == 401

        new_token_response = http.get(
            f"{api_base_url}/account/me",
            headers=bearer(new_token),
            timeout=10,
        )
        assert new_token_response.status_code == 200, new_token_response.text
        assert new_token_response.json()["username"] == renamed_username

    with allure.step("原 refresh token 可继续刷新，返回改名后的账号信息"):
        refreshed = http.post(
            f"{api_base_url}/account/refresh",
            json={"refresh_token": login["refresh_token"]},
            timeout=10,
        )
        assert refreshed.status_code == 200, refreshed.text
        assert refreshed.json()["username"] == renamed_username


@allure.epic("Feed System")
@allure.feature("账号业务接口")
@allure.story("更新个人资料")
@allure.title("资料接口仅更新请求中提供的字段")
def test_update_profile_changes_supplied_fields(
    api_base_url: str,
    http: requests.Session,
    create_account,
    login_account,
) -> None:
    account = create_account()
    login = login_account(account)
    headers = bearer(login["token"])

    with allure.step("资料更新要求登录"):
        unauthorized = http.post(
            f"{api_base_url}/account/updateProfile",
            json={"bio": "should not save"},
            timeout=10,
        )
        assert unauthorized.status_code == 401

    with allure.step("空更新请求被拒绝"):
        empty = http.post(
            f"{api_base_url}/account/updateProfile",
            json={},
            headers=headers,
            timeout=10,
        )
        assert empty.status_code == 400

    avatar_url = "https://images.example.test/avatar.png"
    with allure.step("更新简介与头像地址"):
        updated = http.post(
            f"{api_base_url}/account/updateProfile",
            json={"bio": "pytest profile", "avatar_url": avatar_url},
            headers=headers,
            timeout=10,
        )
        assert updated.status_code == 200, updated.text

        me = http.get(
            f"{api_base_url}/account/me",
            headers=headers,
            timeout=10,
        )
        assert me.status_code == 200, me.text
        assert me.json()["bio"] == "pytest profile"
        assert me.json()["avatar_url"] == avatar_url

    with allure.step("允许将简介清空，同时保留头像地址"):
        cleared = http.post(
            f"{api_base_url}/account/updateProfile",
            json={"bio": ""},
            headers=headers,
            timeout=10,
        )
        assert cleared.status_code == 200, cleared.text
        profile = http.get(
            f"{api_base_url}/account/me",
            headers=headers,
            timeout=10,
        ).json()
        assert profile.get("bio", "") == ""
        assert profile["avatar_url"] == avatar_url


@allure.epic("Feed System")
@allure.feature("账号业务接口")
@allure.story("上传头像")
@allure.title("上传的图片落盘、可访问并写入账号资料")
def test_upload_avatar_stores_and_serves_image(
    api_base_url: str,
    http: requests.Session,
    create_account,
    login_account,
) -> None:
    account = create_account()
    login = login_account(account)
    headers = bearer(login["token"])
    # A tiny PNG fixture; the endpoint checks its detected image type and suffix.
    image_bytes = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+nmz8AAAAASUVORK5CYII="
    )

    with allure.step("未登录时不能上传头像"):
        unauthorized = http.post(
            f"{api_base_url}/account/uploadAvatar",
            files={"file": ("avatar.png", BytesIO(image_bytes), "image/png")},
            timeout=10,
        )
        assert unauthorized.status_code == 401

    with allure.step("拒绝非图片内容"):
        invalid = http.post(
            f"{api_base_url}/account/uploadAvatar",
            headers=headers,
            files={"file": ("avatar.png", BytesIO(b"plain text"), "image/png")},
            timeout=10,
        )
        assert invalid.status_code == 400

    with allure.step("上传 PNG 并确认静态资源可读取"):
        uploaded = http.post(
            f"{api_base_url}/account/uploadAvatar",
            headers=headers,
            files={"file": ("avatar.png", BytesIO(image_bytes), "image/png")},
            timeout=10,
        )
        assert uploaded.status_code == 200, uploaded.text
        avatar_url = uploaded.json()["avatar_url"]
        assert "/static/avatars/" in avatar_url

        image_response = http.get(avatar_url, timeout=10)
        assert image_response.status_code == 200
        assert image_response.content == image_bytes

        profile = http.post(
            f"{api_base_url}/account/findByID",
            json={"id": login["account_id"]},
            timeout=10,
        )
        assert profile.status_code == 200, profile.text
        assert profile.json()["avatar_url"] == avatar_url

    with allure.step("拒绝超过 10 MiB 的图片"):
        too_large = http.post(
            f"{api_base_url}/account/uploadAvatar",
            headers=headers,
            files={
                "file": (
                    "large.png",
                    BytesIO(b"\x00" * ((10 << 20) + 1)),
                    "image/png",
                )
            },
            timeout=30,
        )
        assert too_large.status_code == 400
