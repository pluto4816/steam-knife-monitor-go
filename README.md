# steam-knife-monitor-go

A small Go Steam/CS2 knife-opportunity monitor for one process, one user, one SQLite database,
and QQ official bot C2C notifications.

Go V1 is frozen at tag `v1.0.0`. V2.1 and V2.2 are complete. V2.3 adds explicitly enabled
purchasing with durable safety controls, initially for C5GAME and now extended to ECO.
Automatic purchasing is off by default.

C5 live testing is currently suspended because the user's API account cannot obtain purchase
permission. The active alternative is ECO, with manual and automatic modes described below.

The final purchase feature will offer both automatic and manual purchasing on each of BUFF,
悠悠有品, C5, IGXE, and ECO. ECO is the current example: automatic purchasing runs in
`monitor`/`once`, and manual `eco-purchase-test` currently targets one fixed P250 item.
General manual item selection remains future work. No new purchase implementation for the
other four platforms is started by this specification; existing C5 code is retained.
See [the purchase product model](GO_V2_SPEC.md#purchase-product-model-five-platforms-two-purchase-channels).

## 出售辅助：手机四页账本

当前新增 `-mode sales`：独立运行“新增买入、持仓、待售清单、买卖记录”四个页面。
购买开发暂缓。本模式只处理你手工登记的数据，不运行监控、购买、Steam 查询或 QQ
通知。QQ 日报和消息录入尚未实现。无候选目录也能运行；已有目录用于物品名称建议。

- **新增买入**：按批次填写物品（包含磨损和特殊版本）、数量、人民币买入单价、日期、
  目标挂刀比例；购买平台和 Steam 参考单价可选。保存前显示数量 × 单价的总成本。
- **持仓**：查看剩余数量、成本和目标售价，按名称/平台搜索，编辑买入信息。
- **待售清单**：查看目标售价、预计 Steam 到账、参考折扣和报价记录时间；可以标记已挂售，
  实际成交后登记售出数量与本次**合计实际到账 Steam 余额**。部分售出自动扣减持仓。
- **买卖记录**：保留买入批次和实际售出记录。误录的售出可以作废，恢复数量并留下记录。
  已有售出记录的批次不能改写原买入成本、数量、身份和日期，避免改变历史计算。

出售辅助采用你指定的模型：

```text
挂刀比例 = 买入单价 / (Steam 买家支付单价 / 1.15)
目标 Steam 售价 = 买入单价 * 1.15 / 目标挂刀比例
实际挂刀比例 = 已售部分买入成本 / 实际总到账 Steam 余额
```

例如买入单价 ¥0.60、目标比例 0.6000，则目标 Steam 售价为 ¥1.15，模型预计每件到账
¥1.00。目标售价向上取整到分；计算不使用浮点数或额外最低扣费。默认目标 0.6000 可编辑。
预计到账不承诺与 Steam 实际费用取整完全一致，真实到账直接填写，不再除以 1.15。
人民币成本和 Steam 余额分开显示，不将差额称为现金利润。原监控/C5/ECO 算法暂未修改。

参考报价仅手动维护。超过 24 小时显示“待核价”；可勾选“我刚核实过参考价”刷新相同
价格的记录时间。不会轮询 Steam，也不会因刷新页面产生外部市场请求。

### 本机启动

```bash
go build -o monitor ./cmd/monitor
read -rsp '设置访问密码（至少 16 个字符）: ' SALES_PASSWORD
export SALES_PASSWORD
./monitor -mode sales -db ./steam-knife-monitor.db
```

在同一机器打开 `http://127.0.0.1:8080`，使用该密码登录。会话有效期 12 小时。
默认地址只供本机使用，不能直接在手机访问。数据写入 `sales_*` 表，不读取或改写购买、
扫描账本；本阶段使用一个 `sales` 进程即可。测试时可通过 `-db ./sales-test.db` 隔离数据。

### 手机 HTTPS 访问

如果服务器已有域名和 HTTPS 反向代理，将该域名的请求转发到 `127.0.0.1:8080`：

```bash
./monitor -mode sales -db ./steam-knife-monitor.db \
  -sales-listen 127.0.0.1:8080 \
  -sales-origin https://sales.example.com
```

把示例域名替换成手机实际访问的域名。代理需要保留原始 `Origin` 请求头。该参数是完整
访问源（协议、域名、可选端口），不带路径或末尾斜杠；不匹配的表单请求会被拒绝。

也可以让 Go 直接提供 HTTPS（需要与域名匹配、手机信任的证书和私钥）：

```bash
./monitor -mode sales -db ./steam-knife-monitor.db \
  -sales-listen :8443 -sales-origin https://sales.example.com:8443 \
  -sales-tls-cert /absolute/path/fullchain.pem \
  -sales-tls-key /absolute/path/privkey.pem
```

此时手机打开 `https://sales.example.com:8443`。不要把带登录信息的服务通过公网明文
HTTP 暴露；程序也会拒绝非回环地址的明文监听。候选目录、域名、证书均不会自动申请。

### 长期运行和备份

可用 systemd 用户服务管理进程。下面以本项目目录为例，将二进制和工作目录改为实际路径。
先将 `SALES_PASSWORD=你的长密码` 存入 `~/.config/steam-sales.env`，文件权限设为 `600`，
不要加入 Git。创建 `~/.config/systemd/user/steam-sales.service`：

```ini
[Unit]
Description=Steam selling assistant

[Service]
WorkingDirectory=%h/steam-knife-monitor-go
EnvironmentFile=%h/.config/steam-sales.env
ExecStart=%h/steam-knife-monitor-go/monitor -mode sales -db ./steam-knife-monitor.db -sales-origin https://sales.example.com
Restart=on-failure
RestartSec=5
UMask=0077

[Install]
WantedBy=default.target
```

将访问域名改好并配置 HTTPS 代理后，运行 `systemctl --user daemon-reload` 和
`systemctl --user enable --now steam-sales`。通过 `journalctl --user -u steam-sales` 查看日志。
如需退出 SSH 后仍开机运行，由服务器管理员为该用户启用 linger。

定期在服务停止时备份所选 SQLite 文件，或使用 SQLite 在线备份工具；不要只复制运行中的
数据库主文件而忽略日志文件。改变 `-db` 会打开另一份账本，不会自动迁移原记录。

## ECO automatic purchasing

The ECO manual purchase has succeeded. `monitor` and `once` now support
`-eco-purchase off|dry-run|live` (default off). Only one purchase platform can be enabled;
leave `-c5-purchase off` when using ECO. Other modes reject automatic ECO purchasing.

After the usual complete scan and successful due checks, the program selects current A-tier
items, refreshes SMIS and requires ECO-lowest with price <= CNY 1.00. It checks ECO's official
quote for that exact name, excludes increased prices/no stock, and only then checks Steam's
CNY lowest price and the base A rule. Scan/tier state remains based on SMIS.

Use the ECO credential environment variables documented below and the existing QQ variables.
Unlike C5 dry-run, ECO dry-run needs credentials for its signed official quote query.

```bash
# Test confirmation decisions. The ordinary scan and QQ phase still run.
go run ./cmd/monitor -mode once -eco-purchase dry-run \
  -db ./steam-knife-monitor.db -candidates ./steam-candidates.json

# Enable live purchasing in the existing monitor.
go run ./cmd/monitor -mode monitor -eco-purchase live \
  -db ./steam-knife-monitor.db -candidates ./steam-candidates.json
```

Dry-run performs no ECO balance check, purchase submission or purchase-ledger mutation. Live
reserves the exact confirmed quote (at most CNY 1.00) before submitting one order. The daily
CNY 10.00 cap includes ECO reserved ceilings and existing C5 committed/unresolved amounts.
The previous manual P250 purchase remains excluded after schema migration. An item is never
automatically purchased again, including on a later date or after cancellation.

Ambiguous submissions and orders awaiting their first valid detail query block new purchases;
later runs query the original merchant number without resubmitting. A successful purchase
sends an ECO order-created QQ receipt and **stops the current run after the first success**,
preserving the existing runtime behavior. Steam trade acceptance remains manual.

## ECO: one P250 Re.built Field-Tested purchase test

ECO API and purchase permission must already be enabled. This explicit mode buys at most one
`P250 | Re.built (Field-Tested)` (`P250 | 重构 (久经沙场)`), with a server-side maximum of
CNY 1.00, then queries the order once and exits. It needs no candidate catalog or QQ credentials.
It does not run scans, contact SMIS/Steam/C5/QQ, or enable automatic ECO buying in the monitor.

Supply credentials through the environment. Store the RSA private key matching the public key
registered with ECO in a local protected file, outside version control; do not paste it into
chat, code, command arguments, or logs. The key file may contain PKCS#1/PKCS#8 PEM or base64 DER.

```bash
read -rsp 'ECO identity ID: ' ECO_PARTNER_ID
printf '\n'
export ECO_PARTNER_ID
export ECO_PRIVATE_KEY_FILE='/absolute/path/to/your/eco-private.pem'
read -rsp 'Steam receiving trade URL: ' ECO_TRADE_URL
printf '\n'
export ECO_TRADE_URL
go run ./cmd/monitor -mode eco-purchase-test -db ./steam-knife-monitor.db
```

The mode checks balance, reserves CNY 1.00 and a unique merchant number in SQLite, and submits
one request. The CNY 10.00 daily budget uses Asia/Shanghai and includes existing C5 spending
and unresolved reservations in the same database. Continue using the same persistent database
and the project's single-process model, with C5 live work suspended.

**Running the command again only queries the original order.** It does not buy another item,
even after cancellation or a later calendar day. Never delete/change the database to retry an
uncertain order. A failed, restricted, empty, malformed, over-budget or ambiguous response stops
the command. No internal purchase retry occurs.

`PLACED` means an order was created. The log reports ECO's order-state code separately; it does
not claim the item has arrived in Steam. Check ECO/Steam for delivery and accept the correct
trade manually. This phase performs no Steam login, Guard action or automatic acceptance.

Official references: [setup](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/doc-5242862),
[signing](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/doc-5242863),
[purchase](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/api-220956656),
[order query](https://s.apifox.cn/8dd30eef-e628-4390-b503-846412c608ee/api-220956661).

## Candidate catalog

The monitor now requires a local candidate catalog. Generate or refresh it manually:

```sh
go run ./cmd/monitor \
  -mode update-candidates \
  -candidates ./steam-candidates.json
```

This bounded command:

- downloads ByMykel CSGO-API's English `skins_not_grouped.json` game-definition snapshot and
  filters exact structured fields to `Normal`, `Field-Tested`, and `Mil-Spec Grade` skins;
- fetches the equivalent filtered candidate IDs from smis.club;
- maps snapshot `market_hash_name` values to smis.club numeric item IDs through verified
  single-item details;
- atomically writes `steam-candidates.json` only after the complete update succeeds; and
- exits without opening SQLite, reading QQ credentials, sending a message, or starting the
  monitor.

The snapshot and smis.club use different IDs, so the initial update may need one sequential smis
detail request per candidate to establish mappings. Later updates reuse mappings that still
match and normally request details only for new or changed items. The snapshot is fetched once;
smis requests are sequential and use conservative delays. There is no internal retry or
access-control bypass. The completion log records the exact snapshot SHA-256 and lists each
definition-only `market_hash_name` omitted because no smis.club ID was mapped.

The generated JSON contains no credentials. It is local runtime data and is ignored by Git. It
can be backed up with the SQLite database or regenerated with the command above.

The source is ByMykel's MIT-licensed CSGO-API project:
`https://github.com/ByMykel/CSGO-API`. Its mutable snapshot is structured data generated from CS2
game files, not proof that an item is currently listed on Steam Community Market. The candidate
update does not derive market state from this snapshot. An oversized, changed, malformed, empty,
401, 403, 429, or challenge response stops the update without replacing a valid prior catalog.
The update mode does not call Steam Community Market.

## Execution modes

All normal modes load `./steam-candidates.json` by default. Use `-candidates PATH` to select a
different catalog. They never download the ByMykel snapshot or run the smis.club candidate
search implicitly. Steam price requests occur only in explicitly enabled C5 or ECO automatic
purchase confirmation. A current A item must first pass the selected platform's refreshed
SMIS lowest-price and CNY 1.00 prefilter; ECO also requires a valid official quote with stock
and no price increase before Steam access. Candidate updates, targeted scans, due checks, and
the manual C5/ECO purchase modes never call Steam.

### Preview

Preview performs one targeted detail scan against fresh in-memory SQLite, prints tier totals and
the messages that would be sent, and then exits:

```sh
go run ./cmd/monitor \
  -mode preview \
  -candidates ./steam-candidates.json
```

Preview does not require QQ credentials, open or modify the production database, call QQ, or
mark alerts SENT. It still reads real smis.club detail data using the conservative request
delay.

Because preview starts with empty state, it does not reproduce production hysteresis or
improvement-alert history. Its output describes a fresh-state run.

### One-time production scan

One-time mode reads each catalog item through `/api/commodity/{id}`, processes currently due
item states, sends pending QQ alerts once, and exits:

```sh
go run ./cmd/monitor \
  -mode once \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

This mode has real production side effects: it updates the selected database and may send QQ
messages. It requires all three QQ environment variables shown below. It does not paginate the
global smis.club exchange endpoint.

After each successfully validated candidate, the scan saves a SQLite checkpoint tied to the
SHA-256 fingerprint of the exact ordered catalog. If an upstream restriction interrupts the
scan, the next invocation with that same catalog resumes at the first unfinished candidate.
Changing or refreshing the catalog invalidates the old checkpoint and starts from the beginning.
The checkpoint is cleared after a complete full scan. An incomplete scan still performs no due
checks and sends no pending or empty-result notification.

To receive an explicit QQ completion message when the successful scan finds no tier-A item in
the current candidate catalog, add `-notify-empty`:

```sh
go run ./cmd/monitor \
  -mode once \
  -notify-empty \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

The message text is `没有合适物品`. This flag is accepted only by `once`; it does not change
`preview`, `monitor`, or `update-candidates`. The completion message is sent directly after
the successful bounded run and is not stored as a durable market alert. Its QQ send failure is
returned to the caller without an internal retry.

### Long-running monitor

The default mode preserves the V1 long-running scheduler while using the V2.2 targeted scan:

```sh
go run ./cmd/monitor \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

The equivalent explicit command is:

```sh
go run ./cmd/monitor \
  -mode monitor \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

Conservative runtime defaults are a 24-hour full candidate-detail scan, 60-second due poll,
1-second detail-request delay, and 5-minute ordinary-error delay. These are operating defaults,
not upstream official limits.

Each successful targeted full scan persists a completion receipt. On `monitor` restart, the
initial 426-item scan is skipped only when the receipt is less than 24 hours old, its catalog
fingerprint and candidate count match, all persisted item identities exactly cover the current
catalog, and no partial-scan checkpoint exists. Due checks run immediately, the live purchase
phase remains enabled, and the next full scan stays scheduled 24 hours after the saved
completion time. If any check fails, monitor performs the normal full scan. `once` and
`preview` always perform their normal bounded scans.

## C5 purchase phase (V2.3 Phase 1)

C5 purchasing is disabled by default. The existing commands are equivalent to
`-c5-purchase off`. Automatic `dry-run` or `live` purchasing is accepted by `monitor` and
`once`; the separate one-shot `purchase-test` mode accepts only `live`. `preview` and
`update-candidates` reject purchasing.

Test the decision path without C5 credentials, C5 requests, or purchase-ledger mutations:

```sh
go run ./cmd/monitor \
  -mode once \
  -c5-purchase dry-run \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

Targeted scans and due checks always use only smis.club data and never call Steam. After those
phases succeed, the C5 purchase phase starts from persisted current A items and refreshes each
SMIS detail. It first requires C5 to be the lowest positive platform and the exact C5 price to be
no greater than CNY 1.00. Only then does it query Steam Community Market's
`/market/priceoverview/` once, use the exact CNY `lowest_price` to recompute the discount, and
require base A without hysteresis. The Steam price is used only for payment confirmation; it is
not persisted into ordinary scan state or QQ alerts. Failures, restrictions, missing prices, or
unexpected currency prevent purchase without a C5 submission.

Steam transaction volume continues to use smis.club `steamTransactionQuantity`. It remains a
screening input and is not used as a payment amount or purchase limit.

For every current persisted A-tier candidate that dry-run excludes, it logs
`C5 purchase dry-run excluded` with a stable `reason` code and relevant refreshed values.
The phase and one-shot summaries report the total as `excluded` and `c5_excluded`.

Live mode reads the app key and Steam trade URL only from the environment:

```bash
read -rsp 'C5 app-key: ' C5_APP_KEY
printf '\n'
export C5_APP_KEY
export C5_TRADE_URL='your-steam-trade-url'
go run ./cmd/monitor \
  -mode once \
  -c5-purchase live \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

Do not put either value in the repository or command line. They are never intentionally logged.
Live startup fails closed if either variable is missing.

The fixed Phase 1 controls are:

- one successfully placed or unresolved purchase at most for each catalog item;
- CNY 1.00 maximum for a single order;
- CNY 10.00 maximum committed plus unresolved total per Asia/Shanghai calendar day;
- a fresh smis.club single-item confirmation that checks identity, C5-lowest, and the CNY
  1.00 limit before requesting one fresh Steam CNY `lowest_price`;
- a `core.RatioBP` recomputation that must satisfy base A without hysteresis before C5 access;
- the refreshed C5 price itself is submitted as `maxPrice`, so the C5 server cannot
  select a listing above the confirmed quote; and
- a C5 balance check immediately before each reservation and submission.

The purchase phase runs only after a complete targeted scan and successful due checks, or after
a monitor restart verifies the matching recent completion receipt and exact complete state and
then completes due checks. It runs before QQ alert delivery. A partial scan, unsafe restart
state, or failed due check cannot purchase.

SQLite records `SUBMITTING` before quick-buy. A transport, HTTP, cancellation, or
malformed-response ambiguity becomes `UNCERTAIN` and is never submitted again with a
new trade number. Later live runs first query the official order-detail endpoint with the
original `outTradeNo`; unresolved reconciliation blocks every new purchase. Confirmed
orders become `PLACED`. Until C5 documents a definitive no-order-created error code, an
application-level failure also becomes `UNCERTAIN`; `REJECTED` remains reserved for a future
explicitly verified rejection.

Phase 1 does not recharge, withdraw, create bargains or buy orders, batch-buy, sign in to Steam,
perform Steam Guard actions, or accept trade offers. It uses only C5GAME's documented
[balance](https://opendoc.c5game.com/api-423496792),
[quick-buy](https://opendoc.c5game.com/api-354375293), and
[order-detail](https://opendoc.c5game.com/api-153195179) endpoints.

## One-shot manual C5 purchase test

Use `purchase-test` only for an explicitly selected item already present in the candidate
catalog. It bypasses SMIS, Steam, tier, and lowest-platform checks. Its only market-price
condition is the C5-enforced fixed ceiling of CNY 1.00:

```bash
go run ./cmd/monitor \
  -mode purchase-test \
  -c5-purchase live \
  -c5-item-id 79171 \
  -db ./steam-knife-monitor.db \
  -candidates ./steam-candidates.json
```

The command resolves the exact name from the catalog, checks C5 balance, reserves the item and
daily budget in SQLite as `MANUAL_TEST`, submits at most one quick-buy request with
`maxPrice=1.00`, sends a QQ success receipt, and exits. It never automatically resubmits an
ambiguous request; a later live run reconciles the original `outTradeNo` first. The ordinary
CNY 10.00 daily limit and one-committed-purchase-per-item rule remain active.

## QQ credentials

Production `monitor` and `once` modes require:

```bash
export QQ_BOT_APP_ID='your-app-id'
export QQ_BOT_USER_OPENID='your-user-openid'
read -rsp 'QQ AppSecret: ' QQ_BOT_APP_SECRET
printf '\n'
export QQ_BOT_APP_SECRET
```

Never commit or log the AppSecret. The production modes stop at startup if any required QQ
credential is missing. `preview` and `update-candidates` do not read QQ credentials.

The QQ sender uses the HTTP Access Token and active C2C APIs only. Tokens are cached according
to `expires_in` and refreshed within 60 seconds of expiry. Delivery remains at-least-once: a
PENDING alert may be resent after restart if QQ accepted it but SQLite did not record SENT.

## Verification

```sh
go test ./...
go vet ./...
```

See `GO_V1_SPEC.md` for the frozen business rules and `GO_V2_SPEC.md` for the active V2 phase
boundary.
