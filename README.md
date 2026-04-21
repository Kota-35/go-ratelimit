# go-ratelimit

Token Bucket / Fixed Window / Sliding Window Counter アルゴリズムを使った分散レートリミッターの実装。

## アルゴリズム

| アルゴリズム | 特徴 | 向いているケース |
|---|---|---|
| Token Bucket | バースト上限が明示的・滑らか | API の一般的なレート制限 |
| Fixed Window | シンプル・軽量 | 厳密な秒/分単位の制限 |
| Sliding Window Counter | バースト抑制・Fixed Window の境界問題を緩和 | Fixed Window より精度が必要な場面 |

## 構成

![architecture](docs/architecture.png)

nginx がリクエストを api1/api2 に振り分け、両サーバーが同一の Redis に `EVALSHA` でアクセスしてレート状態を共有する。

実装は `storage.Storage`（状態の読み書き）と `limiter.Limiter`（アルゴリズム）の2層構成。`REDIS_ADDR` の有無で MemoryStorage/RedisStorage を、`ALGORITHM` でアルゴリズムを切り替えられる。

## 設定

| 環境変数 | デフォルト | 説明 |
|---|---|---|
| `ALGORITHM` | `token_bucket` | アルゴリズム。`token_bucket` / `fixed_window` / `sliding_window_counter` |
| `REDIS_ADDR` | (未設定) | Redis のアドレス。未設定の場合は MemoryStorage を使う |

```bash
# docker compose でアルゴリズムを指定して起動
ALGORITHM=token_bucket docker compose -f redis.docker-compose.yml up --build
ALGORITHM=fixed_window docker compose -f redis.docker-compose.yml up --build
ALGORITHM=sliding_window_counter docker compose -f redis.docker-compose.yml up --build

# ローカル単体起動
ALGORITHM=fixed_window go run .
```

## 動作確認

```bash
# Redis版 (複数インスタンスでレート状態を共有)
docker compose -f redis.docker-compose.yml up --build

# Memory版 (インスタンスごとに独立したカウンター)
docker compose -f memory.docker-compose.yml up --build
```

起動後、連打して 200 → 429 の遷移を確認:

```bash
for i in $(seq 1 15); do
  response=$(curl -s -w "\n%{http_code}" -H "X-User-ID: alice" http://localhost:8000/ping)
  printf "[%s] %s\n" "$(echo "$response" | tail -1)" "$(echo "$response" | head -1)"
done
```

**Redis版**（複数インスタンスで状態を共有）: 設定した上限に達すると 429 が返る。api1/api2 をまたいでカウントが共有される。

```
[200] pong from api1
[200] pong from api2
...
[200] pong from api2
[429] 429 Too Many Requests
[429] 429 Too Many Requests
```

**Memory版**（インスタンスごとに独立）: インスタンスごとに独立したカウンターなので、15回では上限に達しにくい。

```
[200] pong from api1
[200] pong from api2
...
[200] pong from api1
```

レスポンスヘッダー:
- 許可時: `X-RateLimit-Remaining`, `X-RateLimit-Reset`
- 拒否時: `429 Too Many Requests`, `Retry-After`

## テスト

```bash
# 単体テスト + race detector
go test ./... -race -v
```

## ベンチマーク

```bash
# CPU数を変えて並列スケーラビリティを比較
go test ./ratelimit/... -bench=. -benchmem -benchtime=5s -cpu=1,4
```

<details>
<summary>ベンチマーク結果 (Apple M2 Pro, <code>-benchtime=5s -cpu=1,4</code>)</summary>

| ベンチマーク | CPU数 | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| MemoryStorage_Allow | 1 | 66.87 | 24 | 1 |
| MemoryStorage_Allow | 4 | 64.89 | 24 | 1 |
| MemoryStorage_AllowParallel | 1 | 67.87 | 24 | 1 |
| MemoryStorage_AllowParallel | 4 | 146.5 | 24 | 1 |
| MemoryStorage_AllowParallelMultiUser | 1 | 67.82 | 24 | 1 |
| MemoryStorage_AllowParallelMultiUser | 4 | 131.8 | 24 | 1 |
| RedisStorage_Allow | 1 | 87,853 | 208,548 | 844 |
| RedisStorage_Allow | 4 | 89,328 | 208,600 | 844 |
| RedisStorage_AllowParallel | 1 | 86,585 | 208,548 | 844 |
| RedisStorage_AllowParallel | 4 | 89,292 | 208,626 | 844 |
| Middleware_Allow | 1 | 772.3 | 1,112 | 16 |
| Middleware_Allow | 4 | 693.3 | 1,112 | 16 |
| Middleware_AllowParallel | 1 | 779.2 | 1,112 | 16 |
| Middleware_AllowParallel | 4 | 507.2 | 1,112 | 16 |

</details>

### 考察

**MemoryStorage**

in-memory なのでネットワーク IO がなく最速（約 67 ns/op）。グローバルな `sync.Mutex` 1 本で全キーを直列化するため、4 並列では約 2 倍悪化する。ユーザーが異なっていても同じロックを取り合う点は変わらない（MultiUser でも Parallel と同様に劣化）。

**RedisStorage**

Redis はシングルスレッドで処理するため、複数 goroutine から同時にリクエストが来ても Redis 側でキューイングされる。そのため並列化しても速度がほぼ変わらない。一方でネットワーク IO や Lua スクリプトの送受信コストがあるため、約 88,000 ns/op となる。複数サーバー間でレート状態を共有できるのはこのコストの対価。

**Middleware**

`Allow()` の呼び出しに加え、`fmt.Sprintf` によるキー生成・`strconv` による数値→文字列変換・レスポンスヘッダーへのセットなどのアロケーションが発生する。MemoryStorage 単体と比較して追加コストが生じ、約 772 ns/op・16 allocs/op はこれらのコストを反映している。

## 実装ノート

### 原子性の担保（Lua スクリプト）

Redis でのレート状態更新は `GET → 計算 → SET` の複数操作になる。素直に実装すると、2 サーバーが同時に `GET` した瞬間に同じ値を読み、両方が消費を確定させてしまう TOCTOU 問題が発生する。

```
api1: GET tokens=1 → 許可と判断
api2: GET tokens=1 → 許可と判断  ← 同じ値を読んでいる
api1: SET tokens=0
api2: SET tokens=0              ← 2リクエスト許可されてしまう
```

`EVALSHA` で複数操作を 1 トランザクションにまとめることで、Redis 側で直列実行を保証している。この設計は Token Bucket / Fixed Window / Sliding Window Counter の全アルゴリズムに共通して適用している。

### MemoryStorage のロック設計

MemoryStorage は `sync.Mutex` 1 本で全キーの読み書きを直列化している。キーが異なるユーザーでも同じロックを取り合うため、並列スケーラビリティは低い。

対策として `sync.Map` + per-key lock 構成が考えられる。異なるキーの処理が互いにブロックしなくなり、MultiUser 並列では約 3 倍のスループット向上が見込める。ただし `LoadOrStore` のたびにヒープ確保が発生し、単一ユーザーへの連続アクセスでは Mutex 版より遅い。

このリポジトリの主眼は**分散環境での Redis を使った状態共有**であり、シングルサーバーの並列最適化は本質的な関心事ではない。Redis を使う本番構成ではメモリストアはフォールバックに過ぎず、Lua による原子性の担保を正しく実装する方が価値が高い。これらのトレードオフから、現実装ではシンプルな `sync.Mutex` を採用している。
