// Command pds is the Power Distribution System desktop application.
//
// It embeds the web UI, serves it on a loopback port and shows it in a native
// window (WebView2 on Windows). All data lives in PostgreSQL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/api"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/config"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/db"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/instance"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/logging"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/window"
	"github.com/superforcesuytr-droid/power-distribution-system/web"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const appTitle = "Power Distribution System"

func main() {
	var (
		port       = flag.Int("port", 0, "TCP port to listen on (0 = pick a free port)")
		browser    = flag.Bool("browser", false, "open in the default browser instead of a native window")
		headless   = flag.Bool("headless", false, "serve the API/UI without opening any window (for testing)")
		configPath = flag.String("config", "", "path to config.json (default: next to the exe, else the user config dir)")
		devDir     = flag.String("dev", "", "serve the UI from this directory instead of the copy embedded in the binary, so edits to web/ show on a browser refresh (use \"web\" from the repository root)")
		showVer    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println(appTitle, version)
		return
	}

	cfgPath := config.ResolvePath(*configPath)
	logCloser, logPath := logging.Setup(config.Dir(cfgPath))
	defer logCloser.Close()
	log.Printf("%s %s starting (config: %s)", appTitle, version, cfgPath)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("warning: %v (continuing with defaults)", err)
	}

	// Launching the application while a copy is already serving should show
	// that copy rather than start a second server against the same database.
	if !*headless {
		if existing, ok := instance.Existing(config.Dir(cfgPath)); ok {
			log.Printf("already running at %s; showing that window instead of starting again", existing)
			window.OpenBrowser(existing)
			return
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store := db.New()
	defer store.Close()
	if dsn, fromEnv := cfg.EffectiveDSN(); dsn != "" {
		if fromEnv {
			log.Printf("using DATABASE_URL from the environment")
		}
		if err := store.Connect(ctx, dsn); err != nil {
			log.Printf("database connection failed: %v (the setup screen will be shown)", err)
		}
	} else {
		log.Printf("no database configured yet; the setup screen will be shown")
	}

	// Normal builds serve the UI from the copy embedded in the executable, so
	// there is nothing to install alongside it. During development --dev reads
	// web/ from disk instead, which turns a UI change into a browser refresh
	// rather than a rebuild.
	static := web.FS()
	if *devDir != "" {
		if _, err := os.Stat(filepath.Join(*devDir, "index.html")); err != nil {
			log.Fatalf("--dev %q: no index.html there (run from the repository root and pass --dev web)", *devDir)
		}
		static = os.DirFS(*devDir)
		log.Printf("dev mode: serving the UI from %s - edit and refresh, no rebuild needed", *devDir)
	}

	srv := &api.Server{Store: store, Config: cfg, Static: static, Version: version, LogPath: logPath, Dev: *devDir != ""}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatalf("cannot listen: %v", err)
	}
	url := "http://" + ln.Addr().String() + "/"
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()
	log.Printf("serving UI at %s", url)
	releaseInstance := instance.Acquire(config.Dir(cfgPath), url)
	defer releaseInstance()

	switch {
	case *headless:
		<-ctx.Done()
	case *browser || cfg.WindowMode == "browser":
		window.OpenBrowser(url)
		log.Printf("running in browser mode; press Ctrl+C to stop")
		<-ctx.Done()
	default:
		dataPath := filepath.Join(config.Dir(cfgPath), "webview2")
		if closed := window.Open(url, appTitle, dataPath, 1320, 900); !closed {
			// Browser fallback: keep serving until interrupted.
			log.Printf("running in browser mode; press Ctrl+C to stop")
			<-ctx.Done()
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShutdown()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Printf("stopped")
}
