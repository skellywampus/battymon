package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type BatteryInfo struct {
	Name           string
	Status         string
	Percent        float64
	PowerNowW      float64
	VoltageNowV    float64
	CurrentNowA    float64
	EnergyNowWh    float64
	EnergyFullWh   float64
	EnergyDesignWh float64
	HealthPercent  float64
	CycleCount     int
	TemperatureC   float64
	TimeRemaining  time.Duration
	TimeToEmpty    time.Duration
	TimeToFull     time.Duration
	ACOnline       bool
	Source         string
	UpdatedAt      time.Time
	Warnings       []string
}

type SystemInfo struct {
	BrightnessPercent *float64
	CPUGovernor       string
	WifiState         string
	BluetoothState    string
}

type Snapshot struct {
	Battery BatteryInfo
	System  SystemInfo
}

type appState struct {
	app         *tview.Application
	layout      *tview.Flex
	header      *tview.TextView
	stats       *tview.Table
	graph       *tview.TextView
	footer      *tview.TextView
	historyW    []float64
	historyPct  []float64
	historyMins []float64
	maxPoints   int
	interval    time.Duration
	screenW     int
	screenH     int
}

var (
	pmsetPercentRe   = regexp.MustCompile(`(\d+)%`)
	pmsetRemainingRe = regexp.MustCompile(`;\s*(\d+:\d+)\s+remaining`)
	trailingIntRe    = regexp.MustCompile(`(-?\d+)`)
)

func main() {
	state := &appState{maxPoints: 60, interval: 2 * time.Second}
	state.app = tview.NewApplication()
	state.header = tview.NewTextView().SetDynamicColors(true)
	state.stats = tview.NewTable().SetBorders(false)
	state.graph = tview.NewTextView().SetDynamicColors(true)
	state.footer = tview.NewTextView().SetDynamicColors(true)

	state.header.SetBorder(true).SetTitle(" battymon ")
	state.stats.SetBorder(true).SetTitle(" battery ")
	state.graph.SetBorder(true).SetTitle(" trends ")
	state.footer.SetBorder(true).SetTitle(" controls ")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(state.header, 3, 0, false).
		AddItem(state.stats, 0, 2, false).
		AddItem(state.graph, 9, 0, false).
		AddItem(state.footer, 3, 0, false)
	state.layout = layout

	state.footer.SetText("[yellow]q[-] quit  [yellow]r[-] refresh")

	state.app.SetRoot(layout, true)
	state.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		w, h := screen.Size()
		state.screenW = w
		state.screenH = h
		state.adjustLayout()
		return false
	})
	state.app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyCtrlC {
			state.app.Stop()
			return nil
		}
		switch ev.Rune() {
		case 'q':
			state.app.Stop()
			return nil
		case 'r':
			go state.refresh()
			return nil
		}
		return ev
	})

	go func() {
		state.refresh()
		ticker := time.NewTicker(state.interval)
		defer ticker.Stop()
		for range ticker.C {
			state.refresh()
		}
	}()

	if err := state.app.Run(); err != nil {
		panic(err)
	}
}

func (s *appState) refresh() {
	snap, err := collectSnapshot()
	if err != nil {
		s.app.QueueUpdateDraw(func() {
			s.header.SetText(fmt.Sprintf("[red]battymon[-]  %s  %s", runtime.GOOS, err))
		})
		return
	}

	if !math.IsNaN(snap.Battery.Percent) && snap.Battery.Percent >= 0 {
		s.historyPct = appendTrim(s.historyPct, snap.Battery.Percent, s.maxPoints)
	}

	s.app.QueueUpdateDraw(func() {
		s.render(snap)
	})
}

