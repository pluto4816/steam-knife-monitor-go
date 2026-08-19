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
point.

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

## Data acquisition rules

Use the already verified smis.club normal web API behavior. Use low-frequency requests,
batching, deduplication, and caching. Do not bypass CAPTCHA, access controls, Cloudflare, rate
limits, or risk controls.

Only valid MarketData can upgrade or downgrade an item. Absence from a response never changes
the stored market state or tier.

## Current phase boundary

Go Core, Collector, SQLite, Scan Cycle, Detail Market Fetch, Tier Scheduler, and the
long-running runtime are complete. The current phase is Notification.

Notification intents are durable. The item-state update and optional pending intent are
committed atomically. Only an alert marked SENT becomes an improvement baseline. Delivery is
at-least-once: if an external send succeeds but the process stops before SQLite records SENT,
the pending alert may be sent again after restart. SQLite alone cannot guarantee duplicate-free
external delivery; that requires idempotency support from the eventual provider.

Runtime defaults are a 24-hour full scan, 60-second due poll, 2-second page delay, 1-second
detail-request delay, and 5-minute ordinary-error delay. These are conservative V1 operating
defaults, not smis.club official limits.
