from __future__ import annotations

import json
import os
from pathlib import Path
from urllib.parse import urlparse
from uuid import uuid4

import allure
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support import expected_conditions as EC


FIXTURES = Path(__file__).resolve().parents[1] / "fixtures"


@allure.feature("Selenium 页面测试")
@allure.title("游客打开首页播放最新视频，向下切换时用双游标加载第二页")
def test_home_feed_loads_next_page(browser, wait, web_base_url, http, api_base_url, ui_account, login_account):
    login = login_account(ui_account)
    headers = {"Authorization": f"Bearer {login['token']}"}
    videos = []
    try:
        with allure.step("通过真实接口上传素材，发布 13 条新视频"):
            media = {}
            for endpoint, filename, field in (
                ("uploadVideo", "clip.mp4", "play_url"),
                ("uploadCover", "cover.png", "cover_url"),
            ):
                with (FIXTURES / filename).open("rb") as file:
                    response = http.post(
                        f"{api_base_url}/video/{endpoint}", headers=headers,
                        files={"file": (filename, file)}, timeout=10,
                    )
                assert response.status_code == 200, response.text
                media[field] = response.json()[field]
            prefix = "Selenium Feed " + uuid4().hex[:8]
            for index in range(13):
                response = http.post(
                    f"{api_base_url}/video/publish", headers=headers,
                    json={**media, "title": f"{prefix} {index}"}, timeout=10,
                )
                assert response.status_code == 200, response.text
                videos.append(response.json())
            expected_titles = [video["title"] for video in reversed(videos)]
            response = http.post(f"{api_base_url}/feed/listLatest", json={"limit": 10}, timeout=10)
            assert response.status_code == 200, response.text
            first = response.json()

        with allure.step("游客直接打开首页，看到作者和真实播放画面"):
            browser.get(web_base_url + "/")
            title = (By.CSS_SELECTOR, ".slide.active .meta .title")
            wait.until(EC.text_to_be_present_in_element(title, expected_titles[0]))
            assert browser.find_element(By.CSS_SELECTOR, ".slide.active .author-name").text == "@" + ui_account["username"]
            wait.until(lambda d: d.execute_script(
                "const v=document.querySelector('.slide.active video'); return !!v && v.readyState>=2 && v.videoWidth>0 && !v.paused && v.currentTime>0"
            ))

        with allure.step("通过键盘向下切换到第 11 条，触发真实的第二页请求"):
            for index in range(1, 11):
                browser.find_element(By.TAG_NAME, "body").send_keys(Keys.ARROW_DOWN)
                wait.until(EC.text_to_be_present_in_element(title, expected_titles[index]))
                if index == 7:
                    wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".slide")) >= 13)
            titles = [element.get_attribute("textContent") for element in browser.find_elements(By.CSS_SELECTOR, ".slide .meta .title")]
            assert expected_titles[10] in titles
            assert len(browser.find_elements(By.CSS_SELECTOR, ".slide")) >= 13
            assert len(browser.find_elements(By.CSS_SELECTOR, ".slide video")) <= 3

        with allure.step("核对浏览器下一页请求携带第一页返回的时间和 ID"):
            requests = []
            for entry in browser.get_log("performance"):
                event = json.loads(entry["message"])["message"]
                if event.get("method") != "Network.requestWillBeSent":
                    continue
                request = event["params"]["request"]
                if urlparse(request["url"]).path == "/api/feed/listLatest":
                    requests.append(json.loads(request["postData"]))
            allure.attach(json.dumps(requests, ensure_ascii=False, indent=2), "首页分页请求", allure.attachment_type.JSON)
            assert len(requests) >= 2
            assert requests[0] == {"limit": 10, "latest_time": 0, "latest_id": 0}
            assert requests[1] == {"limit": 10, "latest_time": first["next_time"], "latest_id": first["next_id"]}
            screenshot = browser.get_screenshot_as_png()
            allure.attach(screenshot, "首页加载第二页并播放", allure.attachment_type.PNG)
            if directory := os.getenv("FEED_UI_ARTIFACT_DIR"):
                root = Path(directory)
                root.mkdir(parents=True, exist_ok=True)
                (root / "feed-next-page.png").write_bytes(screenshot)
                (root / "feed-requests.json").write_text(json.dumps(requests, ensure_ascii=False, indent=2))
    finally:
        for video in videos:
            http.post(f"{api_base_url}/video/delete", headers=headers, json={"id": video["id"]}, timeout=10)