func (s *appState) render(snap Snapshot) {
	b := snap.Battery
	sy := snap.System

	health := naFloat(b.HealthPercent, "%.1f%%")
	pct := naFloat(b.Percent, "%.1f%%")
	pow := formatPower(b.PowerNowW)
	volts := naFloat(b.VoltageNowV, "%.2f V")
	amps := naFloat(b.CurrentNowA, "%.3f A")
	nowWh := naFloat(b.EnergyNowWh, "%.2f Wh")
	fullWh := naFloat(b.EnergyFullWh, "%.2f Wh")
	designWh := naFloat(b.EnergyDesignWh, "%.2f Wh")
	temp := naFloat(b.TemperatureC, "%.1f C")
	cycles := "N/A"
	if b.CycleCount >= 0 {
		cycles = strconv.Itoa(b.CycleCount)
	}
	ac := "No"
	if b.ACOnline {
		ac = "Yes"
	}
	bright := "N/A"
	if sy.BrightnessPercent != nil {
		bright = fmt.Sprintf("%.0f%%", *sy.BrightnessPercent)
	}
	eta := "N/A"
	if b.TimeRemaining > 0 {
		eta = durationShort(b.TimeRemaining)
	} else if !b.ACOnline && b.TimeToEmpty > 0 {
		eta = durationShort(b.TimeToEmpty)
	} else if b.ACOnline && b.TimeToFull > 0 {
		eta = durationShort(b.TimeToFull)
	}
	_, _, graphW, _ := s.graph.GetInnerRect()
	levelBar := percentBar(b.Percent, responsiveBarWidth(graphW))

	s.header.SetText(fmt.Sprintf("[green]battymon[-]  os=%s  source=%s  battery=%s  updated=%s", runtime.GOOS, b.Source, fallback(b.Name, "unknown"), b.UpdatedAt.Format("15:04:05")))

	s.stats.Clear()
	rows := make([][2]string, 0, 17)
	rows = append(rows, [2]string{"Status", fallback(b.Status, "Unknown")})
	rows = append(rows, [2]string{"Percent", pct})
	rows = append(rows, [2]string{"AC Online", ac})
	appendRowIfKnown(&rows, "Time Remaining", eta)
	appendRowIfKnown(&rows, "Power", pow)
	appendRowIfKnown(&rows, "Voltage", volts)
	appendRowIfKnown(&rows, "Current", amps)
	appendRowIfKnown(&rows, "Energy Now", nowWh)
	appendRowIfKnown(&rows, "Energy Full", fullWh)
	appendRowIfKnown(&rows, "Design Capacity", designWh)
	appendRowIfKnown(&rows, "Health", health)
	appendRowIfKnown(&rows, "Cycle Count", cycles)
	appendRowIfKnown(&rows, "Temperature", temp)
	appendRowIfKnown(&rows, "Brightness", bright)
	appendRowIfKnown(&rows, "CPU Governor", fallback(sy.CPUGovernor, "N/A"))
	appendRowIfKnown(&rows, "Wi-Fi", fallback(sy.WifiState, "N/A"))
	appendRowIfKnown(&rows, "Bluetooth", fallback(sy.BluetoothState, "N/A"))
	for i, row := range rows {
		s.stats.SetCell(i, 0, tview.NewTableCell("[yellow]"+row[0]).SetExpansion(1))
		s.stats.SetCell(i, 1, tview.NewTableCell(row[1]).SetExpansion(2))
	}

	var warnings string
	if len(b.Warnings) > 0 {
		warnings = "\n[yellow]Warnings[-]:\n - " + strings.Join(b.Warnings, "\n - ")
	}
	graph := strings.Builder{}
	graph.WriteString("[aqua]Battery %:[-] ")
	graph.WriteString(levelBar)
	graph.WriteString("\n\n[aqua]Power draw:[-] ")
	graph.WriteString(pow)
	graph.WriteString("\n\n[aqua]ETA:[-] ")
	graph.WriteString(eta)
	graph.WriteString(warnings)
	s.graph.SetText(graph.String())
}

func (s *appState) adjustLayout() {
	if s.layout == nil {
		return
	}
	headerH := 3
	footerH := 3
	graphH := 9
	if s.screenH > 0 && s.screenH < 24 {
		headerH = 2
		footerH = 2
		graphH = 5
	}
	if s.screenH > 0 && s.screenH < 16 {
		headerH = 1
		footerH = 1
		graphH = 3
	}
	s.layout.ResizeItem(s.header, headerH, 0)
	s.layout.ResizeItem(s.graph, graphH, 0)
	s.layout.ResizeItem(s.footer, footerH, 0)
}

