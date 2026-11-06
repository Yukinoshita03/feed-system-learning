from __future__ import annotations

import os
from pathlib import Path
from uuid import uuid4

import allure
import pytest
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC


FIXTURES = Path(__file__).resolve().parents[1] / "fixtures"
TITLE_INPUT = (By.CSS_SELECTOR, "input.big-input")
VIDEO_INPUT = (By.CSS_SELECTOR, 'input[type="file"][accept="video/mp4"]')
COVER_INPUT = (By.CSS_SELECTOR, 'input[type="file"][accept="image/jpeg,image/png,image/webp"]')
PUBLISH_BUTTON = (By.CSS_SELECTOR, "button.primary.big-btn")
PUBLISHED = (By.CSS_SELECTOR, ".publish-card > .card")


def wait_for_message(wait, text):
    wait.until(EC.visibility_of_element_located((By.XPATH, f"//div[contains(@class,'toast-msg') and contains(.,'{text}')]")))


@allure.feature("Selenium 页面测试")
def test_publish_page_requires_login(browser, wait, web_base_url):
    browser.get(web_base_url + "/video")
    wait.until(EC.url_contains("/account?redirect="))
    assert browser.find_element(By.CSS_SELECTOR, ".login-card").is_displayed()


@allure.feature("Selenium 页面测试")
def test_wrong_password_stays_on_login_page(browser, wait, web_base_url, ui_account):
    browser.get(web_base_url + "/account")
    wait.until(EC.visibility_of_element_located((By.CSS_SELECTOR, '.login-card input[autocomplete="username"]'))).send_keys(ui_account["username"])
    browser.find_element(By.CSS_SELECTOR, '.login-card input[type="password"]').send_keys("wrong-password")
    browser.find_element(By.CSS_SELECTOR, ".login-card button.primary").click()
    wait_for_message(wait, "用户名或密码错误")
    assert browser.find_element(By.CSS_SELECTOR, ".login-card").is_displayed()


@allure.feature("Selenium 页面测试")
def test_register_then_login_through_pages(browser, wait, web_base_url, login_ui, http, api_base_url):
    account = {"username": "selenium_" + uuid4().hex[:12], "password": "Ui-" + uuid4().hex[:16]}
    browser.get(web_base_url + "/account/register")
    wait.until(EC.visibility_of_element_located((By.CSS_SELECTOR, 'input[autocomplete="username"]'))).send_keys(account["username"])
    browser.find_element(By.CSS_SELECTOR, 'input[autocomplete="new-password"]').send_keys(account["password"])
    browser.find_element(By.XPATH, "//button[normalize-space(.)='注册']").click()
    wait.until(EC.url_to_be(web_base_url + "/account"))
    login_ui(account)
    stored = http.post(
        f"{api_base_url}/account/findByUsername", json={"username": account["username"]}, timeout=10
    )
    assert stored.status_code == 200, stored.text
    assert stored.json()["username"] == account["username"]


@allure.feature("Selenium 页面测试")
@pytest.mark.parametrize("field, message", [
    ("title", "请输入 title"),
    ("video", "请选择视频文件"),
    ("cover", "请选择封面图片"),
])
def test_publish_required_fields(browser, wait, web_base_url, ui_account, login_ui, field, message):
    login_ui(ui_account)
    browser.get(web_base_url + "/video")
    title = wait.until(EC.visibility_of_element_located(TITLE_INPUT))
    if field != "title":
        title.send_keys("必填字段检查")
    if field == "cover":
        browser.find_element(*VIDEO_INPUT).send_keys(str(FIXTURES / "clip.mp4"))
    browser.find_element(*PUBLISH_BUTTON).click()
    wait_for_message(wait, message)
    assert not browser.find_elements(*PUBLISHED)


