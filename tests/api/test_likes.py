from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
import json

import allure
import pytest
import requests


pytestmark = allure.feature("视频点赞")


def post(http, base, endpoint, body=None, headers=None):
    response = http.post(base + endpoint, json={} if body is None else body, headers=headers, timeout=15)
    assert response.status_code == 200, response.text
    return response.json()


@pytest.fixture
def like_video(api_base_url, http, create_account, login_account):
    owner = login_account(create_account())
    headers = {"Authorization": f"Bearer {owner['token']}"}
    item = post(http, api_base_url, "/video/publish", {
        "title": "点赞接口测试", "play_url": "/static/like.mp4", "cover_url": "/static/like.png",
    }, headers)
    yield item, headers
    http.post(api_base_url + "/video/delete", json={"id": item["id"]}, headers=headers, timeout=10)


@pytest.mark.parametrize("endpoint", ["like", "unlike", "isLiked", "listMyLikedVideos"])
def test_like_routes_require_login(api_base_url, http, endpoint):
    response = http.post(api_base_url + "/like/" + endpoint, json={"video_id": 1}, timeout=10)
    assert response.status_code == 401, response.text


@pytest.mark.parametrize("endpoint", ["like", "unlike", "isLiked"])
@pytest.mark.parametrize("body", [{}, {"video_id": -1}, {"video_id": "invalid"}])
def test_like_invalid_video_id(api_base_url, http, like_video, endpoint, body):
    _, headers = like_video
    response = http.post(api_base_url + "/like/" + endpoint, json=body, headers=headers, timeout=10)
    assert response.status_code == 400, response.text


@pytest.mark.parametrize("endpoint", ["like", "unlike", "isLiked"])
def test_like_missing_video(api_base_url, http, like_video, endpoint):
    _, headers = like_video
    response = http.post(api_base_url + "/like/" + endpoint, json={"video_id": 4294967295}, headers=headers, timeout=10)
    assert response.status_code == 404, response.text


@allure.title("重复点赞和取消保持幂等，Feed 按用户返回点赞状态")
def test_like_lifecycle_and_private_feed_state(api_base_url, http, like_video, create_account, login_account):
    item, a = like_video
    other = login_account(create_account())
    b = {"Authorization": f"Bearer {other['token']}"}
    body = {"video_id": item["id"]}
    with allure.step("未点赞时重复取消，数量保持 0"):
        for _ in range(2):
            result = post(http, api_base_url, "/like/unlike", body, a)
            assert result["is_liked"] is False and result["likes_count"] == 0
    with allure.step("重复点赞只计一次，首次点赞时间保持不变"):
        for _ in range(2):
            result = post(http, api_base_url, "/like/like", {**body, "account_id": other["account_id"]}, a)
            assert result["is_liked"] is True and result["likes_count"] == 1
        assert post(http, api_base_url, "/like/isLiked", body, a) == {"is_liked": True}
        assert post(http, api_base_url, "/like/isLiked", {**body, "account_id": item["author_id"]}, b) == {"is_liked": False}
        assert post(http, api_base_url, "/like/listMyLikedVideos", {}, b) == []
        assert [video["id"] for video in post(http, api_base_url, "/like/listMyLikedVideos", {}, a)] == [item["id"]]
    with allure.step("同一公共 Feed 缓存对游客、点赞人和其他账号返回不同的个人状态"):
        for headers, liked in ((None, False), (a, True), (b, False), (None, False), (a, True)):
            page = post(http, api_base_url, "/feed/listLatest", {}, headers)
            video = next(video for video in page["video_list"] if video["id"] == item["id"])
            assert video["likes_count"] == 1
            assert video["is_liked"] is liked
        detail = post(http, api_base_url, "/video/getDetail", {"id": item["id"]})
        assert detail["likes_count"] == 1
        assert detail["create_time"] == item["create_time"]
    with allure.step("第二个账号独立点赞；取消不影响其他账号的记录"):
        result = post(http, api_base_url, "/like/like", {**body, "account_id": item["author_id"]}, b)
        assert result["likes_count"] == 2
        for _ in range(2):
            result = post(http, api_base_url, "/like/unlike", body, a)
            assert result["is_liked"] is False and result["likes_count"] == 1
        assert post(http, api_base_url, "/like/isLiked", body, b)["is_liked"] is True
        result = post(http, api_base_url, "/like/unlike", body, b)
        assert result["likes_count"] == 0
        assert post(http, api_base_url, "/like/listMyLikedVideos", {}, a) == []
        assert post(http, api_base_url, "/like/listMyLikedVideos", {}, b) == []


