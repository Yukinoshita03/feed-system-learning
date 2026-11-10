from __future__ import annotations

import allure
import pytest


@allure.feature("最新视频流")
@pytest.mark.parametrize("body", [
    {"limit": -1},
    {"limit": 51},
    {"limit": "ten"},
    {"latest_time": -1, "latest_id": 1},
    {"latest_time": 1},
    {"latest_id": 1},
    {"latest_time": 1, "latest_id": -1},
    {"latest_time": 9223372036854775807, "latest_id": 1},
    [],
])
def test_feed_rejects_invalid_parameters(api_base_url, http, body):
    response = http.post(f"{api_base_url}/feed/listLatest", json=body, timeout=10)
    assert response.status_code == 400, response.text
    assert response.json()["error"]


@allure.feature("最新视频流")
@pytest.mark.parametrize("body", ["{", "not-json"])
def test_feed_rejects_malformed_json(api_base_url, http, body):
    response = http.post(
        f"{api_base_url}/feed/listLatest", data=body,
        headers={"Content-Type": "application/json"}, timeout=10,
    )
    assert response.status_code == 400, response.text
    assert response.json() == {"error": "请求格式不正确"}


@allure.feature("最新视频流")
def test_feed_empty_page_is_an_array(api_base_url, http):
    response = http.post(
        f"{api_base_url}/feed/listLatest",
        json={"limit": 10, "latest_time": 1, "latest_id": 1}, timeout=10,
    )
    assert response.status_code == 200, response.text
    assert response.json() == {
        "video_list": [], "next_time": 0, "next_id": 0, "has_more": False,
    }


@allure.feature("最新视频流")
@allure.title("发布视频后游客可浏览首页，并用时间和 ID 游标加载下一页")
def test_feed_publish_and_paginate(api_base_url, http, create_account, login_account):
    account = create_account()
    login = login_account(account)
    headers = {"Authorization": f"Bearer {login['token']}"}
    videos = []
    try:
        with allure.step("准备 13 条属于同一作者的视频"):
            for index in range(13):
                response = http.post(
                    f"{api_base_url}/video/publish", headers=headers,
                    json={"title": f"Feed API {index}", "play_url": "/static/test.mp4", "cover_url": "/static/test.png"},
                    timeout=10,
                )
                assert response.status_code == 200, response.text
                videos.append(response.json())
        expected_ids = [video["id"] for video in reversed(videos)]

        with allure.step("默认返回最新 10 条，读取视频流无需登录"):
            response = http.post(f"{api_base_url}/feed/listLatest", json={}, timeout=10)
            assert response.status_code == 200, response.text
            first = response.json()
            assert [item["id"] for item in first["video_list"]] == expected_ids[:10]
            assert first["has_more"] is True
            for item in first["video_list"]:
                assert item["author"] == {"id": login["account_id"], "username": account["username"]}
                assert item["create_time"] > 0
                assert item["likes_count"] == 0
                assert item["is_liked"] is False
                assert "password" not in item["author"]

        with allure.step("每页 5 条，下一页同时传递时间和 ID，结果保持顺序且不重复"):
            body = {"limit": 5, "latest_time": 0, "latest_id": 0}
            seen = []
            for _ in range(3):
                response = http.post(f"{api_base_url}/feed/listLatest", json=body, timeout=10)
                assert response.status_code == 200, response.text
                page = response.json()
                items = page["video_list"]
                assert items
                seen.extend(item["id"] for item in items)
                assert page["next_time"] == items[-1]["create_time"]
                assert page["next_id"] == items[-1]["id"]
                body = {"limit": 5, "latest_time": page["next_time"], "latest_id": page["next_id"]}
            assert seen[:13] == expected_ids
            assert len(seen) == len(set(seen))

        with allure.step("删除一条视频后，最新列表同步移除"):
            deleted = http.post(
                f"{api_base_url}/video/delete", headers=headers, json={"id": videos[-1]["id"]}, timeout=10,
            )
            assert deleted.status_code == 200, deleted.text
            response = http.post(f"{api_base_url}/feed/listLatest", json={"limit": 50}, timeout=10)
            assert response.status_code == 200, response.text
            ids = [item["id"] for item in response.json()["video_list"]]
            assert ids[:12] == expected_ids[1:]
            assert expected_ids[0] not in ids
    finally:
        for video in videos:
            http.post(
                f"{api_base_url}/video/delete", headers=headers, json={"id": video["id"]}, timeout=10,
            )
