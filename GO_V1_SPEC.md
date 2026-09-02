# Steam Knife Monitor — Go V1 Specification

## Goal

Build one small, long-running Go program for a single server, process, user, notification
channel, and SQLite database. Migrate verified business rules, not the Python architecture.

## Candidate and market data

Static candidates satisfy all three conditions:

- 品质：军规级
- 类别：普通
- 磨损：久经沙场

A valid MarketData sample contains a non-negative fixed-point ratio and non-negative integer
Steam daily volume. Prices are exact strings for storage and display and do not affect
classification. Absence, partial scans, and invalid data are not evidence of a tier change.

Ratios use `RatioBP`, where `1.0000 = 10000`. Business comparisons must not use floating
point. When a detail-derived ratio is converted to `RatioBP`, a fractional remainder less than
or equal to half a `RatioBP` unit rounds down; only a remainder strictly greater than half rounds
up.

## Tiers

Base classification, evaluated in order:

- A: ratio <= 0.6000 and volume >= 350.
- B1: not A, ratio <= 0.6300 and volume >= 250.
- B2: not an earlier tier, ratio <= 0.6800 and volume >= 250.
- B3: not an earlier tier, ratio <= 0.7500 and volume >= 250.
- C: every other valid candidate.

A hysteresis:

- Enter A only through the base A rule.
- An existing A remains A while ratio <= 0.6100 and volume >= 320.
- Otherwise re-run base classification.
- Non-A tiers never use the A retention thresholds.

## Timing and trend

Base periods:

- A: 5m
- B1: 10m
- B2: 30m
- B3: 60m
- C: 240m

A ratio improvement of at least 0.0300 (300 RatioBP) is a fast drop. The scheduler uses 50% of
the current tier's base period for the next check only, with A never below 5m. Core reports the
drop and whether it is fast.

## Notifications

- None -> A: notify once.
- B1/B2/B3/C -> A: notify once.
- A -> non-A: do not notify.
- During one continuous A episode, notify again when the last successfully sent A alert ratio
  minus the current ratio is at least 0.0200.
- The same A-entry event must be durable and idempotent across repeated processing and restart.
- Only a successfully sent alert becomes the next improvement baseline.
- `needs_verification=true` blocks new notifications.

Notification Phase 1 is complete: notification intents are durable, and the item-state update
and optional pending intent are committed atomically. Only an alert marked SENT becomes an
improvement baseline.

QQ official bot C2C messaging is the only V1 notification channel. The production sender:

- reads `QQ_BOT_APP_ID`, `QQ_BOT_APP_SECRET`, and `QQ_BOT_USER_OPENID` from the environment;
- obtains an Access Token with `POST https://api.bot.qq.com/app/getAppAccessToken`;
- caches the token according to the returned `expires_in` and refreshes within 60 seconds of
  expiry;
- sends the persisted alert message with `POST /v2/users/{user_openid}/messages` and
  `msg_type=0`;
- performs no background refresh or internal retry.

The AppSecret and Access Token must never be committed or logged. Delivery remains
at-least-once: if an external send succeeds but the process stops before SQLite records SENT,
the pending alert may be sent again after restart. The active C2C API does not provide the
idempotency guarantee needed to remove that window.

## Data acquisition rules

Use the already verified smis.club normal web API behavior. Use low-frequency requests,
batching, deduplication, and caching. Do not bypass CAPTCHA, access controls, Cloudflare, rate
limits, or risk controls.

Only valid MarketData can upgrade or downgrade an item. Absence from a response never changes
the stored market state or tier.

## Current phase boundary

Go Core, Collector, SQLite, Scan Cycle, Detail Market Fetch, Tier Scheduler, the long-running
runtime, Notification Phase 1, and the QQ official sender are complete.

The frozen binding probe verified the HTTP Access Token and active C2C API path: token
acquisition succeeded, the send returned HTTP 200 with QQ API success, and the bound user
received the message.

The production program needs only the QQ HTTP Token and active C2C message APIs. Binding has
already established the single user's openid; production must not add a WebSocket Gateway,
event listener, heartbeat, or binding service.

Runtime defaults are a 24-hour full scan, 60-second due poll, 2-second page delay, 1-second
detail-request delay, and 5-minute ordinary-error delay. These are conservative V1 operating
defaults, not smis.club official limits.