@allure.feature("Selenium 页面测试")
@allure.title("页面登录、上传视频和封面、发布并打开播放详情")
@pytest.mark.parametrize("cover", ["cover.png", "cover.jpg", "cover.webp"])
def test_publish_and_play(
    browser, wait, web_base_url, ui_account, login_ui, cover, http, api_base_url
):
    title = "Selenium 发布 " + uuid4().hex[:8]
    with allure.step("通过登录页登录账号"):
        login_ui(ui_account)
    with allure.step("在发布页填写标题，选择真实 MP4 和封面"):
        browser.get(web_base_url + "/video")
        wait.until(EC.visibility_of_element_located(TITLE_INPUT)).send_keys(title)
        browser.find_element(*VIDEO_INPUT).send_keys(str(FIXTURES / "clip.mp4"))
        browser.find_element(*COVER_INPUT).send_keys(str(FIXTURES / cover))
        wait.until(lambda d: d.execute_script("return document.querySelector('img.cover')?.naturalWidth > 0"))
    with allure.step("点击发布并等待真实接口返回成功"):
        browser.find_element(*PUBLISH_BUTTON).click()
        wait.until(lambda d: d.find_elements(*PUBLISHED) or d.find_elements(By.CSS_SELECTOR, ".toast.error .toast-msg"))
        errors = browser.find_elements(By.CSS_SELECTOR, ".toast.error .toast-msg")
        assert not errors, "发布失败：" + "；".join(e.text for e in errors)
        published = browser.find_element(*PUBLISHED)
        assert title in published.text
        play_link = published.find_element(By.LINK_TEXT, "去播放")
        video_id = int(play_link.get_attribute("href").rstrip("/").rsplit("/", 1)[1])
        play_link.click()
    with allure.step("详情页显示正确标题，浏览器可以加载并播放视频"):
        wait.until(EC.url_to_be(f"{web_base_url}/video/{video_id}"))
        wait.until(EC.text_to_be_present_in_element((By.CSS_SELECTOR, ".meta .title"), title))
        wait.until(lambda d: d.execute_script("const v=document.querySelector('video'); return !!v && v.readyState >= 2 && v.videoWidth > 0 && !v.error"))
        wait.until(lambda d: d.execute_script("const v=document.querySelector('video'); return !!v && !v.paused && v.currentTime > 0"))
        media = browser.find_element(By.CSS_SELECTOR, "video")
        detail = http.post(f"{api_base_url}/video/getDetail", json={"id": video_id}, timeout=10)
        assert detail.status_code == 200, detail.text
        assert detail.json()["title"] == title
        assert detail.json()["author_id"] == ui_account["id"]
        assert media.get_attribute("src") == detail.json()["play_url"]
        assert media.get_attribute("poster") == detail.json()["cover_url"]
        image = http.get(detail.json()["cover_url"], timeout=10)
        assert image.status_code == 200, image.text
        assert image.content == (FIXTURES / cover).read_bytes()
        screenshot = browser.get_screenshot_as_png()
        allure.attach(screenshot, "发布后的真实播放页面", allure.attachment_type.PNG)
        if directory := os.getenv("FEED_UI_ARTIFACT_DIR"):
            target = Path(directory) / f"publish-detail-{Path(cover).stem}-{Path(cover).suffix[1:]}.png"
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(screenshot)


@allure.feature("Selenium 页面测试")
def test_invalid_cover_shows_error_without_publishing(
    browser, wait, web_base_url, ui_account, login_ui, http, api_base_url
):
    login_ui(ui_account)
    browser.get(web_base_url + "/video")
    wait.until(EC.visibility_of_element_located(TITLE_INPUT)).send_keys("错误封面检查")
    browser.find_element(*VIDEO_INPUT).send_keys(str(FIXTURES / "clip.mp4"))
    browser.find_element(*COVER_INPUT).send_keys(str(FIXTURES / "clip.mp4"))
    browser.find_element(*PUBLISH_BUTTON).click()
    wait_for_message(wait, "只支持内容与扩展名匹配")
    assert not browser.find_elements(*PUBLISHED)
    listed = http.post(
        f"{api_base_url}/video/listByAuthorID", json={"author_id": ui_account["id"]}, timeout=10
    )
    assert listed.status_code == 200, listed.text
    assert listed.json() == []


@allure.feature("Selenium 页面测试")
def test_page_logout_revokes_session_and_protects_publish(
    browser, wait, web_base_url, ui_account, login_ui, http, api_base_url
):
    login_ui(ui_account)
    old_token = browser.execute_script("return localStorage.getItem('access_token')")
    browser.get(web_base_url + "/settings")
    wait.until(EC.element_to_be_clickable((By.XPATH, "//button[normalize-space(.)='退出登录']"))).click()
    wait.until(EC.alert_is_present()).accept()
    wait.until(EC.url_to_be(web_base_url + "/"))
    browser.get(web_base_url + "/video")
    wait.until(EC.url_contains("/account?redirect="))
    revoked = http.get(
        f"{api_base_url}/account/me", headers={"Authorization": f"Bearer {old_token}"}, timeout=10
    )
    assert revoked.status_code == 401, revoked.text
