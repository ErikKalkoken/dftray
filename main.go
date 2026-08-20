package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"golang.org/x/sys/unix"
)

const (
	updateInterval = 5 * time.Second
	gb             = 1000 * 1000 * 1000
	appName        = "dftray"
)

//go:embed icon.png
var iconPNGData []byte

var logLevelMap = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

var (
	intervalFlag = flag.Int("interval", 5, "update interval in seconds")
	pathFlag     = flag.String("path", "/", "path used to query disk stats")
	logLevelFlag = flag.String("log-level", "warn", "log level used by the app")
)

func main() {
	flag.Parse()

	if *intervalFlag < 1 {
		log.Fatal("interval must be 1 or larger")
	}

	l, ok := logLevelMap[strings.ToLower(*logLevelFlag)]
	if !ok {
		log.Fatalf("invalid log level: %s", *logLevelFlag)
	}
	slog.SetLogLoggerLevel(l)

	os.Setenv("GDK_BACKEND", "x11") // see systray issue #166

	systray.Run(onReady, nil)
}

// measurement represents a disk stats measurement.
// values are in bytes.
type measurement struct {
	available uint64
	total     uint64
}

func onReady() {
	systray.SetIcon(iconPNGData)
	systray.SetTitle("...")

	x := systray.AddMenuItem(appName, "")
	x.Disable()

	systray.AddSeparator()

	availableItem := systray.AddMenuItem("Available", "")
	availableItem.Disable()

	systray.AddSeparator()

	// notifyItem := systray.AddMenuItem("Notify", "")

	// systray.AddSeparator()

	quitItem := systray.AddMenuItem("Quit", "")

	go func() {
		<-quitItem.ClickedCh
		systray.Quit()
	}()

	// go func() {
	// 	for {
	// 		<-notifyItem.ClickedCh

	// 		title := "title"
	// 		text := "text"
	// 		err := exec.Command("notify-send", title, text, "-a", appName).Run()
	// 		if err != nil {
	// 			slog.Error("failed to notify", "error", err)
	// 		}
	// 	}
	// }()

	go func() {
		for {
			updateValues(availableItem)
			<-time.Tick(updateInterval)
		}
	}()
}

func updateValues(availableItem *systray.MenuItem) {
	var availableDisplay string
	m, err := fetchStats()
	if err != nil {
		slog.Error("Failed to fetch disk stats", "error", err)
		availableDisplay = "?"
	} else {
		slog.Info("disk stats", "stats", m)
		availableDisplay = displayBytes(m.available)
	}
	systray.SetTitle(availableDisplay)
	p := float64(m.available) / float64(m.total) * 100
	availableItem.SetTitle(fmt.Sprintf("Available: %s (%.0f%%)", availableDisplay, p))
}

func displayBytes(b uint64) string {
	v := float64(b) / gb
	return fmt.Sprintf("%.1f GB", v)
}

func fetchStats() (measurement, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(*pathFlag, &stat)
	if err != nil {
		var z measurement
		return z, err
	}
	m := measurement{
		available: uint64(stat.Bavail) * uint64(stat.Bsize),
		total:     uint64(stat.Blocks) * uint64(stat.Bsize),
	}
	return m, nil
}