func collectSnapshot() (Snapshot, error) {
	var b BatteryInfo
	var err error
	s := SystemInfo{}

	switch runtime.GOOS {
	case "linux":
		b, err = collectLinuxBattery()
		s = collectLinuxSystem()
	case "darwin":
		b, err = collectDarwinBattery()
		s = collectDarwinSystem()
	default:
		return Snapshot{}, fmt.Errorf("unsupported os: %s", runtime.GOOS)
	}
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Battery: b, System: s}, nil
}

func collectLinuxBattery() (BatteryInfo, error) {
	return collectLinuxBatteryFromBase("/sys/class/power_supply")
}

func collectLinuxBatteryFromBase(base string) (BatteryInfo, error) {
	bats, err := linuxBatteryPaths(base)
	if err != nil {
		return BatteryInfo{}, err
	}
	if len(bats) == 0 {
		return BatteryInfo{}, errors.New("no BAT* devices found")
	}
	parts := make([]BatteryInfo, 0, len(bats))
	for _, bat := range bats {
		parts = append(parts, collectLinuxBatteryFromPath(bat))
	}
	if len(parts) == 1 {
		info := parts[0]
		info.ACOnline = readAnyACOnline(base)
		finalizeBatteryDerivedFields(&info)
		return info, nil
	}

	info := BatteryInfo{
		Name:           fmt.Sprintf("%d batteries", len(parts)),
		Status:         aggregateLinuxStatus(parts),
		Percent:        math.NaN(),
		PowerNowW:      sumFloat(parts, func(b BatteryInfo) float64 { return b.PowerNowW }),
		VoltageNowV:    avgFloat(parts, func(b BatteryInfo) float64 { return b.VoltageNowV }),
		CurrentNowA:    sumFloat(parts, func(b BatteryInfo) float64 { return b.CurrentNowA }),
		EnergyNowWh:    sumFloat(parts, func(b BatteryInfo) float64 { return b.EnergyNowWh }),
		EnergyFullWh:   sumFloat(parts, func(b BatteryInfo) float64 { return b.EnergyFullWh }),
		EnergyDesignWh: sumFloat(parts, func(b BatteryInfo) float64 { return b.EnergyDesignWh }),
		HealthPercent:  math.NaN(),
		CycleCount:     -1,
		TemperatureC:   avgFloat(parts, func(b BatteryInfo) float64 { return b.TemperatureC }),
		ACOnline:       readAnyACOnline(base),
		Source:         "linux-sysfs",
		UpdatedAt:      time.Now(),
	}
	percentSum := 0.0
	percentCount := 0
	for _, part := range parts {
		if part.CycleCount > info.CycleCount {
			info.CycleCount = part.CycleCount
		}
		if !math.IsNaN(part.Percent) {
			percentSum += part.Percent
			percentCount++
		}
		info.Warnings = append(info.Warnings, part.Warnings...)
	}
	finalizeBatteryDerivedFields(&info)
	if math.IsNaN(info.Percent) && percentCount > 0 {
		info.Percent = percentSum / float64(percentCount)
	}
	info.Warnings = appendUnique(info.Warnings, fmt.Sprintf("aggregated stats across %d batteries", len(parts)))
	return info, nil
}

func collectLinuxSystem() SystemInfo {
	s := SystemInfo{}
	if v, ok := linuxBrightnessPercent(); ok {
		s.BrightnessPercent = &v
	}
	s.CPUGovernor = firstReadableGlob("/sys/devices/system/cpu/cpu*/cpufreq/scaling_governor")
	s.WifiState = linuxRfkillType("wlan")
	s.BluetoothState = linuxRfkillType("bluetooth")
	return s
}

