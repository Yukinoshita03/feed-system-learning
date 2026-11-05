from __future__ import annotations

import json
import os
import shutil
import subprocess
from collections.abc import Callable, Iterator
from typing import Any
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


@pytest.fixture(scope="session")
def redis_command() -> Callable[..., Any]:
    addr = os.getenv("FEED_TEST_REDIS_ADDR", "").strip()
    database = os.getenv("FEED_TEST_REDIS_DB", "").strip()
    if not addr or not database:
        pytest.skip("缓存用例需要显式设置 FEED_TEST_REDIS_ADDR 和 FEED_TEST_REDIS_DB")
    executable = shutil.which("redis-cli")
    if not executable:
        pytest.fail("缓存用例需要 redis-cli，请先安装 Redis 客户端", pytrace=False)
    host, separator, port = addr.rpartition(":")
    try:
        port_number, database_number = int(port), int(database)
    except ValueError:
        pytest.fail("Redis 测试地址必须为 host:port，数据库编号必须为整数", pytrace=False)
    if not separator or not host or not 1 <= port_number <= 65535 or database_number < 0:
        pytest.fail("Redis 测试地址或数据库编号无效", pytrace=False)
    arguments = [
        executable, "-h", host.strip("[]"), "-p", str(port_number),
        "-n", str(database_number), "--json", "-e",
    ]
    environment = os.environ.copy()
    environment.pop("REDISCLI_AUTH", None)
    password = os.getenv("FEED_TEST_REDIS_PASSWORD", "")
    if password:
        environment["REDISCLI_AUTH"] = password

    def command(*args: object) -> Any:
        result = subprocess.run(
            arguments + [str(arg) for arg in args],
            env=environment,
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode != 0:
            pytest.fail(
                "Redis 检查失败：" + (result.stderr.strip() or result.stdout.strip()),
                pytrace=False,
            )
        return json.loads(result.stdout)

    return command


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
