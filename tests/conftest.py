from __future__ import annotations

import os
from collections.abc import Callable, Iterator
from uuid import uuid4

import pytest
import requests


@pytest.fixture(scope="session")
def api_base_url() -> str:
    value = os.getenv("FEED_API_BASE_URL", "").strip().rstrip("/")
    if not value:
        pytest.fail(
            "请设置 FEED_API_BASE_URL，并指向专用的测试后端，例如 "
            "http://127.0.0.1:8082；测试会创建账号数据。",
            pytrace=False,
        )
    return value


@pytest.fixture(scope="session")
def http() -> Iterator[requests.Session]:
    session = requests.Session()
    session.headers.update({"Accept": "application/json"})
    yield session
    session.close()


@pytest.fixture
def create_account(
    api_base_url: str, http: requests.Session
) -> Callable[[], dict[str, str]]:
    def create() -> dict[str, str]:
        suffix = uuid4().hex[:12]
        account = {
            "username": f"pytest_{suffix}",
            "password": f"Test-{uuid4().hex[:16]}",
        }
        response = http.post(
            f"{api_base_url}/account/register",
            json=account,
            timeout=10,
        )
        assert response.status_code == 200, response.text
        return account

    return create


@pytest.fixture
def login_account(
    api_base_url: str, http: requests.Session
) -> Callable[[dict[str, str]], dict[str, object]]:
    def login(account: dict[str, str]) -> dict[str, object]:
        response = http.post(
            f"{api_base_url}/account/login",
            json=account,
            timeout=10,
        )
        assert response.status_code == 200, response.text
        return response.json()

    return login
