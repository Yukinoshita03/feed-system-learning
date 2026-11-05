# 账号接口 API 自动化测试

测试使用 pytest、Requests 和 Allure，覆盖改密、改名、按 ID/用户名查询、头像上传和个人资料更新接口。

## 启动隔离的测试后端

先准备一个专用 MySQL 数据库，然后在 `backend/` 目录启动服务。下面示例使用独立端口和头像目录，避免影响默认的 8081 服务和已有头像文件：

```bash
export MYSQL_DSN='root@tcp(127.0.0.1:3306)/feed_system_pytest?charset=utf8mb4&parseTime=True&loc=Local'
export JWT_SECRET='local-only-pytest-signing-secret'
export HTTP_ADDR=':8082'
export UPLOAD_DIR='/tmp/feed-system-pytest-uploads'
export PUBLIC_BASE_URL='http://127.0.0.1:8082'
go run ./cmd
```

`go run ./cmd` 会自动迁移账号表。运行前可用 MySQL 创建空的测试数据库：

```sql
CREATE DATABASE feed_system_pytest CHARACTER SET utf8mb4;
```

## 安装和运行

在项目根目录执行：

```bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -r tests/requirements.txt
FEED_API_BASE_URL='http://127.0.0.1:8082' \
  python -m pytest tests/api --alluredir=reports/allure-results
```

命令退出后，可用 Allure CLI 打开报告：

```bash
allure serve reports/allure-results
```

用例会创建测试账号，并上传一张小型 PNG。请在专用测试数据库和测试头像目录中运行；清理时删除该数据库及 `UPLOAD_DIR` 下的文件即可。
