<a id="readme-top"></a>

[![Contributors][contributors-shield]][contributors-url]
[![Stars][stars-shield]][stars-url]
[![Issues][issues-shield]][issues-url]
[![MIT License][license-shield]][license-url]
[![CI][ci-shield]][ci-url]

<div align="center">
  <h1>Antigravity Account Switcher</h1>
  <p>A local multi-account pool, live quota dashboard, and HTTP 429 failover proxy for Antigravity and agy.</p>
  <p><a href="#getting-started">Install</a> · <a href="#usage">Usage</a> · <a href="https://github.com/koboshchan/antigravity-account-switcher/issues">Report an issue</a></p>
</div>

<details>
  <summary>Contents</summary>
  <p><a href="#about-the-project">About</a> · <a href="#fork-changes">Fork changes</a> · <a href="#getting-started">Getting started</a> · <a href="#usage">Usage</a> · <a href="#storage-and-safety">Storage and safety</a> · <a href="#contributing">Contributing</a> · <a href="#license-and-credits">License and credits</a></p>
</details>

## About the project

The switcher supervises Antigravity or wraps a CLI process with scoped proxy settings. It replaces upstream authorization with the selected pool account, polls Google Cloud Code PA for quota information, and rotates accounts when an upstream request hits a supported quota-exhaustion response. Buffered requests can be replayed before a response is sent to the client; an already-started stream cannot be transparently retried.

An embedded dashboard shows accounts, quotas, token usage, and live proxy events. Optional secondary-model fallback tries a different model before rotating accounts. Account and model access still depend on Google permissions and quota.

Built with Go 1.24+, pure-Go SQLite (`modernc.org/sqlite`), and embedded HTML, CSS, and JavaScript. No Node build or runtime C library is required.

## Fork changes

This fork tracks [Muriel Gasparini's original project](https://github.com/Muriel-Gasparini/antigravity-account-switcher).

The standalone `refresh-quotas` command now wires up the OAuth token refresher, so expired access tokens can be renewed instead of being polled without refresh support. During migration, stale error states were cleared only after successful refresh and quota checks. That was a one-time local repair, not an automatic reset of every error account.

The proxy no longer replaces every requested model with the dashboard's primary model. Body, path, and query model IDs pass through, including Argon, Hydrogen, Helium, and future IDs. Regression tests cover these cases. This does not grant access to models absent from an account's Google catalog.

## Getting started

Use Linux or WSL with Git, Make, Go 1.24 or newer, and an existing Antigravity or `agy` installation.

```sh
git clone https://github.com/koboshchan/antigravity-account-switcher.git
cd antigravity-account-switcher
make install
export PATH="$HOME/.local/bin:$PATH"
antigravity-account-switcher version
```

`make install` builds with `CGO_ENABLED=0` and installs to `~/.local/bin/antigravity-account-switcher`.

Add accounts through browser OAuth; repeat for each account in the pool.

```sh
antigravity-account-switcher add-account
# Print the OAuth URL rather than opening a browser automatically
antigravity-account-switcher add-account --no-browser
```

On first startup with an empty pool, supported ACP token files can be auto-imported from `~/.gemini/antigravity-acp/acp_token.json` or `~/.gemini/antigravity-cli/acp_token.json`. There is no general agy-swap bulk-import command.

## Usage

Start a new supervised Antigravity session.

```sh
antigravity-account-switcher launch --open
# Override executable discovery
antigravity-account-switcher launch --bin /path/to/antigravity
```

Wrap the real CLI executable instead of an account-switching shell wrapper. The following path is an example installation location; use your own executable if different.

```sh
antigravity-account-switcher wrap -- ~/.local/libexec/agy
```

Run only the proxy and dashboard, without launching Antigravity.

```sh
antigravity-account-switcher serve --port 8080
```

Open `http://127.0.0.1:8080`. The default port is 8080 unless configured otherwise. Use a free port when another supervisor is running. `serve` alone does not attach to or reconfigure an existing agent process.

Inspect the pool or synchronize quotas without launching an agent.

```sh
antigravity-account-switcher list-accounts
antigravity-account-switcher refresh-quotas
antigravity-account-switcher status
antigravity-account-switcher config list
antigravity-account-switcher config set antigravity_bin /path/to/antigravity
```

Enable optional model fallback explicitly. The primary setting is a fallback reference, not an override of incoming model IDs. Primary and secondary must be distinct; they may belong to the same provider.

```sh
antigravity-account-switcher wrap --fallback-secondary \
  --model-primary gemini-3.8-flash-high \
  --model-secondary claude-sonnet-5-5 -- ~/.local/libexec/agy
```

Use IDs actually available to your account. Run `antigravity-account-switcher help` or a command's `--help` for flags. Configuration uses `config list`, `config get <key>`, and `config set <key> <value>`.

## Storage and safety

The default directory is `~/.config/antigravity-account-switcher/`, containing `config.json` and `accounts.db`. `ANTIGRAVITY_CONFIG_DIR`, `ANTIGRAVITY_DB_PATH`, or command-specific `--db` flags can select separate storage.

The SQLite database contains OAuth credentials. Keep it private; do not commit databases, token files, or credential-bearing configuration. Keep the dashboard bound to localhost, and use an SSH tunnel for remote access.

`launch` and `wrap` supervise the child process lifecycle. Exiting their supervisor can terminate its child. Do not restart a supervisor during a long-running job just to update the binary or dashboard.

## Contributing

Open an [issue](https://github.com/koboshchan/antigravity-account-switcher/issues) or submit a focused pull request against this fork. Read [AGENTS.md](AGENTS.md) before changing code.

```sh
make fmt
make lint
make test-race
make build-static
```

Race tests require a C compiler; release binaries do not.

## License and credits

Distributed under the [MIT License](LICENSE). The upstream license and Muriel Gasparini's copyright notice are preserved unchanged.

Original implementation by [Muriel Gasparini](https://github.com/Muriel-Gasparini/antigravity-account-switcher). Fork maintained at [koboshchan/antigravity-account-switcher](https://github.com/koboshchan/antigravity-account-switcher).

README structure adapted from [othneildrew/Best-README-Template](https://github.com/othneildrew/Best-README-Template/blob/master/BLANK_README.md). The template's MIT license is available [here](https://github.com/othneildrew/Best-README-Template/blob/master/LICENSE.txt).

<p align="right"><a href="#readme-top">Back to top</a></p>

[contributors-shield]: https://img.shields.io/github/contributors/koboshchan/antigravity-account-switcher
[contributors-url]: https://github.com/koboshchan/antigravity-account-switcher/graphs/contributors
[stars-shield]: https://img.shields.io/github/stars/koboshchan/antigravity-account-switcher
[stars-url]: https://github.com/koboshchan/antigravity-account-switcher/stargazers
[issues-shield]: https://img.shields.io/github/issues/koboshchan/antigravity-account-switcher
[issues-url]: https://github.com/koboshchan/antigravity-account-switcher/issues
[license-shield]: https://img.shields.io/github/license/koboshchan/antigravity-account-switcher
[license-url]: https://github.com/koboshchan/antigravity-account-switcher/blob/main/LICENSE
[ci-shield]: https://github.com/koboshchan/antigravity-account-switcher/actions/workflows/ci.yml/badge.svg
[ci-url]: https://github.com/koboshchan/antigravity-account-switcher/actions/workflows/ci.yml