def test_liked_list_order_and_video_deletion(api_base_url, http, like_video):
    first, headers = like_video
    second = post(http, api_base_url, "/video/publish", {
        "title": "第二条点赞视频", "play_url": "/static/like2.mp4", "cover_url": "/static/like2.png",
    }, headers)
    try:
        for video in (first, second, first):
            post(http, api_base_url, "/like/like", {"video_id": video["id"]}, headers)
        listed = post(http, api_base_url, "/like/listMyLikedVideos", {"account_id": 999999}, headers)
        assert [video["id"] for video in listed] == [second["id"], first["id"]]
        post(http, api_base_url, "/video/delete", {"id": second["id"]}, headers)
        assert [video["id"] for video in post(http, api_base_url, "/like/listMyLikedVideos", {}, headers)] == [first["id"]]
        for endpoint in ("like", "unlike", "isLiked"):
            response = http.post(api_base_url + "/like/" + endpoint, json={"video_id": second["id"]}, headers=headers, timeout=10)
            assert response.status_code == 404, response.text
    finally:
        http.post(api_base_url + "/video/delete", json={"id": second["id"]}, headers=headers, timeout=10)


def test_like_updates_detail_cache_without_caching_personal_state(api_base_url, http, like_video, redis_command):
    item, headers = like_video
    detail_key = f"video:detail:{item['id']}"
    author_key = f"video:author:{item['author_id']}"
    post(http, api_base_url, "/video/getDetail", {"id": item["id"]})
    post(http, api_base_url, "/video/listByAuthorID", {"author_id": item["author_id"]})
    post(http, api_base_url, "/feed/listLatest")
    ids = redis_command("GET", "feed:latest:first:10")
    author_ids = redis_command("GET", author_key)
    post(http, api_base_url, "/like/like", {"video_id": item["id"]}, headers)
    assert redis_command("GET", detail_key) is None
    assert redis_command("GET", "feed:latest:first:10") == ids
    assert redis_command("GET", author_key) == author_ids
    detail = post(http, api_base_url, "/video/getDetail", {"id": item["id"]})
    assert detail["likes_count"] == 1
    cached = json.loads(redis_command("GET", detail_key))
    assert cached["likes_count"] == 1 and "is_liked" not in cached
    post(http, api_base_url, "/like/unlike", {"video_id": item["id"]}, headers)
    assert post(http, api_base_url, "/video/getDetail", {"id": item["id"]})["likes_count"] == 0


def test_concurrent_http_like_requests_are_idempotent(api_base_url, http, like_video):
    item, headers = like_video
    def request(endpoint):
        response = requests.post(api_base_url + "/like/" + endpoint, json={"video_id": item["id"]}, headers=headers, timeout=15)
        assert response.status_code == 200, response.text
        return response.json()
    for endpoint, count in (("like", 1), ("unlike", 0)):
        with ThreadPoolExecutor(max_workers=8) as workers:
            results = list(workers.map(request, [endpoint] * 16))
        assert all(result["likes_count"] == count for result in results)
        assert post(http, api_base_url, "/video/getDetail", {"id": item["id"]})["likes_count"] == count
        assert post(http, api_base_url, "/like/isLiked", {"video_id": item["id"]}, headers)["is_liked"] is bool(count)


def test_revoked_token_is_not_used_as_feed_identity(api_base_url, http, like_video, create_account, login_account):
    item, _ = like_video
    actor = login_account(create_account())
    headers = {"Authorization": "Bearer " + actor["token"]}
    post(http, api_base_url, "/account/logout", {}, headers)
    for endpoint, body in (("/feed/listLatest", {}), ("/like/like", {"video_id": item["id"]})):
        response = http.post(api_base_url + endpoint, json=body, headers=headers, timeout=10)
        assert response.status_code == 401, response.text
    assert isinstance(post(http, api_base_url, "/feed/listLatest")["video_list"], list)
