from pathlib import Path
from uuid import uuid4

import allure
import pytest
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC

FIXTURES = Path(__file__).resolve().parents[1] / "fixtures"
pytestmark = [allure.feature("社交 CRUD 页面")]


@pytest.fixture
def crud_video(api_base_url, http, create_account, login_account, request):
    owner_account = getattr(request, "param", None) or create_account()
    owner = login_account(owner_account)
    headers = {"Authorization": "Bearer " + owner["token"]}
    media = {}
    for endpoint, filename, field in (("uploadVideo", "clip.mp4", "play_url"), ("uploadCover", "cover.png", "cover_url")):
        with (FIXTURES / filename).open("rb") as file:
            response = http.post(api_base_url + "/video/" + endpoint, headers=headers, files={"file": (filename, file)}, timeout=10)
        assert response.status_code == 200, response.text
        media[field] = response.json()[field]
    response = http.post(api_base_url + "/video/publish", json={**media, "title": "CRUD 页面 " + uuid4().hex[:8]}, headers=headers, timeout=10)
    assert response.status_code == 200, response.text
    item = response.json()
    yield item, owner, owner_account
    fresh = login_account(owner_account)
    http.post(api_base_url + "/video/delete", json={"id": item["id"]}, headers={"Authorization": "Bearer " + fresh["token"]}, timeout=10)


def open_comments(browser, wait):
    wait.until(EC.element_to_be_clickable((By.XPATH, "//div[contains(@class,'actions')]//button[.//span[normalize-space(.)='评论']]"))).click()
    wait.until(EC.visibility_of_element_located((By.CSS_SELECTOR, ".drawer-foot textarea")))


def test_comment_publish_refresh_and_delete(browser, wait, web_base_url, ui_account, login_ui, crud_video):
    item, _, _ = crud_video
    login_ui(ui_account)
    browser.get(web_base_url + "/video/" + str(item["id"]))
    open_comments(browser, wait)
    text = "浏览器评论 " + uuid4().hex[:8]
    browser.find_element(By.CSS_SELECTOR, ".drawer-foot textarea").send_keys(text)
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, ".drawer-foot .primary"))).click()
    wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, ".comment-content"), text))
    browser.refresh()
    open_comments(browser, wait)
    wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, ".comment-content"), text))
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, ".comment-actions button"))).click()
    wait.until(EC.alert_is_present()).accept()
    wait.until(EC.visibility_of_element_located((By.XPATH, "//div[normalize-space(.)='暂无评论']")))


def test_favorite_detail_account_and_refresh(browser, wait, web_base_url, ui_account, login_ui, crud_video):
    item, _, _ = crud_video
    login_ui(ui_account)
    browser.get(web_base_url + "/video/" + str(item["id"]))
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, 'button[aria-label="收藏视频"]'))).click()
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, 'button[aria-label="取消收藏"]')))
    browser.refresh()
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, 'button[aria-label="取消收藏"]')))
    browser.get(web_base_url + "/account")
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[contains(@class,'metric')][.//div[normalize-space(.)='收藏']]"))).click()
    wait.until(EC.element_to_be_clickable((By.XPATH, f"//button[contains(@class,'video-card')][.//div[normalize-space(.)='{item['title']}']]"))).click()
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, 'button[aria-label="取消收藏"]'))).click()
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, 'button[aria-label="收藏视频"]')))
    browser.get(web_base_url + "/account")
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[contains(@class,'metric')][.//div[normalize-space(.)='收藏']]"))).click()
    wait.until(EC.visibility_of_element_located((By.XPATH, "//div[normalize-space(.)='暂无收藏视频']")))


def test_follow_profile_refresh_and_unfollow(browser, wait, web_base_url, ui_account, login_ui, crud_video):
    _, owner, _ = crud_video
    login_ui(ui_account)
    browser.get(web_base_url + "/u/" + str(owner["account_id"]))
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='关注']"))).click()
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='已关注']")))
    browser.refresh()
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='已关注']"))).click()
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='关注']")))


def test_notifications_read_and_delete_in_page(browser, wait, web_base_url, api_base_url, http, ui_account, login_ui, login_account, create_account):
    actor = login_account(create_account())
    response = http.post(api_base_url + "/social/follow", json={"vlogger_id": ui_account["id"]}, headers={"Authorization": "Bearer " + actor["token"]}, timeout=10)
    assert response.status_code == 200, response.text
    login_ui(ui_account)
    wait.until(EC.element_to_be_clickable((By.CSS_SELECTOR, 'a.dy-nav-link[href="/notifications"]'))).click()
    wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, '[data-testid="unread-count"]'), "未读 1"))
    wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, ".notice"), actor["username"]))
    browser.refresh()
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='全部已读']"))).click()
    wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, '[data-testid="unread-count"]'), "未读 0"))
    wait.until(EC.invisibility_of_element_located((By.CSS_SELECTOR, ".notice.unread")))
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='删除通知']"))).click()
    wait.until(EC.visibility_of_element_located((By.XPATH, "//p[normalize-space(.)='暂无通知']")))
    allure.attach(browser.get_screenshot_as_png(), "通知已读并删除", allure.attachment_type.PNG)