func collectDarwinBattery() (BatteryInfo, error) {
	info := BatteryInfo{
		Name:           "InternalBattery",
		Status:         "Unknown",
		Percent:        math.NaN(),
		PowerNowW:      math.NaN(),
		VoltageNowV:    math.NaN(),
		CurrentNowA:    math.NaN(),
		EnergyNowWh:    math.NaN(),
		EnergyFullWh:   math.NaN(),
		EnergyDesignWh: math.NaN(),
		HealthPercent:  math.NaN(),
		CycleCount:     -1,
		TemperatureC:   math.NaN(),
		Source:         "darwin-pmset+ioreg",
		UpdatedAt:      time.Now(),
	}

	pmsetOut, _ := run("pmset", "-g", "batt")
	parsePmset(&info, pmsetOut)

	ioregOut, err := run("ioreg", "-r", "-c", "AppleSmartBattery")
	if err != nil || strings.TrimSpace(ioregOut) == "" {
		return info, errors.New("unable to read macOS battery data via pmset/ioreg")
	}
	parseIORegLoose(&info, ioregOut)
	normalizeBatteryFields(&info)
	derivePowerIfMissing(&info)

	if !math.IsNaN(info.EnergyFullWh) && !math.IsNaN(info.EnergyDesignWh) && info.EnergyDesignWh > 0 {
		info.HealthPercent = info.EnergyFullWh / info.EnergyDesignWh * 100
	}
	if math.IsNaN(info.Percent) && !math.IsNaN(info.EnergyNowWh) && !math.IsNaN(info.EnergyFullWh) && info.EnergyFullWh > 0 {
		info.Percent = info.EnergyNowWh / info.EnergyFullWh * 100
	}
	if !math.IsNaN(info.PowerNowW) && info.PowerNowW > 0 {
		if isDischargingStatus(info.Status) && !math.IsNaN(info.EnergyNowWh) {
			hours := info.EnergyNowWh / info.PowerNowW
			if hours > 0 && hours < 1000 {
				info.TimeToEmpty = time.Duration(hours * float64(time.Hour))
			}
		} else if isChargingStatus(info.Status) && !math.IsNaN(info.EnergyFullWh) && !math.IsNaN(info.EnergyNowWh) {
			remaining := info.EnergyFullWh - info.EnergyNowWh
			if remaining > 0 {
				hours := remaining / info.PowerNowW
				if hours > 0 && hours < 1000 {
					info.TimeToFull = time.Duration(hours * float64(time.Hour))
				}
			}
		}
	}
	selectDisplayedETA(&info)

	if math.IsNaN(info.Percent) {
		return info, errors.New("battery percentage not found")
	}
	return info, nil
}

func collectDarwinSystem() SystemInfo {
	s := SystemInfo{}
	if out, err := run("pmset", "-g", "custom"); err == nil {
		_ = out
	}
	s.CPUGovernor = "N/A"
	s.WifiState = "N/A"
	s.BluetoothState = "N/A"
	return s
}

func parsePmset(info *BatteryInfo, out string) {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "%") {
			if m := pmsetPercentRe.FindStringSubmatch(line); len(m) == 2 {
				info.Percent = parseFloat(m[1])
			}
			lower := strings.ToLower(line)
			switch {
			case strings.Contains(lower, "not charging"):
				info.Status = "Not charging"
			case strings.Contains(lower, "discharging"):
				info.Status = "Discharging"
			case strings.Contains(lower, "charging"):
				info.Status = "Charging"
			case strings.Contains(lower, "charged"):
				info.Status = "Full"
			}
			if m := pmsetRemainingRe.FindStringSubmatch(line); len(m) == 2 {
				remaining := parseHourMinute(m[1])
				if isChargingStatus(info.Status) {
					info.TimeToFull = remaining
				} else {
					info.TimeToEmpty = remaining
				}
				info.TimeRemaining = remaining
			}
		}
		if strings.Contains(strings.ToLower(line), "ac power") {
			info.ACOnline = true
		}
	}
}

