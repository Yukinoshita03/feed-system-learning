from concurrent.futures import ThreadPoolExecutor

import allure
import pytest
import requests

pytestmark = [allure.feature("评论 CRUD")]


def call(http, base, path, data=None, headers=None, status=200):
    result = http.post(base + path, json=data or {}, headers=headers or {}, timeout=15)
    assert result.status_code == status, result.text
    return result.json()


@pytest.fixture
def comment_context(api_base_url, http, create_account, login_account):
    owner, other = (login_account(create_account()) for _ in range(2))
    headers = {"Authorization": "Bearer " + owner["token"]}
    other_headers = {"Authorization": "Bearer " + other["token"]}
    video = call(http, api_base_url, "/video/publish", {
        "title": "评论测试", "play_url": "/static/clip.mp4", "cover_url": "/static/cover.png",
    }, headers)
    yield video, owner, headers, other, other_headers
    http.post(api_base_url + "/video/delete", json={"id": video["id"]}, headers=headers, timeout=10)


def test_comment_lifecycle_permissions_and_cache(api_base_url, http, comment_context, redis_command):
    video, owner, headers, _, other_headers = comment_context
    body = {"video_id": video["id"]}
    assert call(http, api_base_url, "/comment/list", body)["comments"] == []
    call(http, api_base_url, "/video/getDetail", {"id": video["id"]})
    response = call(http, api_base_url, "/comment/publish", {
        **body, "content": "  真实评论  ", "author_id": 999999,
    }, headers, 201)
    comment = response["comment"]
    assert comment["content"] == "真实评论" and comment["author_id"] == owner["account_id"]
    assert response["comments_count"] == 1
    assert redis_command("GET", f"video:detail:{video['id']}") is None
    listed = call(http, api_base_url, "/comment/list", body)
    assert listed["comments"][0]["username"] == owner["username"]
    assert not ({"password", "access_token_hash"} & listed["comments"][0].keys())
    call(http, api_base_url, "/comment/delete", {"comment_id": comment["id"]}, other_headers, 403)
    assert call(http, api_base_url, "/video/getDetail", {"id": video["id"]})["comments_count"] == 1
    assert call(http, api_base_url, "/comment/listAll", body)[0]["id"] == comment["id"]
    assert call(http, api_base_url, "/comment/delete", {"comment_id": comment["id"]}, headers)["comments_count"] == 0
    call(http, api_base_url, "/comment/delete", {"comment_id": comment["id"]}, headers, 404)
    assert call(http, api_base_url, "/comment/list", body)["comments"] == []
    assert call(http, api_base_url, "/video/getDetail", {"id": video["id"]})["comments_count"] == 0


def test_comment_pagination_is_stable_during_new_posts(api_base_url, http, comment_context):
    video, _, headers, _, _ = comment_context
    body = {"video_id": video["id"]}
    ids = [call(http, api_base_url, "/comment/publish", {**body, "content": f"评论 {i}"}, headers, 201)["comment"]["id"] for i in range(5)]
    first = call(http, api_base_url, "/comment/list", {**body, "limit": 2})
    assert [c["id"] for c in first["comments"]] == ids[-1:-3:-1]
    assert first["has_more"]
    call(http, api_base_url, "/comment/publish", {**body, "content": "分页中的新评论"}, headers, 201)
    seen = [c["id"] for c in first["comments"]]
    while first["has_more"]:
        first = call(http, api_base_url, "/comment/list", {**body, "limit": 2, "before_id": first["next_id"]})
        seen.extend(c["id"] for c in first["comments"])
    assert seen == ids[::-1]


@pytest.mark.parametrize("content", ["", "  \n\t", "中" * 1001, 23, None])
def test_invalid_comment_content(api_base_url, http, comment_context, content):
    video, _, headers, _, _ = comment_context
    call(http, api_base_url, "/comment/publish", {"video_id": video["id"], "content": content}, headers, 400)


@pytest.mark.parametrize("path,body", [
    ("/comment/publish", {"video_id": 1, "content": "hi"}),
    ("/comment/delete", {"comment_id": 1}),
])
def test_comment_writes_require_login(api_base_url, http, path, body):
    call(http, api_base_url, path, body, status=401)


@pytest.mark.parametrize("extra", [{"limit": -1}, {"limit": 101}, {"before_id": -1}, {"video_id": 0}])
def test_invalid_comment_page(api_base_url, http, comment_context, extra):
    video, *_ = comment_context
    call(http, api_base_url, "/comment/list", {"video_id": video["id"], **extra}, status=400)


def test_concurrent_comments_count_and_deleted_video(api_base_url, http, comment_context):
    video, _, headers, _, _ = comment_context
    def publish(i):
        return call(requests, api_base_url, "/comment/publish", {"video_id": video["id"], "content": str(i)}, headers, 201)
    with ThreadPoolExecutor(max_workers=6) as pool:
        results = list(pool.map(publish, range(12)))
    assert len({r["comment"]["id"] for r in results}) == 12
    assert call(http, api_base_url, "/video/getDetail", {"id": video["id"]})["comments_count"] == 12
    call(http, api_base_url, "/video/delete", {"id": video["id"]}, headers)
    call(http, api_base_url, "/comment/list", {"video_id": video["id"]}, status=404)
    call(http, api_base_url, "/comment/publish", {"video_id": video["id"], "content": "已删除"}, headers, 404)
    call(http, api_base_url, "/comment/delete", {"comment_id": results[0]["comment"]["id"]}, headers, 404)
