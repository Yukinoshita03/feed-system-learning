# 本机 MySQL：TCP 与 Unix Socket 查询耗时

这个实验使用 Go 的 `database/sql` 和 `go-sql-driver/mysql`，分别通过 TCP 和 Unix Socket 连接同一个 MySQL，连续执行只读的 `SELECT 1`。

从 `backend` 目录运行：

```bash
go run ./experiments/mysql-transport
```

默认连接 `root@tcp(127.0.0.1:3306)` 与 `root@unix(/tmp/mysql.sock)`，不使用密码。可用环境变量 `MYSQL_BENCH_USER` 和 `MYSQL_BENCH_PASSWORD` 指定账号，用 `-tcp`、`-socket`、`-n`、`-rounds` 修改地址及轮次。不要把真实密码写入文件或提交。

程序先校验两种连接的 MySQL 实例和账号相同，然后各预热 200 次；保持各自一条连接，每种方式每轮顺序执行 5000 次查询，运行 6 轮，交替调整先后顺序。每轮耗时除以查询次数得到平均耗时；最后报告 6 轮平均耗时的中位数。结果包括驱动、通信、MySQL 执行和结果读取开销，不包含首次建连时间。

## 一次本机测量（2026-11-04）

环境：Mac arm64、macOS 27.0、Go 1.26.0、Homebrew MySQL 26.7.0。两种方式使用同一 MySQL 实例和 `root@localhost`，客户端单线程，未控制其他系统负载。

```text
round 1 TCP  total=294.665ms mean=58.93 us/query
round 1 Unix total=143.462ms mean=28.69 us/query
round 2 Unix total=160.450ms mean=32.09 us/query
round 2 TCP  total=278.750ms mean=55.75 us/query
round 3 TCP  total=243.782ms mean=48.76 us/query
round 3 Unix total=150.156ms mean=30.03 us/query
round 4 Unix total=110.695ms mean=22.14 us/query
round 4 TCP  total=247.038ms mean=49.41 us/query
round 5 TCP  total=246.624ms mean=49.32 us/query
round 5 Unix total=136.328ms mean=27.27 us/query
round 6 Unix total=125.102ms mean=25.02 us/query
round 6 TCP  total=243.258ms mean=48.65 us/query
TCP  median round mean: 49.37 us/query
Unix median round mean: 27.98 us/query
```

这只是本机、无业务计算的单连接测试，不能直接推断并发业务、远程数据库或部署到容器后的差异。Unix Socket 的路径必须在客户端和 MySQL 所在环境可访问。
