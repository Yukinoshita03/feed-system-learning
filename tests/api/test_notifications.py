from concurrent.futures import ThreadPoolExecutor

import allure
import pytest
import requests

from test_comments import call

pytestmark = [allure.feature("互动通知 CRUD")]


@pytest.fixture
def notification_context(api_base_url, http, create_account, login_account):
    owner, actor, outsider = (login_account(create_account()) for _ in range(3))
    headers = [{"Authorization": "Bearer " + u["token"]} for u in (owner, actor, outsider)]
    video = call(http, api_base_url, "/video/publish", {
        "title": "通知测试", "play_url": "/static/clip.mp4", "cover_url": "/static/cover.png",
    }, headers[0])
    yield video, owner, actor, headers
    http.post(api_base_url + "/video/delete", json={"id": video["id"]}, headers=headers[0], timeout=10)


@pytest.mark.parametrize("endpoint", ["list", "unreadCount", "markRead", "delete"])
def test_notifications_require_login(api_base_url, http, endpoint):
    call(http, api_base_url, "/notification/" + endpoint, status=401)


def test_notification_generation_dedup_read_delete_and_isolation(api_base_url, http, notification_context):
    video, owner, actor, (oh, ah, xh) = notification_context
    assert call(http, api_base_url, "/notification/list", {}, oh)["messages"] == []
    for _ in range(2):
        call(http, api_base_url, "/like/like", {"video_id": video["id"]}, ah)
        call(http, api_base_url, "/favorite/favorite", {"video_id": video["id"]}, ah)
        call(http, api_base_url, "/social/follow", {"vlogger_id": owner["account_id"]}, ah)
    call(http, api_base_url, "/comment/publish", {"video_id": video["id"], "content": "通知里的评论"}, ah, 201)
    messages = call(http, api_base_url, "/notification/list", {}, oh)["messages"]
    assert len(messages) == 4 and {m["kind"] for m in messages} == {"like", "favorite", "follow", "comment"}
    assert all(m["receiver_id"] == owner["account_id"] and m["actor_id"] == actor["account_id"] and m["actor_username"] == actor["username"] for m in messages)
    assert all(not {"event_key", "password", "access_token_hash"} & m.keys() for m in messages)
    assert call(http, api_base_url, "/notification/unreadCount", {}, oh)["unread_count"] == 4
    assert call(http, api_base_url, "/notification/list", {"receiver_id": owner["account_id"]}, xh)["messages"] == []
    target = messages[0]["id"]
    assert call(http, api_base_url, "/notification/markRead", {"message_ids": [target]}, xh)["updated_count"] == 0
    call(http, api_base_url, "/notification/delete", {"message_id": target}, xh, 404)
    assert call(http, api_base_url, "/notification/markRead", {"message_ids": [target, target]}, oh)["updated_count"] == 1
    assert call(http, api_base_url, "/notification/markRead", {"message_ids": [target]}, oh)["updated_count"] == 0
    assert call(http, api_base_url, "/notification/unreadCount", {}, oh)["unread_count"] == 3
    assert len(call(http, api_base_url, "/notification/list", {"unread_only": True}, oh)["messages"]) == 3
    call(http, api_base_url, "/notification/delete", {"message_id": target}, oh)
    call(http, api_base_url, "/notification/delete", {"message_id": target}, oh, 404)
    assert call(http, api_base_url, "/notification/markRead", {"all": True}, oh)["updated_count"] == 3
    assert call(http, api_base_url, "/notification/unreadCount", {}, oh)["unread_count"] == 0


def test_self_interactions_do_not_notify_self(api_base_url, http, notification_context):
    video, _, _, (oh, _, _) = notification_context
    for endpoint in ("/like/like", "/favorite/favorite"):
        call(http, api_base_url, endpoint, {"video_id": video["id"]}, oh)
    call(http, api_base_url, "/comment/publish", {"video_id": video["id"], "content": "自己评论"}, oh, 201)
    assert call(http, api_base_url, "/notification/list", {}, oh)["messages"] == []


def test_notification_pagination_and_repeated_like_after_unlike(api_base_url, http, notification_context):
    video, _, _, (oh, ah, _) = notification_context
    for _ in range(3):
        call(http, api_base_url, "/like/like", {"video_id": video["id"]}, ah)
        call(http, api_base_url, "/like/unlike", {"video_id": video["id"]}, ah)
    first = call(http, api_base_url, "/notification/list", {"limit": 2}, oh)
    assert first["has_more"] and len(first["messages"]) == 2
    second = call(http, api_base_url, "/notification/list", {"limit": 2, "before_id": first["next_id"]}, oh)
    assert not second["has_more"] and len(second["messages"]) == 1
    assert len({m["id"] for m in first["messages"] + second["messages"]}) == 3


def test_concurrent_like_retries_create_one_notification(api_base_url, http, notification_context):
    video, _, _, (oh, ah, _) = notification_context
    with ThreadPoolExecutor(max_workers=6) as pool:
        list(pool.map(lambda _: call(requests, api_base_url, "/like/like", {"video_id": video["id"]}, ah), range(12)))
    assert call(http, api_base_url, "/notification/unreadCount", {}, oh)["unread_count"] == 1


@pytest.mark.parametrize("body", [{}, {"message_ids": []}, {"message_ids": [0]}, {"message_ids": [-1]}, {"message_ids": [1] * 101}, {"all": True, "message_ids": [1]}])
def test_invalid_read_request(api_base_url, http, notification_context, body):
    _, _, _, (oh, _, _) = notification_context
    call(http, api_base_url, "/notification/markRead", body, oh, 400)


@pytest.mark.parametrize("body", [{"limit": 101}, {"limit": -1}, {"before_id": -1}, {"unread_only": "wrong"}])
def test_invalid_notification_page(api_base_url, http, notification_context, body):
    _, _, _, (oh, _, _) = notification_context
    call(http, api_base_url, "/notification/list", body, oh, 400)
