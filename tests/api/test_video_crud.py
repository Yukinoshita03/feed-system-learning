from __future__ import annotations

import allure
import pytest


VIDEO = {
    "title": "第一条视频",
    "play_url": "/static/videos/example.mp4",
    "cover_url": "/static/covers/example.png",
}


@pytest.fixture
def video_owner(create_account, login_account):
    login = login_account(create_account())
    return login["account_id"], {"Authorization": f"Bearer {login['token']}"}


@allure.feature("视频资料 CRUD")
@allure.title("新增、查询、完整修改、重复修改和删除形成完整链路")
def test_video_crud_lifecycle(api_base_url, http, video_owner):
    owner_id, headers = video_owner
    with allure.step("新增记录的作者由登录态决定"):
        created = http.post(
            f"{api_base_url}/video/publish",
            json={**VIDEO, "title": "  第一条视频  ", "author_id": 999999},
            headers=headers, timeout=10,
        )
        assert created.status_code == 200, created.text
        video = created.json()
        assert video["id"] > 0
        assert video["author_id"] == owner_id
        assert video["title"] == VIDEO["title"]

    with allure.step("详情和作者列表允许公开读取"):
        detail = http.post(
            f"{api_base_url}/video/getDetail", json={"id": video["id"]}, timeout=10
        )
        assert detail.status_code == 200, detail.text
        assert detail.json() == video
        listed = http.post(
            f"{api_base_url}/video/listByAuthorID", json={"author_id": owner_id}, timeout=10
        )
        assert listed.status_code == 200, listed.text
        assert listed.json() == [video]

    modified = {
        "id": video["id"], "title": "修改后的标题",
        "play_url": "https://example.com/changed.mp4",
        "cover_url": "https://example.com/changed.png",
    }
    expected_updated = {**video, **modified}
    with allure.step("作者修改资料，重复提交相同内容仍然成功"):
        for _ in range(2):
            updated = http.post(
                f"{api_base_url}/video/update",
                json={**modified, "author_id": 999999}, headers=headers, timeout=10,
            )
            assert updated.status_code == 200, updated.text
            assert updated.json() == expected_updated
        stored = http.post(
            f"{api_base_url}/video/getDetail", json={"id": video["id"]}, timeout=10
        )
        assert stored.status_code == 200, stored.text
        assert stored.json() == expected_updated

    with allure.step("删除后详情、修改、重复删除均返回 404，作者列表为空数组"):
        deleted = http.post(
            f"{api_base_url}/video/delete", json={"id": video["id"]},
            headers=headers, timeout=10,
        )
        assert deleted.status_code == 200, deleted.text
        for endpoint, body in (
            ("getDetail", {"id": video["id"]}),
            ("update", modified),
            ("delete", {"id": video["id"]}),
        ):
            response = http.post(
                f"{api_base_url}/video/{endpoint}", json=body, headers=headers, timeout=10
            )
            assert response.status_code == 404, response.text
        empty = http.post(
            f"{api_base_url}/video/listByAuthorID", json={"author_id": owner_id}, timeout=10
        )
        assert empty.status_code == 200, empty.text
        assert empty.json() == []


@allure.feature("视频资料 CRUD")
@pytest.mark.parametrize("endpoint", ["publish", "update", "delete"])
def test_video_writes_require_login(api_base_url, http, endpoint):
    response = http.post(f"{api_base_url}/video/{endpoint}", json={}, timeout=10)
    assert response.status_code == 401, response.text


@allure.feature("视频资料 CRUD")
@pytest.mark.parametrize("endpoint", ["update", "delete"])
def test_video_writes_reject_other_author(
    api_base_url, http, video_owner, create_account, login_account, endpoint
):
    owner_id, owner_headers = video_owner
    created = http.post(
        f"{api_base_url}/video/publish", json=VIDEO, headers=owner_headers, timeout=10
    )
    assert created.status_code == 200, created.text
    original = created.json()
    other = login_account(create_account())
    denied = http.post(
        f"{api_base_url}/video/{endpoint}",
        json={**VIDEO, "id": original["id"], "title": "篡改标题", "author_id": owner_id},
        headers={"Authorization": f"Bearer {other['token']}"}, timeout=10,
    )
    assert denied.status_code == 403, denied.text
    unchanged = http.post(
        f"{api_base_url}/video/getDetail", json={"id": original["id"]}, timeout=10
    )
    assert unchanged.status_code == 200, unchanged.text
    assert unchanged.json() == original


@allure.feature("视频资料 CRUD")
@pytest.mark.parametrize("replacement", [
    {"title": "   "},
    {"title": "中" * 256},
    {"play_url": ""},
    {"play_url": "javascript:alert(1)"},
    {"cover_url": "//example.com/cover.png"},
])
def test_video_invalid_data_does_not_create_or_change_records(
    api_base_url, http, video_owner, replacement
):
    _, headers = video_owner
    rejected = http.post(
        f"{api_base_url}/video/publish", json={**VIDEO, **replacement},
        headers=headers, timeout=10,
    )
    assert rejected.status_code == 400, rejected.text
    created = http.post(
        f"{api_base_url}/video/publish", json=VIDEO, headers=headers, timeout=10
    )
    assert created.status_code == 200, created.text
    original = created.json()
    rejected_update = http.post(
        f"{api_base_url}/video/update", json={**original, **replacement},
        headers=headers, timeout=10,
    )
    assert rejected_update.status_code == 400, rejected_update.text
    unchanged = http.post(
        f"{api_base_url}/video/getDetail", json={"id": original["id"]}, timeout=10
    )
    assert unchanged.status_code == 200, unchanged.text
    assert unchanged.json() == original


@allure.feature("视频资料 CRUD")
@pytest.mark.parametrize("endpoint, body, status", [
    ("getDetail", {"id": 0}, 400),
    ("getDetail", {"id": "invalid"}, 400),
    ("getDetail", {"id": 4294967295}, 404),
    ("listByAuthorID", {"author_id": 0}, 400),
    ("update", {**VIDEO, "id": 0}, 400),
    ("delete", {"id": 0}, 400),
])
def test_video_id_validation(api_base_url, http, video_owner, endpoint, body, status):
    _, headers = video_owner
    response = http.post(
        f"{api_base_url}/video/{endpoint}", json=body, headers=headers, timeout=10
    )
    assert response.status_code == status, response.text


@allure.feature("视频资料 CRUD")
def test_video_author_list_is_filtered_and_newest_first(
    api_base_url, http, video_owner, create_account, login_account
):
    owner_id, headers = video_owner
    expected = []
    for title in ("旧视频", "新视频"):
        created = http.post(
            f"{api_base_url}/video/publish", json={**VIDEO, "title": title},
            headers=headers, timeout=10,
        )
        assert created.status_code == 200, created.text
        expected.append(created.json())
    other = login_account(create_account())
    other_created = http.post(
        f"{api_base_url}/video/publish", json=VIDEO,
        headers={"Authorization": f"Bearer {other['token']}"}, timeout=10,
    )
    assert other_created.status_code == 200, other_created.text
    listed = http.post(
        f"{api_base_url}/video/listByAuthorID", json={"author_id": owner_id}, timeout=10
    )
    assert listed.status_code == 200, listed.text
    assert listed.json() == expected[::-1]
