# go-ratelimit

Token Bucket / Fixed Window アルゴリズムを使ったレートリミッターの実装。
複数サーバー間でトークン残量を共有するために Redis + Lua スクリプトを使っている。

## 構成

![architecture](docs/architecture.png)

nginx がリクエストを api1/api2 に振り分け、両サーバーが同一の Redis を参照してトークン状態を共有する。

ストアは `storage.Storage` / `limiter.Limiter` の 2 層で抽象化されており、`REDIS_ADDR` 環境変数の有無で MemoryStorage と RedisStorage を切り替えられる。`ALGORITHM` 環境変数でアルゴリズムを選択できる。

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
| `ALGORITHM` | `token_bucket` | アルゴリズム。`token_bucket` / `fixed_window` |
| `REDIS_ADDR` | (未設定) | Redis のアドレス。未設定の場合は MemoryStorage を使う |

```bash
# docker compose で値を変えて起動
ALGORITHM=token_bucket docker compose -f redis.docker-compose.yml up --build

# ローカル単体起動
ALGORITHM=fixed_window go run .
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

**Redis版** (capacity=10): 10回で制限、api1/api2をまたいでカウントが共有される

```
[200] pong from api1
[200] pong from api2
...
[200] pong from api2   # 10回目
[429] 429 Too Many Requests
[429] 429 Too Many Requests
...
```

**Memory版** (capacity=10): インスタンスごとに独立カウンターなので15回では制限に達しない

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
go test ./... -race -v

# ベンチマーク (CPU数を変えて並列スケーラビリティを比較)
go test ./ratelimit/... -bench=. -benchmem -benchtime=5s -cpu=1,4
```

<details>
<summary>ベンチマーク結果 (Apple M2 Pro, <code>-benchtime=5s -cpu=1,4</code>)</summary>

| ベンチマーク | CPU数 | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| RedisStorage_Allow | 1 | 87,853 | 208,548 | 844 |
| RedisStorage_Allow | 4 | 89,328 | 208,600 | 844 |
| RedisStorage_AllowParallel | 1 | 86,585 | 208,548 | 844 |
| RedisStorage_AllowParallel | 4 | 89,292 | 208,626 | 844 |
| Middleware_Allow | 1 | 772.3 | 1,112 | 16 |
| Middleware_Allow | 4 | 693.3 | 1,112 | 16 |
| Middleware_AllowParallel | 1 | 779.2 | 1,112 | 16 |
| Middleware_AllowParallel | 4 | 507.2 | 1,112 | 16 |

</details>

**RedisStorage**

Redis はシングルスレッドで処理するため、複数 goroutine から同時にリクエストが来ても Redis 側でキューイングされる。そのため並列化しても速度がほぼ変わらない。一方でネットワーク IO や Lua スクリプトの送受信コストがあるため、約 88,000 ns/op となる。複数サーバー間でトークン状態を共有できるのはこのコストの対価。

**Middleware**

`Allow()` の呼び出しに加え、`fmt.Sprintf` によるキー生成・`strconv` による数値→文字列変換・レスポンスヘッダーへのセットなどのアロケーションが発生する。MemoryStorage 単体と比較して追加コストが生じ、約 772 ns/op・16 allocs/op はこれらのコストを反映している。

---

### +α: sync.Map によるキー分散の技術的考察

MemoryStorage は `sync.Mutex` 1 本で全キーの読み書きを直列化している。キーが異なるユーザーでも同じロックを取り合うため、ユーザー数が増えても並列スケーラビリティは改善しない。

この問題への対策として `sync.Map` + per-bucket lock 構成が考えられる。`sync.Map` でキーごとに独立した `*bucketState`（各々が `sync.Mutex` を持つ）を管理すれば、異なるキーの処理が互いにブロックしない。MultiUser 並列では約 3 倍のスループット向上が見込める。

ただし `sync.Map` 版は `LoadOrStore` のたびに `*bucketState` をヒープ確保するコストが発生し（約 64 B/2 allocs）、単一ユーザーへの連続アクセスでは Mutex 版より遅い。キー数が多く競合が分散するケースでのみ有利になる。

このリポジトリの主眼は**分散環境での Redis を使った状態共有**であり、シングルサーバーの並列最適化は本質的な関心事ではない。また Redis を使う本番構成ではメモリストアはフォールバックに過ぎず、ここでの並列性能に投資するより、Redis の Lua atomicity（TOCTOU 排除）を正しく実装する方が価値が高い。これらのトレードオフから、現実装ではシンプルな `sync.Mutex` を採用している。