func parseIORegLoose(info *BatteryInfo, out string) {
	lines := strings.Split(out, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if v, ok := extractIORegInt(line, "CycleCount"); ok {
			info.CycleCount = v
		}
		if v, ok := extractIORegInt(line, "Temperature"); ok && v >= 0 {
			if v > 5000 {
				info.TemperatureC = float64(v)/100.0 - 273.15
			} else if v > 2000 {
				info.TemperatureC = float64(v)/10.0 - 273.15
			} else {
				info.TemperatureC = float64(v) / 100.0
			}
		}
		if v, ok := extractIORegNum(line, "Voltage"); ok && v >= 0 {
			info.VoltageNowV = float64(v) / 1000.0
			if !math.IsNaN(info.CurrentNowA) {
				info.PowerNowW = info.VoltageNowV * info.CurrentNowA
			}
		}
		if v, ok := extractIORegNum(line, "AppleRawBatteryVoltage"); ok && v >= 0 && math.IsNaN(info.VoltageNowV) {
			info.VoltageNowV = float64(v) / 1000.0
			if !math.IsNaN(info.CurrentNowA) {
				info.PowerNowW = info.VoltageNowV * info.CurrentNowA
			}
		}
		if v, ok := extractIORegNum(line, "Amperage"); ok {
			amps := math.Abs(float64(v)) / 1000.0
			info.CurrentNowA = amps
			if !math.IsNaN(info.VoltageNowV) {
				info.PowerNowW = info.VoltageNowV * amps
			}
		}
		if v, ok := extractIORegNum(line, "InstantAmperage"); ok && math.IsNaN(info.CurrentNowA) {
			amps := math.Abs(float64(v)) / 1000.0
			info.CurrentNowA = amps
			if !math.IsNaN(info.VoltageNowV) {
				info.PowerNowW = info.VoltageNowV * amps
			}
		}
		if v, ok := extractIORegInt(line, "AppleRawCurrentCapacity"); ok && v >= 0 && !math.IsNaN(info.VoltageNowV) {
			info.EnergyNowWh = float64(v) * info.VoltageNowV / 1000.0
		} else if v, ok := extractIORegInt(line, "NominalChargeCapacity"); ok && v >= 0 && !math.IsNaN(info.VoltageNowV) {
			info.EnergyNowWh = float64(v) * info.VoltageNowV / 1000.0
		}
		if v, ok := extractIORegInt(line, "AppleRawMaxCapacity"); ok && v >= 0 && !math.IsNaN(info.VoltageNowV) {
			info.EnergyFullWh = float64(v) * info.VoltageNowV / 1000.0
		} else if v, ok := extractIORegInt(line, "MaxCapacity"); ok && v > 100 && !math.IsNaN(info.VoltageNowV) {
			info.EnergyFullWh = float64(v) * info.VoltageNowV / 1000.0
		}
		if v, ok := extractIORegInt(line, "DesignCapacity"); ok && v > 100 && !math.IsNaN(info.VoltageNowV) {
			info.EnergyDesignWh = float64(v) * info.VoltageNowV / 1000.0
		}
		if v, ok := extractIORegInt(line, "ExternalConnected"); ok {
			if v != 0 {
				info.ACOnline = true
			}
		}
		if v, ok := extractIORegInt(line, "TimeRemaining"); ok && v > 0 && v < 65535 {
			info.TimeToEmpty = time.Duration(v) * time.Minute
		}
		if v, ok := extractIORegInt(line, "AvgTimeToEmpty"); ok && v > 0 && v < 65535 && !isChargingStatus(info.Status) {
			info.TimeToEmpty = time.Duration(v) * time.Minute
		}
		if v, ok := extractIORegInt(line, "AvgTimeToFull"); ok && v > 0 && v < 65535 && isChargingStatus(info.Status) {
			info.TimeToFull = time.Duration(v) * time.Minute
		}
		if v, ok := extractIORegInt(line, "IsCharging"); ok && v != 0 {
			info.Status = "Charging"
		}
		if isIORegBoolTrue(line, "ExternalConnected") {
			info.ACOnline = true
		}
		if isIORegBoolTrue(line, "IsCharging") {
			info.Status = "Charging"
		}
		if (math.IsNaN(info.PowerNowW) || info.PowerNowW <= 0) && strings.Contains(line, "\"BatteryPower\"") {
			if mw, ok := extractIORegNum(line, "BatteryPower"); ok && mw != 0 {
				info.PowerNowW = math.Abs(float64(mw)) / 1000.0
			}
		}
	}
	selectDisplayedETA(info)
}

func linuxBrightnessPercent() (float64, bool) {
	bases, _ := filepath.Glob("/sys/class/backlight/*")
	if len(bases) == 0 {
		return 0, false
	}
	cur := readFloatScale(filepath.Join(bases[0], "brightness"), 1)
	max := readFloatScale(filepath.Join(bases[0], "max_brightness"), 1)
	if math.IsNaN(cur) || math.IsNaN(max) || max <= 0 {
		return 0, false
	}
	return cur / max * 100, true
}

