from __future__ import annotations

import json
import os
from pathlib import Path
import re
from urllib.parse import urlparse

import allure
import pytest
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.support.ui import WebDriverWait


@pytest.fixture(scope="session")
def web_base_url():
    url = os.getenv("FEED_WEB_BASE_URL", "").strip().rstrip("/")
    if not url:
        pytest.fail("页面测试需要 FEED_WEB_BASE_URL，指向前端地址", pytrace=False)
    return url


@pytest.fixture
def browser(web_base_url, request):
    options = webdriver.ChromeOptions()
    options.page_load_strategy = "eager"
    options.add_argument("--window-size=1440,1000")
    options.add_argument("--autoplay-policy=no-user-gesture-required")
    if os.getenv("FEED_UI_HEADLESS", "1") != "0":
        options.add_argument("--headless=new")
    if binary := os.getenv("FEED_UI_CHROME_BINARY"):
        options.binary_location = binary
    options.set_capability("goog:loggingPrefs", {"browser": "ALL", "performance": "ALL"})
    driver_path = os.getenv("FEED_UI_CHROMEDRIVER")
    service = Service(driver_path) if driver_path else Service()
    # ChromeDriver 为每次测试创建独立的临时浏览器资料，不使用个人 Chrome 会话。
    driver = webdriver.Chrome(service=service, options=options)
    driver.set_page_load_timeout(20)
    try:
        yield driver
    finally:
        try:
            errors = []
            for entry in driver.get_log("performance"):
                event = json.loads(entry["message"])["message"]
                if event.get("method") != "Network.responseReceived":
                    continue
                response = event["params"]["response"]
                if response["status"] >= 400:
                    parsed = urlparse(response["url"])
                    errors.append({"path": parsed.path, "status": response["status"]})
            allure.attach(json.dumps(errors, ensure_ascii=False, indent=2), "HTTP 错误响应", allure.attachment_type.JSON)
            if directory := os.getenv("FEED_UI_ARTIFACT_DIR"):
                root = Path(directory)
                root.mkdir(parents=True, exist_ok=True)
                filename = re.sub(r"[^A-Za-z0-9_.-]", "_", request.node.nodeid)
                (root / (filename + "-network.json")).write_text(json.dumps(errors, ensure_ascii=False, indent=2))
        finally:
            driver.quit()


@pytest.fixture
def wait(browser):
    return WebDriverWait(browser, 15)


@pytest.fixture
def ui_account(create_account, http, api_base_url):
    account = create_account()
    response = http.post(
        f"{api_base_url}/account/findByUsername", json={"username": account["username"]}, timeout=10
    )
    assert response.status_code == 200, response.text
    return {**account, "id": response.json()["id"]}


@pytest.fixture
def login_ui(browser, wait, web_base_url):
    def login(account):
        browser.get(web_base_url + "/account")
        wait.until(EC.visibility_of_element_located((By.CSS_SELECTOR, '.login-card input[autocomplete="username"]'))).send_keys(account["username"])
        browser.find_element(By.CSS_SELECTOR, '.login-card input[type="password"]').send_keys(account["password"])
        browser.find_element(By.CSS_SELECTOR, ".login-card button.primary").click()
        wait.until(EC.visibility_of_element_located((By.XPATH, f"//div[contains(@class,'title') and normalize-space(.)='@{account['username']}']")))
    return login


@pytest.hookimpl(hookwrapper=True)
def pytest_runtest_makereport(item, call):
    outcome = yield
    report = outcome.get_result()
    if report.when != "call" or not report.failed:
        return
    driver = item.funcargs.get("browser")
    if driver is not None:
        allure.attach(driver.get_screenshot_as_png(), "失败页面截图", allure.attachment_type.PNG)
        allure.attach(driver.page_source, "失败页面 HTML", allure.attachment_type.HTML)
        if directory := os.getenv("FEED_UI_ARTIFACT_DIR"):
            root = Path(directory)
            root.mkdir(parents=True, exist_ok=True)
            filename = re.sub(r"[^A-Za-z0-9_.-]", "_", item.nodeid)
            driver.save_screenshot(str(root / (filename + "-failed.png")))
