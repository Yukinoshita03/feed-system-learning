# RabbitMQ 本地设施

这套 Compose 配置负责启动本地 RabbitMQ。后端启动时会连接该服务并声明消息拓扑；互动通知当前仍由原有数据库逻辑同步处理。

## 启动

在仓库根目录执行：

```bash
cd infra/rabbitmq
docker compose up -d
docker compose ps
```

RabbitMQ 的 AMQP 端口是 `127.0.0.1:5672`，管理控制台是 <http://127.0.0.1:15672>。

默认本地账号是：

```text
用户名：feeddev
密码：feeddev-local-password
```

只用于本地学习。启动前可以通过环境变量覆盖：

```bash
export RABBITMQ_DEFAULT_USER='feeddev'
export RABBITMQ_DEFAULT_PASS='change-this-locally'
export RABBITMQ_ERLANG_COOKIE='change-this-cookie-locally-20261109'
docker compose up -d
```

`RABBITMQ_ERLANG_COOKIE` 是 RabbitMQ 节点之间进行 Erlang 分布式通信的共享密钥；单节点开发环境也需要它满足 RabbitMQ 的长度要求。

## 验证

```bash
docker compose ps
docker compose logs --tail=100 rabbitmq
```

容器状态为 `healthy` 后，再开始接入 Go 客户端。

## 停止

```bash
docker compose down
```

`docker compose down` 不会删除命名卷；需要同时删除本地 RabbitMQ 数据时才执行：

```bash
docker compose down -v
```
