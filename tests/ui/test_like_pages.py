from __future__ import annotations

import os
from pathlib import Path
from uuid import uuid4

import allure
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC


FIXTURES = Path(__file__).resolve().parents[1] / "fixtures"


@allure.feature("Selenium 页面测试")
@allure.title("首页点赞、刷新保持红心、查看我赞过的视频、详情页取消点赞")
def test_like_persists_between_home_detail_and_account(browser, wait, web_base_url, http, api_base_url, ui_account, login_ui, create_account, login_account):
    owner = login_account(create_account())
    headers = {"Authorization": "Bearer " + owner["token"]}
    media = {}
    for endpoint, filename, field in (("uploadVideo", "clip.mp4", "play_url"), ("uploadCover", "cover.png", "cover_url")):
        with (FIXTURES / filename).open("rb") as file:
            response = http.post(api_base_url + "/video/" + endpoint, headers=headers, files={"file": (filename, file)}, timeout=10)
        assert response.status_code == 200, response.text
        media[field] = response.json()[field]
    title = "Selenium 点赞 " + uuid4().hex[:8]
    response = http.post(api_base_url + "/video/publish", json={**media, "title": title}, headers=headers, timeout=10)
    assert response.status_code == 200, response.text
    item = response.json()
    home_heart = (By.CSS_SELECTOR, ".slide.active .actions .act:first-child")
    detail_heart = (By.CSS_SELECTOR, ".stage .actions .act:first-child")
    def state(locator, liked, count):
        def matches(driver):
            elements = driver.find_elements(*locator)
            return bool(elements) and (("liked" in elements[0].find_element(By.CSS_SELECTOR, ".icon").get_attribute("class")) == liked) and elements[0].find_element(By.CSS_SELECTOR, ".count").text == str(count)
        return matches
    try:
        with allure.step("通过页面登录，在首页点赞另一位作者的视频"):
            login_ui(ui_account)
            browser.get(web_base_url + "/")
            wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, ".slide.active .title"), title))
            wait.until(state(home_heart, False, 0))
            browser.find_element(*home_heart).click()
            wait.until(state(home_heart, True, 1))
        with allure.step("刷新首页，红心和计数从后端恢复"):
            browser.refresh()
            wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, ".slide.active .title"), title))
            wait.until(state(home_heart, True, 1))
        with allure.step("账号页的点赞列表包含该视频，点击进入详情"):
            browser.get(web_base_url + "/account")
            wait.until(EC.element_to_be_clickable((By.XPATH, "//button[contains(@class,'metric')][.//div[normalize-space(.)='点赞']]"))).click()
            card = wait.until(EC.element_to_be_clickable((By.XPATH, f"//button[contains(@class,'video-card')][.//div[normalize-space(.)='{title}']]")))
            card.click()
            wait.until(EC.url_to_be(web_base_url + "/video/" + str(item["id"])))
            wait.until(state(detail_heart, True, 1))
        with allure.step("详情页取消点赞，刷新后维持未点赞与计数 0"):
            browser.find_element(*detail_heart).click()
            wait.until(state(detail_heart, False, 0))
            browser.refresh()
            wait.until(state(detail_heart, False, 0))
            screenshot = browser.get_screenshot_as_png()
            allure.attach(screenshot, "取消点赞后刷新页面", allure.attachment_type.PNG)
            if directory := os.getenv("FEED_UI_ARTIFACT_DIR"):
                root = Path(directory)
                root.mkdir(parents=True, exist_ok=True)
                (root / "like-after-refresh.png").write_bytes(screenshot)
        with allure.step("回到我赞过的视频，列表恢复为空"):
            browser.get(web_base_url + "/account")
            wait.until(EC.element_to_be_clickable((By.XPATH, "//button[contains(@class,'metric')][.//div[normalize-space(.)='点赞']]"))).click()
            wait.until(EC.visibility_of_element_located((By.XPATH, "//*[normalize-space(.)='暂无点赞视频']")))
    finally:
        http.post(api_base_url + "/video/delete", json={"id": item["id"]}, headers=headers, timeout=10)
