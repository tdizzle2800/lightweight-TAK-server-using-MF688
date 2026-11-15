// puck-tak: a tiny TAK-compatible server for very small devices.
//
//	CoT relay (TCP)         location + markers, relayed between connected clients
//	CoT relay (TLS)         same hub, on a second port, when -cert/-key are given
//	Marti sync API (HTTP)   upload / search / download of data packages
//
// No client authentication: it is meant for a private WiFi network. Bind it to
// the WiFi interface address only.
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

const version = "0.3.0"

var (
	flagBind      = flag.String("bind", "127.0.0.1", "address to listen on (on the puck: the WiFi bridge address)")
	flagCoT       = flag.Int("cot-port", 8087, "TCP port for the CoT stream")
	flagCoTTLS    = flag.Int("cot-tls-port", 8089, "TLS port for the CoT stream (only used with -cert and -key)")
	flagCert      = flag.String("cert", "", "TLS certificate (PEM); enables the TLS CoT port together with -key")
	flagKey       = flag.String("key", "", "TLS private key (PEM)")
	flagGenCert   = flag.Bool("gen-cert", false, "create a self-signed -cert/-key pair if neither file exists yet")
	flagHTTP      = flag.Int("http-port", 8080, "TCP port for HTTP (data packages)")
	flagData      = flag.String("data", "./data", "directory for stored data packages")
	flagMaxUpload = flag.Int("max-upload-mb", 100, "largest single upload, in MB")
	flagMaxStore  = flag.Int("max-store-mb", 500, "total space for stored packages, in MB")
	flagLog       = flag.String("log", "", "log file (rotated at 1 MB); default is stderr")
	flagPID       = flag.String("pidfile", "", "write the process id here")
	flagDaemon    = flag.Bool("daemon", false, "detach from the terminal and keep running in the background")
	flagVerbose   = flag.Bool("v", false, "log every CoT event")
	flagVersion   = flag.Bool("version", false, "print version and exit")
)

// rotLog is a log writer that keeps at most ~2 MB on disk (current + one old file).
type rotLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

const rotMax = 1 << 20

func openRotLog(path string) (*rotLog, error) {
	l := &rotLog{path: path}
	return l, l.open()
}

func (l *rotLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	l.f, l.size = f, st.Size()
	return nil
}

func (l *rotLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > rotMax {
		l.f.Close()
		os.Rename(l.path, l.path+".1")
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// listenRetry waits (up to ~3 minutes) for the bind address to exist, so the
// server can be started before the WiFi hotspot interface is up.
func listenRetry(addr string) (net.Listener, error) {
	var err error
	for i := 0; i < 90; i++ {
		var ln net.Listener
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if !errors.Is(err, syscall.EADDRNOTAVAIL) {
			return nil, err
		}
		if i == 0 {
			log.Printf("waiting for %s to come up...", addr)
		}
		time.Sleep(2 * time.Second)
	}
	return nil, err
}

func daemonize() {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("daemon: %v", err)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), "PUCKTAK_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Fatalf("daemon: %v", err)
	}
	fmt.Printf("puck-tak started in background (pid %d)\n", cmd.Process.Pid)
	os.Exit(0)
}

func main() {
	flag.Parse()
	if *flagVersion {
		fmt.Println("puck-tak", version)
		return
	}
	if *flagDaemon && os.Getenv("PUCKTAK_CHILD") == "" {
		daemonize()
	}
	if *flagLog != "" {
		if err := os.MkdirAll(filepath.Dir(*flagLog), 0o755); err != nil {
			log.Fatalf("log dir: %v", err)
		}
		rl, err := openRotLog(*flagLog)
		if err != nil {
			log.Fatalf("log file: %v", err)
		}
		log.SetOutput(rl)
	}
	debug.SetMemoryLimit(96 << 20) // keep the footprint small on a 400 MB device

	if *flagPID != "" {
		os.WriteFile(*flagPID, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
	}
	if err := os.MkdirAll(*flagData, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	st, err := openStore(*flagData, int64(*flagMaxStore)<<20)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	h := newHub()
	cotLn, err := listenRetry(net.JoinHostPort(*flagBind, fmt.Sprint(*flagCoT)))
	if err != nil {
		log.Fatalf("cot listen: %v", err)
	}
	var tlsLn net.Listener
	tlsDesc := "off"
	if *flagGenCert {
		if *flagCert == "" || *flagKey == "" {
			log.Fatalf("tls: -gen-cert needs -cert and -key paths")
		}
		created, err := ensureCert(*flagCert, *flagKey, *flagBind)
		if err != nil {
			log.Fatalf("tls: -gen-cert: %v", err)
		}
		if created {
			log.Printf("tls: created self-signed certificate %s (key %s)", *flagCert, *flagKey)
		}
	}
	if *flagCert != "" || *flagKey != "" {
		cert, err := tls.LoadX509KeyPair(*flagCert, *flagKey)
		if err != nil {
			log.Fatalf("tls: load -cert/-key: %v", err)
		}
		raw, err := listenRetry(net.JoinHostPort(*flagBind, fmt.Sprint(*flagCoTTLS)))
		if err != nil {
			log.Fatalf("cot tls listen: %v", err)
		}
		tlsLn = tls.NewListener(raw, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		tlsDesc = "ssl://" + raw.Addr().String()
	}
	httpLn, err := listenRetry(net.JoinHostPort(*flagBind, fmt.Sprint(*flagHTTP)))
	if err != nil {
		log.Fatalf("http listen: %v", err)
	}
	log.Printf("puck-tak %s up: CoT tcp://%s, CoT TLS %s, HTTP http://%s, data %s (%d packages)",
		version, cotLn.Addr(), tlsDesc, httpLn.Addr(), *flagData, st.count())

	go h.acceptLoop(cotLn)
	if tlsLn != nil {
		go h.acceptLoop(tlsLn)
	}
	srv := &http.Server{
		Handler:           newMux(&api{st: st, hub: h, maxUpload: int64(*flagMaxUpload) << 20}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if err := srv.Serve(httpLn); err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	go func() {
		prune := time.NewTicker(30 * time.Second)
		stats := time.NewTicker(5 * time.Minute)
		for {
			select {
			case <-prune.C:
				h.prune()
			case <-stats.C:
				log.Printf("stats: %d clients, %d events in, %d dropped, %d cached positions, %d packages",
					h.clientCount(), h.eventsIn.Load(), h.dropped.Load(), h.cacheSize(), st.count())
			}
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	cotLn.Close()
	if tlsLn != nil {
		tlsLn.Close()
	}
	srv.Close()
	if *flagPID != "" {
		os.Remove(*flagPID)
	}
}
