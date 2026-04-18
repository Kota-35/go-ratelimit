# go-ratelimit

Token Bucket アルゴリズムを使ったレートリミッターの実装。
複数サーバー間でトークン残量を共有するために Redis + Lua スクリプトを使っている。

## 構成

```
Client → nginx (round-robin) → api1 / api2 → Redis
```

nginx がリクエストを api1/api2 に振り分け、両サーバーが同一の Redis を参照してトークン状態を共有する。

ストアは `Store` インターフェースで抽象化されており、`REDIS_ADDR` 環境変数の有無で MemoryStore と RedisStore を切り替えられる。

## なぜ Lua スクリプトが必要か

Redis のトークン補充は `GET → 計算 → SET` の 3 操作になる。
これを素直に実装すると、2 サーバーが同時に `GET` した瞬間に同じ残量を読み、両方が消費を確定させてしまう。

```
api1: GET tokens=1 → 許可と判断
api2: GET tokens=1 → 許可と判断  ← 同じ値を読んでいる
api1: SET tokens=0
api2: SET tokens=0              ← 2リクエスト許可されてしまう
```

`EVALSHA` で 3 操作を 1 トランザクションにまとめることで、Redis 側で直列実行が保証される。

## 設定

| 環境変数 | デフォルト | 説明 |
|---|---|---|
| `CAPACITY` | `10` | バケツの最大トークン数 |
| `REFILL_RATE` | `1` | 1秒あたりの補充トークン数 |
| `REDIS_ADDR` | (未設定) | Redis のアドレス。未設定の場合は MemoryStore を使う |

```bash
# docker compose で値を変えて起動
CAPACITY=100 REFILL_RATE=5 docker compose -f redis.docker-compose.yml up --build

# ローカル単体起動
CAPACITY=5 REFILL_RATE=0.5 go run .
```

## 動作確認

```bash
# Redis版 (複数インスタンスでトークン状態を共有)
docker compose -f redis.docker-compose.yml up --build

# Memory版 (インスタンスごとに独立したカウンター)
docker compose -f momery.docker-compose.yml up --build
```

起動後、15回連打して 200 → 429 の遷移を確認:

```bash
for i in $(seq 1 15); do
  response=$(curl -s -w "\n%{http_code}" -H "X-User-ID: alice" http://localhost:8000/ping)
  printf "[%s] %s\n" "$(echo "$response" | tail -1)" "$(echo "$response" | head -1)"
done
```

**Redis版** (CAPACITY=10): 10回で制限、api1/api2をまたいでカウントが共有される

```
[200] pong from api1
[200] pong from api2
...
[200] pong from api2   # 10回目
[429] 429 Too Many Requests
[429] 429 Too Many Requests
...
```

**Memory版** (CAPACITY=10): インスタンスごとに独立カウンターなので15回では制限に達しない

```
[200] pong from api1
[200] pong from api2
...
[200] pong from api1   # 15回目も200
```

レスポンスヘッダー:
- 許可時: `X-RateLimit-Remaining`, `X-RateLimit-Reset`
- 拒否時: `429 Too Many Requests`, `Retry-After`

## テスト

```bash
# 単体テスト + race detector
go test ./ratelimit/... -race -v

# ベンチマーク (CPU数を変えて並列スケーラビリティを比較)
go test ./ratelimit/... -bench=. -benchmem -benchtime=5s -cpu=1,4
```

ベンチマーク結果 (Apple M2 Pro):

```
BenchmarkMemoryStore_Allow             71 ns/op    0 allocs/op
BenchmarkMemoryStore_AllowParallel-4  161 ns/op    0 allocs/op  ← mutex 競合で2.3倍悪化
BenchmarkRedisStore_Allow           92204 ns/op  843 allocs/op
BenchmarkRedisStore_AllowParallel-4 93333 ns/op  843 allocs/op  ← Lua atomic により並列でも劣化なし
```

MemoryStore は 4 並列で約 2.3 倍遅くなっているが、RedisStore は並列数に関わらずほぼ一定。
Redis の Lua がシリアライズを保証しているため競合が起きないことがわかる。
