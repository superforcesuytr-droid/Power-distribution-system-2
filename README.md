# Power Distribution System

A desktop application for managing an electrical distribution network -
buildings, distribution boards, MCCBs, MCBs and final circuits - with all data
stored in **PostgreSQL**. It ships as `PowerDistributionSystem.exe` on Windows
and `PowerDistributionSystem.app` on macOS.

The application is self-contained: no runtime to install and nothing to deploy.
It shows the interface in its own window and talks to your PostgreSQL server
directly.

![Dashboard](docs/screenshot-dashboard.png)

The single line diagram is drawn from the same data and can be edited directly:

![Single line diagram](docs/screenshot-sld.png)

## Features

- **Dashboard** - buildings sidebar, per-board totals (total current, MCB
  capacity, active / under-maintenance circuits, utilisation), and the full
  MCCB → MCB → circuit tree with live load bars. Add, edit and delete at every
  level.
- **HV Overview** - the site-wide 22 kV picture above the boards: incoming
  feeders on one continuous busbar, bus couplers that split it into sections,
  and the outgoing ways from each feeder's switchgear. A way can carry an RCCB,
  ELR or ELCB, pass through a transformer, and land on a destination box that
  opens the board it feeds. Everything on it is editable in place.
- **Explorer** - flat, filterable table of every circuit across the site with
  CSV export.
- **Single Line Diagram** - auto-generated SLD of any board, colour-coded by
  load level, and **editable in place**: the pencil on any breaker changes its
  name or rating, the bin deletes it, and the `+ MCCB` / `+ MCB` controls on the
  busbars add new ones. The drawing redraws itself immediately after every
  change, so a whole board can be built up from an empty diagram. An MCB can
  also be moved to a different MCCB and the diagram re-routes it. Clicking a
  breaker body opens it on the dashboard.
- **Activity** - audit log of every change (who, what, when).
- **Global search** across boards, MCCBs, MCBs and circuits.
- **Roles** - Viewer (read only), Technician (add/edit breakers and circuits),
  Supervisor (everything, including delete, buildings/boards and settings).
- **Engineering settings** - capacity factor and warning/critical thresholds
  are configurable. By default capacity = rating × 1.14 (≈ the IEC 60898
  conventional non-tripping current), warning at 65 %, critical above 85 %.
- **First-run setup screen** - enter the PostgreSQL connection details; the
  database and schema are created automatically and seeded with demo data.

## Requirements

| Component  | Requirement |
|------------|-------------|
| Windows    | Windows 10 (21H2 or later) or Windows 11, 64-bit. Uses the Microsoft Edge WebView2 runtime that ships with Windows; if it is missing the app opens in your default browser instead. |
| macOS      | macOS 11 Big Sur or later, Apple Silicon or Intel. The window is provided by an installed Chromium browser (Chrome, Edge, Brave, Vivaldi or Chromium) running in app mode; with none of those installed the app opens in your default browser instead. |
| PostgreSQL | Version 13 or newer, reachable over TCP from the machine running the app. A local install works fine - https://www.postgresql.org/download/ (on a Mac, `brew install postgresql@16 && brew services start postgresql@16`). |

## Running the application

### Windows

1. Download `PowerDistributionSystem.exe` (from the GitHub **Actions** artifacts of
   any build, or from a **Release**) and place it anywhere, e.g. `C:\PDS\`.
2. Double-click it. The first time it opens a **Connect to PostgreSQL** screen.
3. Enter host, port, database name, user and password and click **Test
   connection**, then **Connect & save**.
   - If the database does not exist yet it is created for you (the user needs
     `CREATEDB` rights, or create it yourself: `CREATE DATABASE power_distribution;`).
   - Tables are created automatically and, on an empty database, filled with a
     demo site (Main FAB / FAC5 …) so you can explore immediately.
4. Choose your role in the top-right corner and start working.

Settings are saved to `%APPDATA%\PowerDistribution\config.json`. To run in
portable mode, put a `config.json` next to the exe instead. A log file is
written beside the config file.

### macOS

1. Download `PowerDistributionSystem-macos.zip`, unzip it, and drag
   **PowerDistributionSystem.app** into your **Applications** folder.
2. macOS blocks apps that are not signed by a paid Apple developer account, so
   clear that flag once, in Terminal:

   ```sh
   xattr -cr /Applications/PowerDistributionSystem.app
   ```

   Without this step the first launch reports that the app "is damaged" or is
   "from an unidentified developer". The alternative, if you would rather not
   use Terminal, is to double-click the app, then open **System Settings →
   Privacy & Security**, scroll to the message about the blocked app and click
   **Open Anyway**.
3. Double-click the app. The first time it opens a **Connect to PostgreSQL**
   screen; fill it in exactly as described for Windows above.
4. Choose your role in the top-right corner and start working.

The app has no Dock icon of its own, because the window it opens belongs to the
browser hosting the interface. **Closing that window quits the application**,
which it notices from the window itself rather than from the browser process,
because that browser is often one you already had running.
Opening the app again while it is already running reveals the window that is
already there rather than starting a second copy against the same database.

Settings are saved to `~/Library/Application Support/PowerDistribution/config.json`,
with the log file beside it. To see the log:

```sh
tail -f ~/Library/Application\ Support/PowerDistribution/power-distribution.log
```

### Command-line options

```
PowerDistributionSystem.exe [--browser] [--port 8080] [--config path\to\config.json] [--headless]

