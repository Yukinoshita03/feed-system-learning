from __future__ import annotations

from pathlib import Path
import re
from urllib.parse import urlparse

import allure
import pytest


CLIP = Path(__file__).resolve().parents[1] / "fixtures" / "clip.mp4"
MAX_VIDEO_BYTES = 200 << 20


@pytest.fixture
def upload_owner(create_account, login_account):
    login = login_account(create_account())
    return login["account_id"], {"Authorization": f"Bearer {login['token']}"}


@allure.feature("普通视频上传")
@allure.title("保存 MP4 后可通过返回地址读取，之后可以独立发布视频记录")
def test_upload_video_then_publish(api_base_url, http, upload_owner):
    owner_id, headers = upload_owner
    original = CLIP.read_bytes()
    urls = []
    with allure.step("重复上传同名文件，生成不同的随机文件名"):
        for _ in range(2):
            uploaded = http.post(
                f"{api_base_url}/video/uploadVideo",
                files={"file": ("../../example.MP4", original, "application/octet-stream")},
                headers=headers, timeout=10,
            )
            assert uploaded.status_code == 200, uploaded.text
            body = uploaded.json()
            assert body["url"] == body["play_url"]
            assert body["play_url"].startswith(api_base_url + "/static/")
            url_path = urlparse(body["play_url"]).path
            assert re.fullmatch(rf"/static/videos/{owner_id}/[0-9a-f]{{32}}\.mp4", url_path)
            urls.append(body["play_url"])
        assert urls[0] != urls[1]

    with allure.step("文件内容与原文件一致，支持播放器使用的 Range 请求"):
        for url in urls:
            downloaded = http.get(url, timeout=10)
            assert downloaded.status_code == 200, downloaded.text
            assert downloaded.content == original
            assert downloaded.headers["Content-Type"].startswith("video/mp4")
        ranged = http.get(urls[0], headers={"Range": "bytes=0-15"}, timeout=10)
        assert ranged.status_code == 206
        assert ranged.content == original[:16]
        assert ranged.headers["Content-Range"] == f"bytes 0-15/{len(original)}"

    with allure.step("上传阶段不创建数据库视频记录"):
        listed = http.post(
            f"{api_base_url}/video/listByAuthorID", json={"author_id": owner_id}, timeout=10
        )
        assert listed.status_code == 200, listed.text
        assert listed.json() == []

    with allure.step("把上传所得地址交给已有发布接口"):
        created = http.post(
            f"{api_base_url}/video/publish",
            json={"title": "真实文件发布", "play_url": urls[0], "cover_url": "/static/covers/example.png"},
            headers=headers, timeout=10,
        )
        assert created.status_code == 200, created.text
        assert created.json()["play_url"] == urls[0]
        assert created.json()["author_id"] == owner_id


@allure.feature("普通视频上传")
def test_upload_video_requires_login(api_base_url, http):
    response = http.post(
        f"{api_base_url}/video/uploadVideo",
        files={"file": ("clip.mp4", CLIP.read_bytes(), "video/mp4")}, timeout=10,
    )
    assert response.status_code == 401, response.text


@allure.feature("普通视频上传")
@pytest.mark.parametrize("filename, data, content_type", [
    ("fake.mp4", b"not an mp4", "video/mp4"),
    ("empty.mp4", b"", "video/mp4"),
    ("wrong.txt", None, "video/mp4"),
])
def test_upload_video_rejects_invalid_file(
    api_base_url, http, upload_owner, filename, data, content_type
):
    _, headers = upload_owner
    response = http.post(
        f"{api_base_url}/video/uploadVideo",
        files={"file": (filename, CLIP.read_bytes() if data is None else data, content_type)},
        headers=headers, timeout=10,
    )
    assert response.status_code == 400, response.text


@allure.feature("普通视频上传")
@pytest.mark.parametrize("mode", ["wrong-field", "json", "malformed-multipart"])
def test_upload_video_rejects_wrong_request(api_base_url, http, upload_owner, mode):
    _, headers = upload_owner
    if mode == "wrong-field":
        arguments = {"files": {"video": ("clip.mp4", CLIP.read_bytes(), "video/mp4")}}
    elif mode == "json":
        arguments = {"json": {"file": "clip.mp4"}}
    else:
        headers = {**headers, "Content-Type": "multipart/form-data; boundary=missing-boundary"}
        arguments = {"data": b"broken multipart body"}
    response = http.post(
        f"{api_base_url}/video/uploadVideo", headers=headers, timeout=10, **arguments
    )
    assert response.status_code == 400, response.text


@allure.feature("普通视频上传")
@allure.title("实际上传超过 200 MiB 的文件被拒绝")
def test_upload_video_rejects_oversized_file(api_base_url, http, upload_owner):
    _, headers = upload_owner
    boundary = "feed-video-upload-test-boundary"
    clip = CLIP.read_bytes()

    def multipart():
        yield (
            f"--{boundary}\r\n"
            'Content-Disposition: form-data; name="file"; filename="large.mp4"\r\n'
            "Content-Type: video/mp4\r\n\r\n"
        ).encode()
        yield clip
        # 流式生成 200 MiB + 1 字节，不在 Python 内存中构造整个大文件。
        remaining = MAX_VIDEO_BYTES + 1 - len(clip)
        chunk = b"x" * (64 << 10)
        while remaining:
            part = chunk[:min(remaining, len(chunk))]
            yield part
            remaining -= len(part)
        yield f"\r\n--{boundary}--\r\n".encode()

    response = http.post(
        f"{api_base_url}/video/uploadVideo",
        data=multipart(), headers={**headers, "Content-Type": f"multipart/form-data; boundary={boundary}"},
        timeout=30,
    )
    assert response.status_code == 400, response.text
    assert response.json()["error"] == "视频文件不能为空且不能超过 200 MiB"


@allure.feature("普通视频上传")
def test_upload_video_rejects_logged_out_token(api_base_url, http, upload_owner):
    _, headers = upload_owner
    logged_out = http.post(f"{api_base_url}/account/logout", headers=headers, timeout=10)
    assert logged_out.status_code == 200, logged_out.text
    response = http.post(
        f"{api_base_url}/video/uploadVideo",
        files={"file": ("clip.mp4", CLIP.read_bytes(), "video/mp4")},
        headers=headers, timeout=10,
    )
    assert response.status_code == 401, response.text
