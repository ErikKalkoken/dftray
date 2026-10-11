package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/unix"
)

const (
	gb      = 1000 * 1000 * 1000
	appName = "dftray"
)

// measurement represents a disk stats measurement.
// values are in bytes.
type measurement struct {
	available uint64
	total     uint64
}

var (
	colorDarkError      = color.NRGBA{R: 0xf4, G: 0x43, B: 0x36, A: 0xff}
	colorDarkForeground = color.NRGBA{R: 0xf3, G: 0xf3, B: 0xf3, A: 0xff}
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
	intervalFlag = flag.Int("interval", 10, "update interval in seconds")
	limitFlag    = flag.Float64("limit", 10.0, "lower storage limit in GB")
	logLevelFlag = flag.String("log-level", "warn", "log level used by the app")
	marginFlag   = flag.Float64("margin", 1.0, "storage above the limit in GB required to clear an alert")
	pathFlag     = flag.String("path", "/", "path used to query disk stats")
)

var (
	isTriggered bool
	iconNormal  []byte
	iconError   []byte
)

func main() {
	flag.Parse()

	if *intervalFlag < 1 {
		log.Fatal("interval must be 1 or larger")
	}

	if !(*limitFlag >= 0) {
		log.Fatal("limit must be 0 or larger")
	}

	if !(*marginFlag >= 0) {
		log.Fatal("margin must be 0 or larger")
	}

	l, ok := logLevelMap[strings.ToLower(*logLevelFlag)]
	if !ok {
		log.Fatalf("invalid log level: %s", *logLevelFlag)
	}
	slog.SetLogLoggerLevel(l)

	systray.Run(onReady, nil)
}

func onReady() {
	themedIcon := func(c color.Color) []byte {
		themed, err := themedPNG(iconPNGData, c)
		if err != nil {
			slog.Warn("Failed to theme icon", "error", err)
			return iconPNGData
		}
		return themed
	}
	iconNormal = themedIcon(colorDarkForeground)
	iconError = themedIcon(colorDarkError)

	systray.SetIcon(iconNormal)

	x := systray.AddMenuItem(appName, "")
	x.Disable()

	systray.AddSeparator()

	availableItem := systray.AddMenuItem("Available", "")
	availableItem.Disable()

	systray.AddSeparator()

	restartItem := systray.AddMenuItem("Restart", "")
	quitItem := systray.AddMenuItem("Quit", "")

	go func() {
		for {
			<-restartItem.ClickedCh
			restartApp()
		}
	}()

	go func() {
		<-quitItem.ClickedCh
		systray.Quit()
	}()

	go func() {
		ticker := time.NewTicker(time.Duration(*intervalFlag) * time.Second)
		defer ticker.Stop()
		for {
			updateAndCheck(availableItem)
			<-ticker.C
		}
	}()
}

// restartApp spawns a new instance of the app with the same arguments,
// then quits the current instance so the tray icon is cleaned up properly.
func restartApp() {
	exe, err := os.Executable()
	if err != nil {
		slog.Error("failed to restart", "error", err)
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		slog.Error("failed to restart", "error", err)
		return
	}
	systray.Quit()
}

func updateAndCheck(availableItem *systray.MenuItem) {
	m, err := fetchStats()
	if err != nil {
		slog.Error("Failed to fetch disk stats", "error", err)
	} else {
		slog.Info("disk stats", "stats", m)
	}

	u := evaluate(m, err, uint64(*limitFlag*gb), uint64(*marginFlag*gb), isTriggered)
	isTriggered = u.triggered
	switch u.icon {
	case iconSetError:
		systray.SetIcon(iconError)
	case iconSetNormal:
		systray.SetIcon(iconNormal)
	}
	if u.notifyText != "" {
		go notify(u.notifyText)
	}
	availableItem.SetTitle(u.title)
}

type iconChange int

const (
	iconUnchanged iconChange = iota
	iconSetNormal
	iconSetError
)

// update describes the changes resulting from one measurement.
type update struct {
	title      string     // menu title for the available item
	triggered  bool       // new alert state
	icon       iconChange // icon to set, if any
	notifyText string     // notification to send; empty means none
}

// evaluate decides what to show for a measurement.
// An alert is triggered when available storage drops below limit
// and only cleared once it rises to limit + margin or above (hysteresis).
// It has no side effects, so it can be tested without a systray.
func evaluate(m measurement, err error, limit, margin uint64, wasTriggered bool) update {
	makeAvailableTitle := func(v string) string {
		return "Available: " + v
	}

	if err != nil {
		return update{title: makeAvailableTitle("?"), triggered: wasTriggered}
	}

	var isTriggered bool
	if wasTriggered {
		isTriggered = m.available < limit+margin // stay triggered until clearly recovered
	} else {
		isTriggered = m.available < limit
	}
	availableDisplay := displayBytes(m.available)

	u := update{triggered: isTriggered}
	if isTriggered && !wasTriggered {
		u.icon = iconSetError
		u.notifyText = fmt.Sprintf(
			"Below storage limit: %s left (limit %s)",
			availableDisplay,
			displayBytes(limit),
		)
	} else if !isTriggered && wasTriggered {
		u.icon = iconSetNormal
	}

	p := float64(m.available) / float64(m.total) * 100
	u.title = makeAvailableTitle(fmt.Sprintf("%s (%.0f%%)", availableDisplay, p))
	if isTriggered {
		u.title += " !!!"
	}
	return u
}

func notify(text string) {
	err := exec.Command("notify-send", appName, text, "-a", appName).Run()
	if err != nil {
		slog.Error("failed to notify", "error", err)
	}
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
		available: uint64(stat.Bavail) * uint64(stat.Frsize),
		total:     uint64(stat.Blocks) * uint64(stat.Frsize),
	}
	if m.total == 0 {
		// e.g. pseudo filesystems like /proc
		var z measurement
		return z, fmt.Errorf("filesystem at %s has zero size", *pathFlag)
	}
	return m, nil
}

func themedPNG(in []byte, c color.Color) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	newImg := image.NewNRGBA(bounds)
	target := color.NRGBAModel.Convert(c).(color.NRGBA)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// keep the original alpha so anti-aliased edges stay smooth
			_, _, _, a := img.At(x, y).RGBA()
			px := target
			px.A = uint8(uint32(target.A) * a / 0xffff)
			newImg.SetNRGBA(x, y, px)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, newImg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
