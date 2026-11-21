# Antigravity Account Switcher

A local account pool, quota dashboard, and failover proxy for Antigravity and `agy`. Run your agent through the switcher to use eligible pool accounts when one runs out of quota.

[Get started](#get-started) · [Run your agent](#run-your-agent) · [Quota routing](#quota-routing) · [Troubleshooting](#troubleshooting)

This is [koboshchan's fork](https://github.com/koboshchan/antigravity-account-switcher) of [Muriel Gasparini's original project](https://github.com/Muriel-Gasparini/antigravity-account-switcher), under the [MIT license](LICENSE). The upstream copyright notice is preserved.

## Get started

Use Linux or WSL with Go 1.24+, Git, Make, and an existing Antigravity or `agy` installation.

```sh
git clone https://github.com/koboshchan/antigravity-account-switcher.git
cd antigravity-account-switcher
make install
export PATH="$HOME/.local/bin:$PATH"
antigravity-account-switcher version
```

The switcher is one Go binary with pure-Go SQLite and an embedded dashboard. There is no Node build step. `make install` builds with `CGO_ENABLED=0` and installs to `~/.local/bin`.

Connect a Google account through browser OAuth, then repeat for the rest of your pool.

```sh
antigravity-account-switcher add-account
antigravity-account-switcher refresh-quotas
```

Use `add-account --no-browser` to print the sign-in URL without opening a browser. You can also connect accounts from **Add Account** in the dashboard.

When the pool is empty, startup can import an existing ACP token from `~/.gemini/antigravity-acp/acp_token.json` or `~/.gemini/antigravity-cli/acp_token.json`. There is no general agy-swap bulk importer.

## Run your agent

Launch Antigravity with its proxy supervisor and open the dashboard.

```sh
antigravity-account-switcher launch --open
```

Override executable discovery or use a different dashboard port when needed.

```sh
antigravity-account-switcher launch --bin /path/to/antigravity --dashboard-port 8081
```

For a CLI, wrap the real executable, not an account-switching shell wrapper. Replace this example path with your installation's executable.

```sh
antigravity-account-switcher wrap -- ~/.local/libexec/agy
```

Both commands start the proxy and dashboard at `http://127.0.0.1:8080` by default. `wrap` also accepts `--dashboard-port` before `--`. The child receives scoped `HTTP_PROXY`, `HTTPS_PROXY`, and `CLOUD_CODE_URL` settings; your shell's environment is unchanged.

To run just the proxy and dashboard, without starting an agent, use this instead.

```sh
antigravity-account-switcher serve --port 8080
```

The dashboard shows accounts, per-family quota windows, selected accounts, token usage, and live proxy events. It includes light and dark themes. `serve` alone does not reroute an already-running agent.

> [!IMPORTANT]
> `launch` and `wrap` couple the agent's lifetime to the supervisor. Stopping the supervisor can terminate its child. Do not restart it during a long-running job just to update the binary.

## Quota routing

Gemini, Claude/GPT, and Argon keep separate active-account selections. Exhausting Claude/GPT does not block an account's available Gemini quota. Every applicable quota window must have capacity; a full 5-hour window does not cancel an exhausted weekly window.

The proxy replaces upstream authorization with the selected account's token. Supported rate-limit and quota-exhaustion responses trigger account rotation and request replay before the response reaches the client. It cannot transparently retry a stream that has already started.

Argon and Carbon have specific routing rules in this fork.

| Request | Routing |
| --- | --- |
| Normal Argon | Prefers eligible Ultra accounts. If none remain, base/low/high IDs can use `gemini-4-argon-trial` on Pro accounts with available `gemini-argon-wtu-weekly` quota. |
| `gemini-4-argon-trial` | Prefers eligible Ultra accounts and becomes `gemini-4-argon-low` upstream. On non-Ultra accounts it keeps the trial ID, preferring advertised trial quota. |
| Carbon | Requires an eligible Ultra account with measured Gemini capacity. No Pro fallback; returns HTTP 429 when none qualify. |

"Ultra" is the switcher's inference from a successful live quota poll reporting Gemini quota without a separate Argon bucket, not a subscription lookup. On those accounts, Argon and Carbon share Gemini's measured 5-hour and weekly limits. Separately reported Argon quotas remain independent. Missing Argon data otherwise displays as unknown, not a guessed percentage.

Other model IDs pass through unchanged; there is no configurable primary/secondary model fallback. Model access still depends on Google's permissions. A structured `403 SUBSCRIPTION_REQUIRED` temporarily excludes the rejected account/model pair for 30 minutes in the running process, without blocking unrelated models.

Family cooldowns persist and successful quota polls reconcile them. Manually disabled accounts and authentication-error accounts stay excluded. A quota-endpoint HTTP 403 blocks the account across families and hides cached percentages until a successful live poll.

> [!WARNING]
> Request bodies are buffered in RAM for replay, up to 1 GiB per request. Large concurrent requests can use substantial memory.

## Inspect and configure

```sh
antigravity-account-switcher list-accounts
antigravity-account-switcher status
antigravity-account-switcher refresh-quotas
antigravity-account-switcher config list
antigravity-account-switcher config set antigravity_bin /path/to/antigravity
antigravity-account-switcher config set port 8081
```

Background quota polling defaults to every five minutes. `refresh-quotas` forces a refresh and distinguishes successful live refreshes from cached or unverified data. It skips manually disabled accounts. Use `status` or `refresh-quotas` for family-specific selections and quota evidence.

The default directory is `~/.config/antigravity-account-switcher/`, containing `config.json` and the credential-bearing `accounts.db`. Use `ANTIGRAVITY_CONFIG_DIR` for another directory, or `ANTIGRAVITY_DB_PATH` / a command's `--db` flag for another database.

The listening port follows command-line flags, then `ANTIGRAVITY_PORT`, saved configuration, and finally 8080. It always binds to `127.0.0.1` and fails if the requested port is occupied instead of silently picking another one.

Run `antigravity-account-switcher help` or a command's `--help` for more options. Configuration supports `list`, `get <key>`, and `set <key> <value>`.

## Troubleshooting

**Google verification required**

Open **Verify with Google** on the account's dashboard card and complete Google's check. Then choose **Refresh Quotas** or run `antigravity-account-switcher refresh-quotas`. The CLI also prints a validated Google verification link when one is available; links never open automatically.

Verification blocks that account across all families without changing its manual or authentication state. Only a successful direct Google quota recheck clears the requirement. Cached/local quota data and reset timers cannot clear it. A generic quota-endpoint 403 is a separate unavailable state.

**Adding an account from another computer**

Access the dashboard through an SSH tunnel. If Google's redirect lands on an unreachable loopback URL on your browser's computer, paste the full callback URL into the pending **Add Account** dialog's callback field. Keep the dialog open until account-save confirmation. The switcher validates the pending login and exchanges the code locally; it does not fetch the pasted URL.

**The port is already in use**

Stop the instance you no longer need, or choose another port with `launch --dashboard-port 8081`, `wrap --dashboard-port 8081 -- <command>`, or `serve --port 8081`.

> [!CAUTION]
> The SQLite database contains OAuth credentials. Keep it and token files private, and do not commit them. Do not expose the dashboard publicly; use an SSH tunnel for remote access.

## Development

Read [AGENTS.md](AGENTS.md) before changing code. Race tests need a C compiler; the release binary does not.

```sh
make lint
make test-race
make build-static
```

Report bugs in this fork's [issue tracker](https://github.com/koboshchan/antigravity-account-switcher/issues).

The earlier README used [Best-README-Template](https://github.com/othneildrew/Best-README-Template), available under its [MIT license](https://github.com/othneildrew/Best-README-Template/blob/master/LICENSE.txt).