# macOS - the executable inside the bundle
/Applications/PowerDistributionSystem.app/Contents/MacOS/PowerDistributionSystem --browser
```

| Flag | Meaning |
|------|---------|
| `--browser`  | open in the default web browser instead of the native window |
| `--port N`   | listen on a fixed port (default: 17820 on 127.0.0.1, or any free port if that one is taken) |
| `--config`   | use a specific settings file |
| `--headless` | serve the UI/API only, without opening a window (for testing) |
| `--dev DIR`  | serve the UI from DIR on disk instead of the embedded copy, for development |

`DATABASE_URL` (e.g. `postgres://user:pass@host:5432/dbname?sslmode=disable`)
overrides the saved connection settings when set.

## Building from source

Requires [Go 1.24+](https://go.dev/dl/). No Node.js or other toolchain is needed -
the UI is plain HTML/CSS/JS embedded into the binary.

```powershell
# On Windows
powershell -ExecutionPolicy Bypass -File build\build-windows.ps1 -Version 1.0.0
```

```sh
# macOS app bundle (both Apple Silicon and Intel), from macOS or Linux
build/build-macos.sh 1.0.0
# or
make macos

# Windows exe, cross-compiled from Linux or macOS
build/build-windows.sh 1.0.0
# or
make windows
```

Building on your own Mac is the smoothest route: locally built apps are not
quarantined by Gatekeeper, so the `xattr` step above is not needed. Install Go
with `brew install go` first.

### Developing on a Mac for Windows

The application is one Go codebase with no platform-specific logic outside
`internal/window`, so almost all of the work can be done and checked on a Mac
and only the final executable has to be produced for Windows.

```sh
brew install go postgresql@16
brew services start postgresql@16

git clone https://github.com/superforcesuytr-droid/Power-distribution-system-2.git
cd Power-distribution-system-2
make dev            # http://localhost:8080, UI served from web/ on disk
```

`make dev` passes `--dev web`, which serves `web/index.html`, `web/app.js` and
`web/styles.css` from the working tree instead of the copies embedded in the
binary. Editing the interface then needs only a browser refresh; a rebuild is
only required for Go changes. The Settings dialog's About tab says when dev mode
is active, so a stale build is easy to spot.

When the change is ready:

```sh
make test           # go vet and the unit tests
make windows        # dist/PowerDistributionSystem.exe, cross-compiled
```

Cross-compiling needs no Windows machine and no extra toolchain, because the
project is pure Go with `CGO_ENABLED=0`.

**What a Mac cannot check.** Only one thing differs on Windows: the window
itself, which uses the Edge WebView2 runtime rather than the app-mode Chromium
window used on macOS. Everything else - the interface, the calculations, the
API, the database layer - is identical and is exercised by the Mac build. To
cover the rest, the `windows-smoke` CI job runs the freshly built exe on a real
`windows-latest` runner on every push and fails the build unless it starts,
resolves its paths under `AppData`, and serves the embedded UI. Pushing a branch
is therefore enough to prove the exe runs on Windows, and the finished binary is
downloadable from that run's **Actions** artifacts.

Running the exe on the Mac through Wine or CrossOver is possible but of limited
use: WebView2 is unavailable there, so the app falls back to opening the
interface in the Mac's browser, which is what the native macOS build already
does more neatly.

The output is `dist\PowerDistributionSystem.exe` (about 10 MB). Run the tests with
`make test`. To try the app on Linux/macOS during development use
`go run ./cmd/pds --browser`.

The GitHub Actions workflow (`.github/workflows/build.yml`) runs the tests against
a real PostgreSQL service and uploads the Windows exe as an artifact on every
push; pushing a tag such as `v1.0.0` also attaches it to a GitHub Release.

## Project layout

```
cmd/pds/                 entry point (window, HTTP server, lifecycle) + Windows resources
internal/api/            JSON HTTP API and role checks
internal/config/         config.json handling
internal/db/             pgx connection pool, migrations (embedded SQL), seed data, queries
internal/model/          domain types and load/utilisation calculations (+ tests)
internal/window/         native window (WebView2 on Windows, browser elsewhere)
web/                     embedded single page UI (index.html, app.js, styles.css)
build/                   build scripts, Windows icon/version resources, macOS bundle files
```

## Data model

```
buildings ─< boards ─< mccbs ─< mcbs ─< circuits
                                          audit_log, app_settings

hv_networks ─< hv_feeders ─< hv_ways ─> boards   (the destination a way feeds)
            ─< hv_couplers ─> hv_feeders         (the two sections it ties)
```

- Every breaker has a rated current; **capacity** = rating × capacity factor.
- MCB current = sum of its circuits' loads; MCCB current = sum of its MCBs;
  board total current = sum of its MCCBs; **utilisation** = total current /
  sum of MCB ratings.
- Deleting a parent removes everything beneath it (confirmed in the UI and
  restricted to the Supervisor role).
- An MCB can be re-parented onto another MCCB; breaker names must be unique
  within their parent.

## Security notes

- The built-in HTTP server listens on `127.0.0.1` only; nothing is exposed on the
  network.
- Roles are a UI/workflow control, not authentication: anyone using the PC can
  switch role. Use PostgreSQL user permissions if you need hard enforcement.
- The database password is stored in `config.json` in your user profile. Keep
  that file private, or leave the password blank and use a `.pgpass` file /
  `DATABASE_URL` instead.
