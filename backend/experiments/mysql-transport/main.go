// 对比本机 MySQL 的 TCP 与 Unix Socket 查询耗时，只执行只读查询。
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	"github.com/go-sql-driver/mysql"
)

type connection struct {
	name string
	db   *sql.DB
}

func open(network, address string) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("MYSQL_BENCH_USER")
	if cfg.User == "" {
		cfg.User = "root"
	}
	cfg.Passwd = os.Getenv("MYSQL_BENCH_PASSWORD")
	cfg.Net, cfg.Addr = network, address
	cfg.Timeout = 3 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	// 单连接、单请求串行，比较请求往返，不比较连接池并发能力。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func identity(db *sql.DB) (string, string, error) {
	var uuid, user string
	err := db.QueryRow("SELECT @@server_uuid, CURRENT_USER()").Scan(&uuid, &user)
	return uuid, user, err
}

func query(db *sql.DB) error {
	var result int
	if err := db.QueryRow("SELECT 1").Scan(&result); err != nil {
		return err
	}
	if result != 1 {
		return fmt.Errorf("unexpected query result: %d", result)
	}
	return nil
}

func main() {
	n := flag.Int("n", 5000, "queries per round")
	rounds := flag.Int("rounds", 6, "number of rounds")
	socket := flag.String("socket", "/tmp/mysql.sock", "MySQL Unix socket path")
	address := flag.String("tcp", "127.0.0.1:3306", "MySQL TCP address")
	flag.Parse()
	if *n <= 0 || *rounds <= 0 {
		log.Fatal("n and rounds must be positive")
	}
	tcp, err := open("tcp", *address)
	if err != nil {
		log.Fatalf("TCP connection: %v", err)
	}
	defer tcp.Close()
	unix, err := open("unix", *socket)
	if err != nil {
		log.Fatalf("Unix connection: %v", err)
	}
	defer unix.Close()
	connections := []connection{{"TCP", tcp}, {"Unix", unix}}
	tcpID, tcpUser, err := identity(tcp)
	if err != nil {
		log.Fatal(err)
	}
	unixID, unixUser, err := identity(unix)
	if err != nil {
		log.Fatal(err)
	}
	if tcpID != unixID || tcpUser != unixUser {
		log.Fatal("connections use different MySQL instances or accounts")
	}
	fmt.Printf("same server and account: yes; queries per round: %d; rounds: %d\n", *n, *rounds)
	for _, conn := range connections {
		for i := 0; i < 200; i++ {
			if err := query(conn.db); err != nil {
				log.Fatalf("%s warmup: %v", conn.name, err)
			}
		}
	}
	samples := map[string][]time.Duration{"TCP": {}, "Unix": {}}
	for round := 0; round < *rounds; round++ {
		// 每轮交替先后顺序，避免总是同一种连接先执行。
		for step := 0; step < 2; step++ {
			conn := connections[(round+step)%2]
			start := time.Now()
			for i := 0; i < *n; i++ {
				if err := query(conn.db); err != nil {
					log.Fatalf("%s query: %v", conn.name, err)
				}
			}
			elapsed := time.Since(start)
			samples[conn.name] = append(samples[conn.name], elapsed)
			fmt.Printf("round %d %-4s total=%s mean=%.2f us/query\n", round+1, conn.name, elapsed.Round(time.Microsecond), float64(elapsed.Nanoseconds())/float64(*n)/1000)
		}
	}
	for _, conn := range connections {
		values := samples[conn.name]
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		median := float64(values[len(values)/2].Nanoseconds())
		if len(values)%2 == 0 {
			median = (float64(values[len(values)/2-1].Nanoseconds()) + median) / 2
		}
		fmt.Printf("%-4s median round mean: %.2f us/query\n", conn.name, median/float64(*n)/1000)
	}
}
