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

	srv := &api.Server{Store: store, Config: cfg, Static: web.FS(), Version: version, LogPath: logPath}
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