func linuxRfkillType(kind string) string {
	bases, _ := filepath.Glob("/sys/class/rfkill/rfkill*")
	for _, b := range bases {
		t := readTrim(filepath.Join(b, "type"))
		if t == kind {
			soft := readTrim(filepath.Join(b, "soft"))
			hard := readTrim(filepath.Join(b, "hard"))
			if soft == "1" || hard == "1" {
				return "Blocked"
			}
			return "Enabled"
		}
	}
	return "N/A"
}

func readAnyACOnline(base string) bool {
	ents, _ := os.ReadDir(base)
	for _, e := range ents {
		path := filepath.Join(base, e.Name(), "online")
		if readTrim(path) == "1" {
			return true
		}
	}
	return false
}

func firstReadableGlob(pattern string) string {
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		if v := readTrim(m); v != "" {
			return v
		}
	}
	return ""
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readFloatScale(path string, scale float64) float64 {
	v := readTrim(path)
	if v == "" {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return math.NaN()
	}
	return f / scale
}

func readIntDefault(path string, def int) int {
	v := readTrim(path)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

func extractTrailingInt(line string, def int) int {
	m := trailingIntRe.FindAllString(line, -1)
	if len(m) == 0 {
		return def
	}
	v, err := strconv.Atoi(m[len(m)-1])
	if err != nil {
		return def
	}
	return v
}

func parseHourMinute(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
}

func durationShort(d time.Duration) string {
	if d <= 0 {
		return "N/A"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func naFloat(v float64, format string) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "N/A"
	}
	return fmt.Sprintf(format, v)
}

func fallback(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func appendTrim(slice []float64, v float64, max int) []float64 {
	slice = append(slice, v)
	if len(slice) > max {
		slice = slice[len(slice)-max:]
	}
	return slice
}

func sparkline(values []float64, width int) string {
	if len(values) == 0 {
		return "(no data yet)"
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	vals := values
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	minV, maxV := vals[0], vals[0]
	for _, v := range vals {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if maxV-minV == 0 {
		return strings.Repeat("▄", len(vals))
	}
	var b strings.Builder
	for _, v := range vals {
		n := int(math.Round((v - minV) / (maxV - minV) * 7))
		if n < 0 {
			n = 0
		}
		if n > 7 {
			n = 7
		}
		b.WriteRune(bars[n])
	}
	b.WriteString(fmt.Sprintf("  min=%.2f max=%.2f", minV, maxV))
	return b.String()
}

func linuxBatteryPaths(base string) ([]string, error) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var bats []string
	for _, e := range ents {
		p := filepath.Join(base, e.Name())
		typ := strings.ToLower(readTrim(filepath.Join(p, "type")))
		if typ == "battery" {
			bats = append(bats, p)
			continue
		}
		if typ == "" && strings.HasPrefix(e.Name(), "BAT") {
			bats = append(bats, p)
		}
	}
	sort.Strings(bats)
	return bats, nil
}

func collectLinuxBatteryFromPath(bat string) BatteryInfo {
	info := BatteryInfo{
		Name:           filepath.Base(bat),
		Status:         readTrim(filepath.Join(bat, "status")),
		Percent:        readFloatScale(filepath.Join(bat, "capacity"), 1),
		PowerNowW:      math.NaN(),
		VoltageNowV:    math.NaN(),
		CurrentNowA:    math.NaN(),
		EnergyNowWh:    math.NaN(),
		EnergyFullWh:   math.NaN(),
		EnergyDesignWh: math.NaN(),
		HealthPercent:  math.NaN(),
		CycleCount:     readIntDefault(filepath.Join(bat, "cycle_count"), -1),
		TemperatureC:   readFloatScale(filepath.Join(bat, "temp"), 10),
		Source:         "linux-sysfs",
		UpdatedAt:      time.Now(),
	}
	energyNow := readFloatScale(filepath.Join(bat, "energy_now"), 1_000_000)
	energyFull := readFloatScale(filepath.Join(bat, "energy_full"), 1_000_000)
	energyDesign := readFloatScale(filepath.Join(bat, "energy_full_design"), 1_000_000)
	powerNow := readFloatScale(filepath.Join(bat, "power_now"), 1_000_000)
	voltageNow := readFloatScale(filepath.Join(bat, "voltage_now"), 1_000_000)
	currentNow := readFloatScale(filepath.Join(bat, "current_now"), 1_000_000)
	if !math.IsNaN(currentNow) {
		currentNow = math.Abs(currentNow)
	}
	if !math.IsNaN(powerNow) {
		powerNow = math.Abs(powerNow)
	}

	if math.IsNaN(energyNow) {
		chargeNow := readFloatScale(filepath.Join(bat, "charge_now"), 1_000_000)
		chargeFull := readFloatScale(filepath.Join(bat, "charge_full"), 1_000_000)
		chargeDesign := readFloatScale(filepath.Join(bat, "charge_full_design"), 1_000_000)
		if !math.IsNaN(chargeNow) && !math.IsNaN(voltageNow) {
			energyNow = chargeNow * voltageNow
		}
		if !math.IsNaN(chargeFull) && !math.IsNaN(voltageNow) {
			energyFull = chargeFull * voltageNow
		}
		if !math.IsNaN(chargeDesign) && !math.IsNaN(voltageNow) {
			energyDesign = chargeDesign * voltageNow
		}
	}
	if math.IsNaN(powerNow) && !math.IsNaN(currentNow) && !math.IsNaN(voltageNow) {
		powerNow = math.Abs(currentNow * voltageNow)
	}

	info.PowerNowW = powerNow
	info.VoltageNowV = voltageNow
	info.CurrentNowA = currentNow
	info.EnergyNowWh = energyNow
	info.EnergyFullWh = energyFull
	info.EnergyDesignWh = energyDesign
	finalizeBatteryDerivedFields(&info)
	return info
}

func finalizeBatteryDerivedFields(info *BatteryInfo) {
	normalizeBatteryFields(info)
	derivePowerIfMissing(info)
	if !math.IsNaN(info.EnergyFullWh) && !math.IsNaN(info.EnergyDesignWh) && info.EnergyDesignWh > 0 {
		info.HealthPercent = info.EnergyFullWh / info.EnergyDesignWh * 100
	}
	if math.IsNaN(info.Percent) && !math.IsNaN(info.EnergyNowWh) && !math.IsNaN(info.EnergyFullWh) && info.EnergyFullWh > 0 {
		info.Percent = info.EnergyNowWh / info.EnergyFullWh * 100
	}
	if math.IsNaN(info.Percent) {
		info.Warnings = appendUnique(info.Warnings, "battery percentage not exposed")
	}
	statusLower := strings.ToLower(info.Status)
	if !math.IsNaN(info.PowerNowW) && info.PowerNowW > 0 {
		if strings.Contains(statusLower, "discharg") && !math.IsNaN(info.EnergyNowWh) {
			hours := info.EnergyNowWh / info.PowerNowW
			if hours > 0 && hours < 1000 {
				info.TimeRemaining = time.Duration(hours * float64(time.Hour))
			}
		} else if strings.Contains(statusLower, "charg") && !math.IsNaN(info.EnergyFullWh) && !math.IsNaN(info.EnergyNowWh) {
			remaining := info.EnergyFullWh - info.EnergyNowWh
			if remaining > 0 {
				hours := remaining / info.PowerNowW
				if hours > 0 && hours < 1000 {
					info.TimeRemaining = time.Duration(hours * float64(time.Hour))
				}
			}
		}
	}
}

func aggregateLinuxStatus(parts []BatteryInfo) string {
	hasCharging := false
	hasDischarging := false
	for _, p := range parts {
		status := strings.ToLower(p.Status)
		if strings.Contains(status, "discharg") {
			hasDischarging = true
			continue
		}
		if strings.Contains(status, "charg") {
			hasCharging = true
		}
	}
	switch {
	case hasCharging && hasDischarging:
		return "Mixed"
	case hasCharging:
		return "Charging"
	case hasDischarging:
		return "Discharging"
	}
	for _, p := range parts {
		if strings.TrimSpace(p.Status) != "" {
			return p.Status
		}
	}
	return "Unknown"
}

func sumFloat(parts []BatteryInfo, selector func(BatteryInfo) float64) float64 {
	sum := 0.0
	count := 0
	for _, p := range parts {
		v := selector(p)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		sum += v
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum
}

func avgFloat(parts []BatteryInfo, selector func(BatteryInfo) float64) float64 {
	sum := 0.0
	count := 0
	for _, p := range parts {
		v := selector(p)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		sum += v
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

func appendUnique(values []string, candidate string) []string {
	if strings.TrimSpace(candidate) == "" {
		return values
	}
	for _, v := range values {
		if v == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func extractIORegInt(line, key string) (int, bool) {
	v, ok := extractIORegNum(line, key)
	if !ok {
		return 0, false
	}
	if v < math.MinInt || v > math.MaxInt {
		return 0, false
	}
	return int(v), true
}

func extractIORegNum(line, key string) (int64, bool) {
	prefix := fmt.Sprintf("\"%s\" = ", key)
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if raw == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n, true
	}
	if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return int64(n), true
	}
	return 0, false
}

func isIORegBoolTrue(line, key string) bool {
	prefix := fmt.Sprintf("\"%s\" = ", key)
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	l := strings.ToLower(line)
	return strings.Contains(l, "yes") || strings.Contains(l, "true")
}

func appendRowIfKnown(rows *[][2]string, label, value string) {
	if value == "" || value == "N/A" {
		return
	}
	*rows = append(*rows, [2]string{label, value})
}

func percentBar(p float64, width int) string {
	if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 {
		return "(no battery percentage data)"
	}
	if p > 100 {
		p = 100
	}
	filled := int(math.Round((p / 100.0) * float64(width)))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	empty := width - filled
	color := "red"
	switch {
	case p >= 60:
		color = "green"
	case p >= 30:
		color = "yellow"
	}
	return fmt.Sprintf("[%s]%s%s[-] %.1f%%", color, strings.Repeat("█", filled), strings.Repeat("░", empty), p)
}

func responsiveBarWidth(graphInnerWidth int) int {
	w := graphInnerWidth - 18
	if w < 10 {
		return 10
	}
	if w > 80 {
		return 80
	}
	return w
}

func isChargingStatus(status string) bool {
	s := strings.ToLower(status)
	if strings.Contains(s, "not charg") {
		return false
	}
	if strings.Contains(s, "discharg") {
		return false
	}
	return strings.Contains(s, "charging")
}

func isDischargingStatus(status string) bool {
	return strings.Contains(strings.ToLower(status), "discharg")
}

func selectDisplayedETA(info *BatteryInfo) {
	if info.ACOnline {
		if info.TimeToFull > 0 {
			info.TimeRemaining = info.TimeToFull
			return
		}
		if info.TimeToEmpty > 0 {
			info.TimeRemaining = info.TimeToEmpty
		}
		return
	}
	if info.TimeToEmpty > 0 {
		info.TimeRemaining = info.TimeToEmpty
	}
}

func normalizeBatteryFields(info *BatteryInfo) {
	if !math.IsNaN(info.EnergyNowWh) && !math.IsNaN(info.EnergyFullWh) && info.EnergyFullWh > 0 && info.EnergyNowWh > info.EnergyFullWh {
		info.EnergyNowWh = info.EnergyFullWh
	}
}

func derivePowerIfMissing(info *BatteryInfo) {
	if !math.IsNaN(info.PowerNowW) && info.PowerNowW > 0 {
		return
	}
	if info.TimeToEmpty > 0 && !math.IsNaN(info.EnergyNowWh) && info.EnergyNowWh > 0 {
		hours := info.TimeToEmpty.Hours()
		if hours > 0 {
			p := info.EnergyNowWh / hours
			if p > 0 && p < 1000 {
				info.PowerNowW = p
				return
			}
		}
	}
	if info.TimeToFull > 0 && !math.IsNaN(info.EnergyFullWh) && !math.IsNaN(info.EnergyNowWh) && info.EnergyFullWh > info.EnergyNowWh {
		hours := info.TimeToFull.Hours()
		if hours > 0 {
			p := (info.EnergyFullWh - info.EnergyNowWh) / hours
			if p > 0 && p < 1000 {
				info.PowerNowW = p
			}
		}
	}
}

func formatPower(w float64) string {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return "N/A"
	}
	if w < 1 {
		return fmt.Sprintf("%.3f W", w)
	}
	return fmt.Sprintf("%.2f W", w)
}
