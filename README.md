# qoder

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) provider plugin for the Qoder family. CN and Intl share one plugin and one default identifier, `qoder`. Each auth file records its own region. `login_region` applies only to new logins: `cn` (default) uses `https://qoder.cn`, `intl` uses `https://qoder.com`. Legacy `qoder-cn-<uid>.json` and `qoder-intl-<uid>.json` files are adopted on startup.

The source directory is `plugins/qoder`. Names `qoder-cn` and `qoder-intl` in the repository README and in [docs/PROTOCOL.md](../../docs/PROTOCOL.md) are the host-level protocol notes. A split-channel build can still register under those ids (`-X main.providerName=qoder-cn` or `qoder-intl`). Install that package in place of the unified plugin.

Built with Go (CGO, c-shared). The component version is `plugins/qoder/VERSION` (0.8.73 in this tree). Repository tags, `make` injecting `git describe`, and a local stamp such as `0.8.87-local.<sha>` are a different series. See [Which number is which](../../README.md#which-number-is-which) in the root README.

CN API hosts are `openapi.qoder.com.cn` and `gateway.qoder.com.cn`. The older qoderwork IDE host `qoder.com.cn` is the login domain this plugin stopped using at component 0.8.45.

## Credits

`GET {billing}/api/v2/quota/usage` is folded by `summarizeQuotaResponse` in `billing.go`. The pools are `userQuota`, `addOnQuota`, `dedicatedResourcePackages`, and `orgResourcePackage` when the response includes it.

| Field | Meaning |
| --- | --- |
| `total_remain` | Sum of each pool's `remaining`. Credits that can still be spent. |
| `total_used` | Sum of each pool's `used`. Spend in the current cycle. |
| `total_size` | Sum of each pool's `total`. |
| `packages` | Always includes 基础额度 and 赠送/签到额度, then each dedicated package, then the org shared pool. |
| `fetched_at` | When this snapshot was taken. Billing can lag for minutes after a chat; compare this timestamp when the numbers look frozen. |

A pool can report `remaining` with `total` 0. Those zeros add into `total_size`, so a snapshot can be `total_remain` 6000, `total_used` 0, `total_size` 0. `displayNote` in `policy.go` writes `余6000 已用0` in that case. It appends `池S` only when `total_size > 0`.

`GET /plugins/qoder/credits` returns these absolute numbers. `POST /plugins/qoder/refresh` refreshes the cache. There is no management route that returns a percentage window, and this plugin does not implement the host QuotaProvider.

A manager account list that draws only percentage windows therefore has no window to draw. That list shows an empty quota (the CPAMP label is 「暂无额度数据」) while the plugin panel still refreshes and shows `total_remain`. A monthly bar synthesized from a hand-set cap is manager UI outside `plugins/qoder`.

Exhausted means `total_remain` is 0 and the snapshot carries a usage signal: `total_used > 0`, `total_size > 0`, or a non-empty `packages` list (`isCreditsExhausted`). A missing snapshot, or all zeros with no packages, stays `积分未知`. `summarizeQuotaResponse` always emits the two base package rows, so a successful all-zero quota response counts as exhausted.

### What the plugin panel draws

`panel.html` is a second view of the same snapshot.

- `fmtRemain` labels `总` as `total_remain + total_used` on every card.
- The per-account progress bar uses `total_size` when it is greater than 0, and otherwise uses `remain + used` (`progressHTML`).
- The summary 额度池 and 消耗占比 do the same fallback on the summed figures. With remain 6000, used 0, size 0, 消耗占比 renders as 0% of a pool of 6000.
- `summarizeCredits` in `panel.go` sets its `total` to the larger of `remain + used` and `total_size`.

That filled-in pool is display math inside the page. The auth-file note does not use it. A card can read `余 6000 已用 0` while the panel's 额度池 reads 6000.

## plan_gate (code 112)

The chat gateway rejects some (account, model) pairs with business code `112`. `planGateLine` renders:

```text
plan_gate: code=112 套餐额度或模型范围限制（同账号其他模型通常不受影响）
```

`recordUpstreamFailure` cools that pair. The account stays selectable, and the credits snapshot stays at the last fetch. `gateway_plan_gate_test.go` keeps this copy out of the hard-credit class and out of the 429 soft rate-limit class.

A host that badges an account from its last proxied request can leave a failure mark in place after credits have refreshed. Refreshing credits does not rewrite that request record. Code 112 with a positive `total_remain` is the normal shape of this rejection: the sentence names 套餐额度 or 模型范围 for the model that was called, and other models on the same account keep working.

Active pair cooldowns are listed at `GET /plugins/qoder/cooldowns` and cleared with `POST /plugins/qoder/cooldowns/clear`.

## Check-in

Both regions claim through the campaigns API. The legacy CN `daily-check-in/claim` endpoint answers 409 and grants nothing; the plugin does not call it. Auto check-in runs at 10:05 Asia/Shanghai, plus local 10:00, 15:00, and 21:00 (`checkin_auto`, default true).

## Queue wait budget

Set `plugins.configs.qoder.queue_wait_budget` to limit the cumulative time spent waiting on upstream `10605` / `RESOURCE_LIMIT` queue tickets:

```yaml
plugins:
  configs:
    qoder:
      enabled: true
      queue_wait_budget: 30
```

The value is an integer from 0 to 150 seconds. Omitting it preserves the existing 150-second budget; `0` disables queue retries. The existing maximum of three retries and 45-second per-wait cap still apply. With 30-second tickets, a budget of 30 permits one retry, while a 45-second ticket cannot fit and fails immediately.

This limits queue-ticket waits per plugin execution, not network or generation time or retries performed by the host. A shorter budget returns an explicit queue error sooner but may give up on requests that would succeed after a longer wait.

## License

MIT — see [LICENSE](LICENSE).

## Disclaimer

The code originates from the upstream reference repository and is provided for mirroring purposes only.
