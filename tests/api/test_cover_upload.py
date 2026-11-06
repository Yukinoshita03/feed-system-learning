from __future__ import annotations

from pathlib import Path
import re
from urllib.parse import urlparse

import allure
import pytest


FIXTURES = Path(__file__).resolve().parents[1] / "fixtures"


@pytest.fixture
def cover_owner(create_account, login_account):
    login = login_account(create_account())
    return login["account_id"], {"Authorization": f"Bearer {login['token']}"}


@allure.feature("封面上传")
@pytest.mark.parametrize("filename, fixture, extension, content_type", [
    ("cover.JPG", "cover.jpg", "jpg", "image/jpeg"),
    ("cover.jpeg", "cover.jpg", "jpeg", "image/jpeg"),
    ("cover.png", "cover.png", "png", "image/png"),
    ("cover.webp", "cover.webp", "webp", "image/webp"),
])
def test_cover_supported_formats(
    api_base_url, http, cover_owner, filename, fixture, extension, content_type
):
    owner_id, headers = cover_owner
    data = (FIXTURES / fixture).read_bytes()
    response = http.post(
        f"{api_base_url}/video/uploadCover",
        files={"file": (filename, data, "application/octet-stream")},
        headers=headers, timeout=10,
    )
    assert response.status_code == 200, response.text
    payload = response.json()
    assert payload["url"] == payload["cover_url"]
    assert payload["cover_url"].startswith(api_base_url + "/static/")
    url_path = urlparse(payload["cover_url"]).path
    assert re.fullmatch(rf"/static/covers/{owner_id}/[0-9a-f]{{32}}\.{extension}", url_path)
    downloaded = http.get(payload["cover_url"], timeout=10)
    assert downloaded.status_code == 200, downloaded.text
    assert downloaded.content == data
    assert downloaded.headers["Content-Type"].startswith(content_type)


@allure.feature("封面上传")
@allure.title("上传真实视频和封面后，可以通过原有发布接口创建记录")
def test_cover_upload_then_publish(api_base_url, http, cover_owner):
    owner_id, headers = cover_owner
    cover = (FIXTURES / "cover.png").read_bytes()
    urls = []
    with allure.step("同名封面每次生成独立地址，文件内容完整保存"):
        for _ in range(2):
            uploaded = http.post(
                f"{api_base_url}/video/uploadCover",
                files={"file": ("../../same-cover.PNG", cover, "image/png")},
                headers=headers, timeout=10,
            )
            assert uploaded.status_code == 200, uploaded.text
            urls.append(uploaded.json()["cover_url"])
        assert urls[0] != urls[1]
        for url in urls:
            saved = http.get(url, timeout=10)
            assert saved.status_code == 200, saved.text
            assert saved.content == cover

    with allure.step("上传封面不创建视频记录，也不修改账号头像"):
        listed = http.post(
            f"{api_base_url}/video/listByAuthorID", json={"author_id": owner_id}, timeout=10
        )
        assert listed.status_code == 200, listed.text
        assert listed.json() == []
        account = http.get(f"{api_base_url}/account/me", headers=headers, timeout=10)
        assert account.status_code == 200, account.text
        assert account.json().get("avatar_url", "") == ""

    with allure.step("上传 MP4，再提交真实的播放地址和封面地址"):
        video_upload = http.post(
            f"{api_base_url}/video/uploadVideo",
            files={"file": ("clip.mp4", (FIXTURES / "clip.mp4").read_bytes(), "video/mp4")},
            headers=headers, timeout=10,
        )
        assert video_upload.status_code == 200, video_upload.text
        created = http.post(
            f"{api_base_url}/video/publish",
            json={"title": "视频封面联调", "play_url": video_upload.json()["play_url"], "cover_url": urls[0]},
            headers=headers, timeout=10,
        )
        assert created.status_code == 200, created.text
        detail = http.post(
            f"{api_base_url}/video/getDetail", json={"id": created.json()["id"]}, timeout=10
        )
        assert detail.status_code == 200, detail.text
        assert detail.json()["cover_url"] == urls[0]
        assert detail.json()["play_url"] == video_upload.json()["play_url"]


