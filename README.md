# Power Distribution System

A Windows desktop application (`PowerDistributionSystem.exe`) for managing an
electrical distribution network - buildings, distribution boards, MCCBs, MCBs and
final circuits - with all data stored in **PostgreSQL**.

The executable is fully self-contained: one file, no runtime to install. It shows
the interface in a native window and talks to your PostgreSQL server directly.

![Dashboard](docs/screenshot-dashboard.png)

The single line diagram is drawn from the same data and can be edited directly:

![Single line diagram](docs/screenshot-sld.png)

## Features

- **Dashboard** - buildings sidebar, per-board totals (total current, MCB
  capacity, active / under-maintenance circuits, utilisation), and the full
  MCCB → MCB → circuit tree with live load bars. Add, edit and delete at every
  level.
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
| PostgreSQL | Version 13 or newer, reachable over TCP from the PC running the app. A local install from https://www.postgresql.org/download/windows/ works fine. |

## Running the application

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

### Command-line options

```
PowerDistributionSystem.exe [--browser] [--port 8080] [--config path\to\config.json] [--headless]
```

| Flag | Meaning |
|------|---------|
| `--browser`  | open in the default web browser instead of the native window |
| `--port N`   | listen on a fixed port (default: a free random port on 127.0.0.1) |
| `--config`   | use a specific settings file |
| `--headless` | serve the UI/API only, without opening a window (for testing) |

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
# On Linux / macOS (cross-compile)
build/build-windows.sh 1.0.0
# or
make windows
```

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
build/                   build scripts and Windows icon/version resources
```

## Data model

```
buildings ─< boards ─< mccbs ─< mcbs ─< circuits
                                          audit_log, app_settings
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
