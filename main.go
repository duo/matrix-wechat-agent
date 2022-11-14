package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/duo/matrix-wechat-agent/internal"

	log "github.com/sirupsen/logrus"
)

const (
	defaultReconnectBackoff = 2 * time.Second
	maxReconnectBackoff     = 2 * time.Minute
	reconnectBackoffReset   = 5 * time.Minute
)

var (
	host    string
	secret  string
	version string

	websocketStarted chan struct{}
	stopPinger       chan struct{}
)

func init() {
	flag.StringVar(&host, "h", "", "appservice address")
	flag.StringVar(&secret, "s", "", "secret")
	flag.StringVar(&version, "V", "", "fake version")
}

func main() {
	flag.Parse()

	if len(host) == 0 || len(secret) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	var driverDLL string
	if runtime.GOARCH == "amd64" {
		driverDLL = "wxDriver64.dll"
	} else {
		driverDLL = "wxDriver.dll"
	}
	driver, err := syscall.LoadLibrary(driverDLL)
	if err != nil {
		log.Fatal(err)
	}
	defer syscall.FreeLibrary(driver)

	var as internal.AppService

	workdir := filepath.Join(internal.GetDocDir(), "matrix_wechat_agent")
	if !internal.PathExists(workdir) {
		if err := os.MkdirAll(workdir, 0o644); err != nil {
			log.Fatalf("Failed to create temp folder: %v", err)
		}
	}

	as.Workdir = workdir
	as.Docdir = internal.GetWechatDocdir()

	internal.GetWechatManager().SetVersion(version)

	go internal.GetWechatManager().Serve(&as)

	go startWebSocket(&as, host, secret)

	go startPinger(&as)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	select {
	case stopPinger <- struct{}{}:
	default:
	}

	as.StopWebsocket(internal.ErrWebsocketManualStop)

	internal.GetWechatManager().Dispose()

	fmt.Printf("\n")
}

func startPinger(as *internal.AppService) {
	interval := 15 * time.Second
	clock := time.NewTicker(interval)
	defer func() {
		log.Infoln("Websocket pinger stopped")
		clock.Stop()
	}()
	log.Infof("Pinging websocket every %s", interval)
	for {
		select {
		case <-clock.C:
			pingServer(as)
		case <-stopPinger:
			return
		}
	}
}

func pingServer(as *internal.AppService) {
	if !as.HasWebsocket() {
		log.Debugln("Received server ping request, but no websocket connected. Trying to short-circuit backoff sleep")
		select {
		case <-websocketStarted:
		case <-time.After(15 * time.Second):
			if !as.HasWebsocket() {
				log.Warnln("Failed to ping websocket: didn't connect after 15 seconds of waiting")
				return
			}
		}
	}

	if err := as.SendPing(); err != nil {
		log.Warnln("Websocket ping returned error: %v", err)
		as.StopWebsocket(fmt.Errorf("websocket ping returned error: %w", err))
	}
}

func startWebSocket(as *internal.AppService, url, secret string) {
	onConnect := func() {
		select {
		case websocketStarted <- struct{}{}:
		default:
		}
	}

	defer func() {
		log.Debugln("Appservice websocket loop finished")
	}()

	reconnectBackoff := defaultReconnectBackoff
	lastDisconnect := time.Now().UnixNano()

	for {
		err := as.StartWebsocket(url, secret, onConnect)
		if err == internal.ErrWebsocketManualStop {
			return
		} else if err != nil {
			log.Errorln("Error in appservice websocket:", err)
		}

		now := time.Now().UnixNano()
		if lastDisconnect+reconnectBackoffReset.Nanoseconds() < now {
			reconnectBackoff = defaultReconnectBackoff
		} else {
			reconnectBackoff *= 2
			if reconnectBackoff > maxReconnectBackoff {
				reconnectBackoff = maxReconnectBackoff
			}
		}
		lastDisconnect = now
		log.Infof("Websocket disconnected, reconnecting in %d seconds...", int(reconnectBackoff.Seconds()))

		<-time.After(reconnectBackoff)
	}
}