@allure.feature("封面上传")
def test_cover_upload_requires_login(api_base_url, http):
    response = http.post(
        f"{api_base_url}/video/uploadCover",
        files={"file": ("cover.png", (FIXTURES / "cover.png").read_bytes(), "image/png")},
        timeout=10,
    )
    assert response.status_code == 401, response.text


@allure.feature("封面上传")
def test_cover_upload_rejects_logged_out_token(api_base_url, http, cover_owner):
    _, headers = cover_owner
    logout = http.post(f"{api_base_url}/account/logout", headers=headers, timeout=10)
    assert logout.status_code == 200, logout.text
    response = http.post(
        f"{api_base_url}/video/uploadCover",
        files={"file": ("cover.png", (FIXTURES / "cover.png").read_bytes(), "image/png")},
        headers=headers, timeout=10,
    )
    assert response.status_code == 401, response.text


@allure.feature("封面上传")
@pytest.mark.parametrize("filename, fixture, data", [
    ("fake.png", None, b"<script>alert(1)</script>"),
    ("empty.png", None, b""),
    ("mismatch.jpg", "cover.png", None),
    ("unsupported.gif", None, b"GIF89a" + b"\x00" * 20),
    ("video.png", "clip.mp4", None),
])
def test_cover_upload_rejects_invalid_files(
    api_base_url, http, cover_owner, filename, fixture, data
):
    _, headers = cover_owner
    content = (FIXTURES / fixture).read_bytes() if fixture is not None else data
    response = http.post(
        f"{api_base_url}/video/uploadCover",
        files={"file": (filename, content, "image/png")}, headers=headers, timeout=10,
    )
    assert response.status_code == 400, response.text


@allure.feature("封面上传")
@pytest.mark.parametrize("mode", ["wrong-field", "json", "malformed-multipart"])
def test_cover_upload_rejects_wrong_request(api_base_url, http, cover_owner, mode):
    _, headers = cover_owner
    if mode == "wrong-field":
        arguments = {"files": {"cover": ("cover.png", (FIXTURES / "cover.png").read_bytes(), "image/png")}}
    elif mode == "json":
        arguments = {"json": {"file": "cover.png"}}
    else:
        headers = {**headers, "Content-Type": "multipart/form-data; boundary=missing"}
        arguments = {"data": b"broken multipart"}
    response = http.post(
        f"{api_base_url}/video/uploadCover", headers=headers, timeout=10, **arguments
    )
    assert response.status_code == 400, response.text


@allure.feature("封面上传")
def test_cover_upload_rejects_oversized_file(api_base_url, http, cover_owner):
    _, headers = cover_owner
    data = (FIXTURES / "cover.png").read_bytes() + b"x" * (10 << 20)
    response = http.post(
        f"{api_base_url}/video/uploadCover",
        files={"file": ("large.png", data, "image/png")}, headers=headers, timeout=10,
    )
    assert response.status_code == 400, response.text
    assert response.json()["error"] == "封面文件不能为空且不能超过 10 MiB"


@allure.feature("封面上传")
def test_cover_upload_uses_authenticated_account_directory(
    api_base_url, http, cover_owner, create_account, login_account
):
    first_id, first_headers = cover_owner
    second = login_account(create_account())
    for account_id, headers in (
        (first_id, first_headers),
        (second["account_id"], {"Authorization": f"Bearer {second['token']}"}),
    ):
        uploaded = http.post(
            f"{api_base_url}/video/uploadCover",
            data={"author_id": "999999"},
            files={"file": ("cover.png", (FIXTURES / "cover.png").read_bytes(), "image/png")},
            headers=headers, timeout=10,
        )
        assert uploaded.status_code == 200, uploaded.text
        assert urlparse(uploaded.json()["cover_url"]).path.startswith(f"/static/covers/{account_id}/")
