# go-ratelimit

Token Bucket アルゴリズムを使ったレートリミッターの実装。
複数サーバー間でトークン残量を共有するために Redis + Lua スクリプトを使っている。

## 構成

![architecture](docs/architecture.png)

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
docker compose -f memory.docker-compose.yml up --build
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

ベンチマーク結果 (Apple M2 Pro, `-benchtime=5s -cpu=1,4`):

| ベンチマーク | CPU数 | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| MemoryStoreMutex_Allow | 1 | 70.43 | 0 | 0 |
| MemoryStoreMutex_Allow | 4 | 70.14 | 0 | 0 |
| MemoryStoreMutex_AllowParallel | 1 | 70.10 | 0 | 0 |
| MemoryStoreMutex_AllowParallel | 4 | 158.7 | 0 | 0 |
| MemoryStoreMutex_AllowParallelMultiUser | 1 | 69.91 | 0 | 0 |
| MemoryStoreMutex_AllowParallelMultiUser | 4 | 156.7 | 0 | 0 |
| MemoryStoreSyncMap_Allow | 1 | 107.0 | 64 | 2 |
| MemoryStoreSyncMap_Allow | 4 | 100.0 | 64 | 2 |
| MemoryStoreSyncMap_AllowParallel | 1 | 107.6 | 64 | 2 |
| MemoryStoreSyncMap_AllowParallel | 4 | 164.5 | 64 | 2 |
| MemoryStoreSyncMap_AllowParallelMultiUser | 1 | 109.0 | 64 | 2 |
| MemoryStoreSyncMap_AllowParallelMultiUser | 4 | 37.76 | 64 | 2 |
| RedisStore_Allow | 1 | 92,149 | 208,524 | 843 |
| RedisStore_Allow | 4 | 89,337 | 208,578 | 843 |
| RedisStore_AllowParallel | 1 | 87,386 | 208,524 | 843 |
| RedisStore_AllowParallel | 4 | 92,562 | 208,603 | 843 |
| Middleware_Allow | 1 | 805.0 | 1,088 | 15 |
| Middleware_Allow | 4 | 694.5 | 1,088 | 15 |
| Middleware_AllowParallel | 1 | 778.1 | 1,088 | 15 |
| Middleware_AllowParallel | 4 | 592.8 | 1,088 | 15 |

**MemoryStoreMutex**

in-memory なのでネットワーク IO がなく最速（約 70 ns/op）。グローバルな `sync.Mutex` 1 本で全キーを直列化するため、4 並列では約 2.3 倍悪化する。ユーザーが異なっていても同じロックを取り合う点は変わらない（MultiUser でも Parallel と同様に劣化）。

**RedisStore**

Redis はシングルスレッドで処理するため、複数 goroutine から同時にリクエストが来ても Redis 側でキューイングされる。そのため並列化しても速度がほぼ変わらない。一方でネットワーク IO や Lua スクリプトの送受信コストがあるため、MemoryStore より約 1,300 倍遅い（約 90,000 ns/op）。複数サーバー間でトークン状態を共有できるのはこのコストの対価。

**Middleware**

`Allow()` の呼び出しに加え、`fmt.Sprintf` によるキー生成・`strconv` による数値→文字列変換・レスポンスヘッダーへのセットなどのアロケーションが発生する。MemoryStore 単体の約 11 倍（約 805 ns/op）、15 allocs/op はこれらのコストを反映している。

---

### +α: sync.Map によるキー分散の検証

> このリポジトリの主題は「複数サーバー間で状態を共有する分散レートリミッター」であり、シングルサーバーの並列性能は本質的な関心事ではない。ただし `sync.Mutex` のグローバルロック問題は概念として重要なので、比較用に `MemoryStoreSyncMap` を実装して計測した。

`sync.Mutex` 版はキーが異なっていても 1 本のグローバルロックで直列化されるため、ユーザー数が増えても並列スケーラビリティは改善しない。

`sync.Map` + per-bucket lock 版は、異なるキーのバケツが独立したロックを持つため、ユーザーごとの処理が互いにブロックしない。

```
                                       cpu=1      cpu=4      変化
Mutex_AllowParallelMultiUser          69.9 ns   156.7 ns   +2.2x 遅化
SyncMap_AllowParallelMultiUser       109.0 ns    37.8 ns   +2.9x 高速化
```

MultiUser-4 の 37.8 ns/op は「1 操作が 38 ns で終わった」ではなく、4 goroutine が並列に動くことで **スループットが約 3 倍になった** 結果である（`ns/op = 経過wall時間 ÷ 総ops数` のため、並列化が効くほど値が小さくなる）。

ただし `sync.Map` 版は 64 B/op・2 allocs/op のヒープアロケーションが発生する。`LoadOrStore` で毎回 `*bucketState` を生成してから破棄するコストであり、単一ユーザーへの連続アクセスでは Mutex 版より遅い（107 ns vs 70 ns）。キー数が多く競合が分散するケースでのみ有利になる。
