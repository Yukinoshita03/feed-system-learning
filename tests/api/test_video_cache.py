from __future__ import annotations

import json
import time
from uuid import uuid4

import allure
import pytest


pytestmark = allure.feature("视频与 Feed Redis 缓存")
FEED_KEY = "feed:latest:first:10"


@pytest.fixture
def cached_video(api_base_url, http, create_account, login_account):
    account = create_account()
    login = login_account(account)
    headers = {"Authorization": f"Bearer {login['token']}"}
    response = http.post(
        f"{api_base_url}/video/publish", headers=headers,
        json={"title": "缓存原始标题", "play_url": "/static/cache.mp4", "cover_url": "/static/cache.png"}, timeout=10,
    )
    assert response.status_code == 200, response.text
    video = response.json()
    yield video, headers
    http.post(f"{api_base_url}/video/delete", json={"id": video["id"]}, headers=headers, timeout=10)


def read(http, api_base_url, endpoint, body):
    response = http.post(api_base_url + endpoint, json=body, timeout=10)
    assert response.status_code == 200, response.text
    return response.json()


def warm(http, api_base_url, video):
    read(http, api_base_url, "/video/getDetail", {"id": video["id"]})
    read(http, api_base_url, "/video/listByAuthorID", {"author_id": video["author_id"]})
    read(http, api_base_url, "/feed/listLatest", {})


@allure.title("视频详情、作者作品及首页缓存使用独立 key 和有效 TTL")
def test_video_and_list_cache_values(api_base_url, http, cached_video, redis_command):
    video, _ = cached_video
    warm(http, api_base_url, video)
    detail_key, author_key = f"video:detail:{video['id']}", f"video:author:{video['author_id']}"
    assert json.loads(redis_command("GET", detail_key)) == video
    assert json.loads(redis_command("GET", author_key)) == {"ids": [video["id"]]}
    first = json.loads(redis_command("GET", FEED_KEY))
    assert video["id"] in first["ids"]
    assert len(first["ids"]) <= 11
    for key in (detail_key, author_key, FEED_KEY):
        assert redis_command("PTTL", key) > 0
        assert redis_command("PTTL", "cache:version:" + key) > redis_command("PTTL", key)
    assert "is_liked" not in first
    assert "author" not in first


@allure.title("修改详情后列表 ID 缓存仍可复用，列表内容反映新标题")
def test_video_update_invalidates_detail(api_base_url, http, cached_video, redis_command):
    video, headers = cached_video
    warm(http, api_base_url, video)
    updated = {**video, "title": "缓存更新后的标题"}
    response = http.post(f"{api_base_url}/video/update", json=updated, headers=headers, timeout=10)
    assert response.status_code == 200, response.text
    assert redis_command("GET", f"video:detail:{video['id']}") is None
    assert redis_command("GET", f"video:author:{video['author_id']}") is not None
    assert redis_command("GET", FEED_KEY) is not None
    listed = read(http, api_base_url, "/video/listByAuthorID", {"author_id": video["author_id"]})
    assert listed == [updated]
    feed = read(http, api_base_url, "/feed/listLatest", {})
    assert next(item for item in feed["video_list"] if item["id"] == video["id"])["title"] == updated["title"]


@allure.title("删除视频后清除详情、作者列表和首页缓存")
def test_video_delete_invalidates_all_lists(api_base_url, http, cached_video, redis_command):
    video, headers = cached_video
    warm(http, api_base_url, video)
    response = http.post(f"{api_base_url}/video/delete", json={"id": video["id"]}, headers=headers, timeout=10)
    assert response.status_code == 200, response.text
    for key in (f"video:detail:{video['id']}", f"video:author:{video['author_id']}", FEED_KEY):
        assert redis_command("GET", key) is None
    assert read(http, api_base_url, "/video/listByAuthorID", {"author_id": video["author_id"]}) == []
    detail = http.post(f"{api_base_url}/video/getDetail", json={"id": video["id"]}, timeout=10)
    assert detail.status_code == 404, detail.text
    assert video["id"] not in [item["id"] for item in read(http, api_base_url, "/feed/listLatest", {})["video_list"]]


@allure.title("详情缓存过期或内容损坏时回源数据库并修复缓存")
@pytest.mark.parametrize("mode", ["expired", "corrupt"])
def test_video_cache_expiry_and_corrupt_data(api_base_url, http, cached_video, redis_command, mode):
    video, _ = cached_video
    key = f"video:detail:{video['id']}"
    read(http, api_base_url, "/video/getDetail", {"id": video["id"]})
    if mode == "expired":
        redis_command("PEXPIRE", key, 1)
        deadline = time.monotonic() + 1
        while redis_command("EXISTS", key) and time.monotonic() < deadline:
            time.sleep(0.005)
        assert redis_command("EXISTS", key) == 0
    else:
        redis_command("SET", key, "not-json", "PX", 60000)
    assert read(http, api_base_url, "/video/getDetail", {"id": video["id"]}) == video
    assert json.loads(redis_command("GET", key)) == video


@allure.title("改名后复用首页 ID 缓存，作者用户名从数据库获取最新值")
def test_feed_cache_does_not_freeze_author_name(api_base_url, http, cached_video, redis_command):
    video, headers = cached_video
    warm(http, api_base_url, video)
    ids_before = redis_command("GET", FEED_KEY)
    name = "renamed_" + uuid4().hex[:12]
    response = http.post(
        f"{api_base_url}/account/rename", json={"new_username": name}, headers=headers, timeout=10,
    )
    assert response.status_code == 200, response.text
    headers["Authorization"] = "Bearer " + response.json()["token"]
    page = read(http, api_base_url, "/feed/listLatest", {})
    assert redis_command("GET", FEED_KEY) == ids_before
    assert next(item for item in page["video_list"] if item["id"] == video["id"])["author"]["username"] == name


@allure.title("发布视频使原有空作品列表和首页缓存失效")
def test_publish_invalidates_empty_author_list(api_base_url, http, create_account, login_account, redis_command):
    login = login_account(create_account())
    author_key = f"video:author:{login['account_id']}"
    assert read(http, api_base_url, "/video/listByAuthorID", {"author_id": login["account_id"]}) == []
    assert json.loads(redis_command("GET", author_key)) == {"ids": []}
    read(http, api_base_url, "/feed/listLatest", {})
    response = http.post(
        f"{api_base_url}/video/publish", headers={"Authorization": f"Bearer {login['token']}"},
        json={"title": "首条作品", "play_url": "/static/new.mp4", "cover_url": "/static/new.png"}, timeout=10,
    )
    assert response.status_code == 200, response.text
    assert redis_command("GET", author_key) is None
    assert redis_command("GET", FEED_KEY) is None
    listed = read(http, api_base_url, "/video/listByAuthorID", {"author_id": login["account_id"]})
    assert [item["id"] for item in listed] == [response.json()["id"]]
