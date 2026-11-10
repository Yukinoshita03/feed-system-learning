from concurrent.futures import ThreadPoolExecutor

import allure
import pytest
import requests

from test_comments import call

pytestmark = [allure.feature("收藏 CRUD")]


@pytest.fixture
def favorite_context(api_base_url, http, create_account, login_account):
    owner, other = (login_account(create_account()) for _ in range(2))
    headers = {"Authorization": "Bearer " + owner["token"]}
    other_headers = {"Authorization": "Bearer " + other["token"]}
    videos = [call(http, api_base_url, "/video/publish", {
        "title": f"收藏视频 {i}", "play_url": "/static/clip.mp4", "cover_url": "/static/cover.png",
    }, headers) for i in range(3)]
    yield videos, headers, other_headers
    for video in videos:
        http.post(api_base_url + "/video/delete", json={"id": video["id"]}, headers=headers, timeout=10)


@pytest.mark.parametrize("endpoint", ["favorite", "unfavorite", "isFavorited", "listMyFavorites"])
def test_favorites_require_login(api_base_url, http, endpoint):
    call(http, api_base_url, "/favorite/" + endpoint, {"video_id": 1}, status=401)


def test_favorite_lifecycle_isolation_and_cache(api_base_url, http, favorite_context, redis_command):
    videos, actor, other = favorite_context
    video = videos[0]
    body = {"video_id": video["id"], "account_id": 999999}
    call(http, api_base_url, "/video/getDetail", {"id": video["id"]})
    for _ in range(2):
        assert call(http, api_base_url, "/favorite/favorite", body, actor) == {"is_favorited": True, "favorites_count": 1}
    assert redis_command("GET", f"video:detail:{video['id']}") is None
    assert call(http, api_base_url, "/favorite/isFavorited", body, actor)["is_favorited"]
    assert not call(http, api_base_url, "/favorite/isFavorited", body, other)["is_favorited"]
    assert call(http, api_base_url, "/favorite/listMyFavorites", {"account_id": video["author_id"]}, other)["video_list"] == []
    assert call(http, api_base_url, "/video/getDetail", {"id": video["id"]})["favorites_count"] == 1
    for _ in range(2):
        assert call(http, api_base_url, "/favorite/unfavorite", body, actor) == {"is_favorited": False, "favorites_count": 0}
    assert call(http, api_base_url, "/favorite/listMyFavorites", {}, actor)["video_list"] == []


def test_favorite_pagination_uses_favorite_order_and_removes_deleted_video(api_base_url, http, favorite_context):
    videos, actor, _ = favorite_context
    for video in videos[::-1]:
        call(http, api_base_url, "/favorite/favorite", {"video_id": video["id"]}, actor)
    call(http, api_base_url, "/favorite/favorite", {"video_id": videos[-1]["id"]}, actor)
    first = call(http, api_base_url, "/favorite/listMyFavorites", {"limit": 2}, actor)
    assert [v["id"] for v in first["video_list"]] == [v["id"] for v in videos[:2]]
    assert first["has_more"]
    second = call(http, api_base_url, "/favorite/listMyFavorites", {"limit": 2, "before_id": first["next_id"]}, actor)
    assert [v["id"] for v in second["video_list"]] == [videos[-1]["id"]]
    assert not second["has_more"]
    call(http, api_base_url, "/video/delete", {"id": videos[-1]["id"]}, actor)
    assert not call(http, api_base_url, "/favorite/listMyFavorites", {"before_id": first["next_id"]}, actor)["video_list"]
    for endpoint in ("favorite", "unfavorite", "isFavorited"):
        call(http, api_base_url, "/favorite/" + endpoint, {"video_id": videos[-1]["id"]}, actor, 404)


def test_concurrent_favorite_retries_do_not_duplicate_count(api_base_url, http, favorite_context):
    videos, actor, _ = favorite_context
    body = {"video_id": videos[0]["id"]}
    for endpoint, expected in (("favorite", 1), ("unfavorite", 0)):
        with ThreadPoolExecutor(max_workers=6) as pool:
            results = list(pool.map(lambda _: call(requests, api_base_url, "/favorite/" + endpoint, body, actor), range(12)))
        assert all(result["favorites_count"] == expected for result in results)
        assert call(http, api_base_url, "/video/getDetail", {"id": videos[0]["id"]})["favorites_count"] == expected


@pytest.mark.parametrize("body", [{"limit": 101}, {"limit": -1}, {"before_id": -1}])
def test_invalid_favorite_page(api_base_url, http, favorite_context, body):
    _, actor, _ = favorite_context
    call(http, api_base_url, "/favorite/listMyFavorites", body, actor, 400)


@pytest.mark.parametrize("body", [{}, {"video_id": 0}, {"video_id": -1}, {"video_id": "oops"}])
def test_invalid_favorite_video_id(api_base_url, http, favorite_context, body):
    _, actor, _ = favorite_context
    for endpoint in ("favorite", "unfavorite", "isFavorited"):
        call(http, api_base_url, "/favorite/" + endpoint, body, actor, 400)
