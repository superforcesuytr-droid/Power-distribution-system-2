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

// preferredPort is tried before falling back to any free port. A stable
// address matters because the browser profile that hosts the window remembers
// the last one it was shown: with a fresh random port on every launch, the
// restored window points at a port nothing is listening on any more and the
// operator is met with "connection refused" instead of the application.
const preferredPort = 17820

// listen binds the requested port, or the preferred one, or any free port.
func listen(port int) (net.Listener, error) {
	if port > 0 {
		return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	}
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort)); err == nil {
		return ln, nil
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func main() {
	var (
		port       = flag.Int("port", 0, "TCP port to listen on (0 = the standard port, else any free one)")
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

	// Only a plain launch stands in for "the application". A copy started for
	// development, or one asked for a particular port, is meant to run
	// alongside whatever else is going, so it neither defers to a copy that is
	// already serving nor claims to be the one that others should defer to.
	soleInstance := *devDir == "" && *port == 0
	dataPath := filepath.Join(config.Dir(cfgPath), "webview2")

	// Launching the application while a copy is already serving should show
	// that copy rather than start a second server against the same database.
	if soleInstance && !*headless {
		if existing, ok := instance.Existing(config.Dir(cfgPath), version); ok {
			log.Printf("already running at %s; showing that window instead of starting again", existing)
			window.Reveal(existing, appTitle, dataPath, 1320, 900)
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
	ln, err := listen(*port)
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
	if soleInstance {
		release := instance.Acquire(config.Dir(cfgPath), url)
		defer release()
	}

	switch {
	case *headless:
		<-ctx.Done()
	case *browser || cfg.WindowMode == "browser":
		window.OpenBrowser(url)
		log.Printf("running in browser mode; press Ctrl+C to stop")
		<-ctx.Done()
	default:
		switch window.Open(url, appTitle, dataPath, 1320, 900) {
		case window.Closed:
			// The window was shown by us and has now been closed.
		case window.Handed:
			waitForWindow(ctx, srv)
		default:
			log.Printf("running in browser mode; press Ctrl+C to stop")
			<-ctx.Done()
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShutdown()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Printf("stopped")
}

// waitForWindow blocks until the interface window goes away.
//
// The page holds a connection open for as long as it is on screen, so counting
// those connections tells us when the window has closed. The browser process
// cannot: the browser showing the window is often one the operator already had
// running, which neither starts nor exits with us, and a browser asked to open
// a window when it is already running hands the request to the running copy and
// exits immediately.
func waitForWindow(ctx context.Context, srv *api.Server) {
	const (
		startup = 2 * time.Minute // allow for a slow first paint
		linger  = 8 * time.Second // survive a page reload
	)
	started := time.Now()
	var emptySince time.Time

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			live, seen := srv.Windows()
			switch {
			case live > 0:
				emptySince = time.Time{}
			case !seen:
				if time.Since(started) > startup {
					log.Printf("no window connected within %s; stopping", startup)
					return
				}
			case emptySince.IsZero():
				emptySince = time.Now()
			case time.Since(emptySince) > linger:
				log.Printf("window closed; stopping")
				return
			}
		}
	}
}
