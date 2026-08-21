package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework Carbon

#include <CoreGraphics/CoreGraphics.h>
#include <Carbon/Carbon.h>
#include <stdlib.h>
#include <string.h>

extern void goKeyCallback(int keyCode, int flags);
extern void goTapDisabled(int byUserInput);

static CFMachPortRef eventTap = NULL;

static CGEventRef eventCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    // macOS disables the tap if the callback is slow (watchdog) or on user-input
    // protection; without re-enabling it here the tap stays dead forever.
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (eventTap) CGEventTapEnable(eventTap, true);
        goTapDisabled(type == kCGEventTapDisabledByUserInput ? 1 : 0);
        return event;
    }
    if (type == kCGEventFlagsChanged) {
        CGEventFlags flags = CGEventGetFlags(event);
        int keyCode = (int)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        goKeyCallback(keyCode, (int)flags);
    }
    return event;
}

static void startEventTap() {
    CGEventMask mask = CGEventMaskBit(kCGEventFlagsChanged);
    CFMachPortRef tap = CGEventTapCreate(
        kCGSessionEventTap,
        kCGHeadInsertEventTap,
        kCGEventTapOptionDefault,
        mask,
        eventCallback,
        NULL
    );

    if (!tap) {
        printf("Failed to create event tap. Check Accessibility permissions.\n");
        return;
    }
    eventTap = tap;

    CFRunLoopSourceRef source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopCommonModes);
    CGEventTapEnable(tap, true);
    CFRunLoopRun();
}

static CFStringRef getCurrentInputSource() {
    TISInputSourceRef source = TISCopyCurrentKeyboardInputSource();
    if (!source) return NULL;

    CFStringRef sourceID = (CFStringRef)TISGetInputSourceProperty(source, kTISPropertyInputSourceID);
    CFStringRef result = CFStringCreateCopy(kCFAllocatorDefault, sourceID);
    CFRelease(source);
    return result;
}

static int selectInputSource(const char *sourceID) {
    CFStringRef targetID = CFStringCreateWithCString(kCFAllocatorDefault, sourceID, kCFStringEncodingUTF8);

    CFArrayRef sources = TISCreateInputSourceList(NULL, false);
    if (!sources) {
        CFRelease(targetID);
        return -1;
    }

    CFIndex count = CFArrayGetCount(sources);
    int found = 0;

    for (CFIndex i = 0; i < count; i++) {
        TISInputSourceRef source = (TISInputSourceRef)CFArrayGetValueAtIndex(sources, i);
        CFStringRef sourceIDProp = (CFStringRef)TISGetInputSourceProperty(source, kTISPropertyInputSourceID);

        if (sourceIDProp && CFStringCompare(sourceIDProp, targetID, 0) == kCFCompareEqualTo) {
            TISSelectInputSource(source);
            found = 1;
            break;
        }
    }

    CFRelease(sources);
    CFRelease(targetID);
    return found ? 0 : -1;
}

static const char* getSelectableKeyboardLayouts() {
    CFArrayRef sources = TISCreateInputSourceList(NULL, false);
    if (!sources) return "";

    static char buffer[4096];
    buffer[0] = '\0';
    int offset = 0;

    CFStringRef prefix = CFSTR("com.apple.keylayout.");
    CFIndex count = CFArrayGetCount(sources);

    for (CFIndex i = 0; i < count; i++) {
        TISInputSourceRef source = (TISInputSourceRef)CFArrayGetValueAtIndex(sources, i);
        CFBooleanRef selectable = (CFBooleanRef)TISGetInputSourceProperty(source, kTISPropertyInputSourceIsSelectCapable);

        if (!selectable || !CFBooleanGetValue(selectable)) continue;

        CFStringRef sourceID = (CFStringRef)TISGetInputSourceProperty(source, kTISPropertyInputSourceID);
        if (!sourceID) continue;

        if (!CFStringHasPrefix(sourceID, prefix)) continue;

        char tmp[256];
        CFStringGetCString(sourceID, tmp, sizeof(tmp), kCFStringEncodingUTF8);

        int len = strlen(tmp);
        if (offset + len + 2 > (int)sizeof(buffer)) break;

        if (offset > 0) {
            buffer[offset++] = '\n';
        }
        memcpy(buffer + offset, tmp, len);
        offset += len;
        buffer[offset] = '\0';
    }

    CFRelease(sources);
    return buffer;
}

static const char* getCurrentInputSourceString() {
    CFStringRef source = getCurrentInputSource();
    if (!source) return "";

    static char buffer[256];
    CFStringGetCString(source, buffer, sizeof(buffer), kCFStringEncodingUTF8);
    CFRelease(source);
    return buffer;
}
*/
import "C"

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

const (
	fnKeyFlag         = 0x800000 // NSEventModifierFlagFunction
	shiftFlag         = 0x20000  // kCGEventFlagMaskShift
	optionFlag        = 0x80000  // kCGEventFlagMaskAlternate
	keylayoutPrefix   = "com.apple.keylayout."
	longPressDuration = 500 * time.Millisecond
	// Mask covering standard modifier keys: shift, control, option, command, fn (excludes caps lock and numpad)
	modifierMask = shiftFlag | 0x40000 | optionFlag | 0x100000 | fnKeyFlag // shift, ctrl, option, cmd, fn
)

var (
	buildDate string
	commit    string
	version   string
)

var (
	fnPressed          = false
	fnTimer            *time.Timer
	shortcutEnabled    = false
	shiftOptionPressed = false
	shiftOptionTimer   *time.Timer
	layouts            []string
	previousLayout     string
	cycleMode          bool
	switchRequests     = make(chan bool, 16)
)

//export goKeyCallback
func goKeyCallback(keyCode C.int, flags C.int) {
	f := int(flags)

	// Fn key handling (always active)
	fnNow := (f & fnKeyFlag) != 0

	if fnNow && !fnPressed {
		if cycleMode {
			requestSwitch(false)
		} else {
			fnTimer = time.AfterFunc(longPressDuration, func() {
				requestSwitch(true)
			})
		}
	} else if !fnNow && fnPressed && !cycleMode {
		if fnTimer != nil && fnTimer.Stop() {
			requestSwitch(false)
		}
	}
	fnPressed = fnNow

	// Shift+Option handling (only when enabled)
	if !shortcutEnabled {
		return
	}

	// Check if exactly Shift+Option are pressed (no other modifiers besides caps lock/numpad)
	activeModifiers := f & modifierMask
	shiftOptionNow := activeModifiers == (shiftFlag | optionFlag)

	if shiftOptionNow && !shiftOptionPressed {
		if cycleMode {
			requestSwitch(false)
		} else {
			shiftOptionTimer = time.AfterFunc(longPressDuration, func() {
				requestSwitch(true)
			})
		}
	} else if !shiftOptionNow && shiftOptionPressed && !cycleMode {
		if shiftOptionTimer != nil && shiftOptionTimer.Stop() {
			requestSwitch(false)
		}
	}
	shiftOptionPressed = shiftOptionNow
}

//export goTapDisabled
func goTapDisabled(byUserInput C.int) {
	reason := "timeout"
	if byUserInput != 0 {
		reason = "user input"
	}
	fmt.Printf("[%s] Event tap disabled by %s; re-enabled\n", timestamp(), reason)
}

func timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// requestSwitch queues a layout switch for the worker goroutine. The event tap
// callback must return fast — a slow callback trips the macOS watchdog, which
// disables the tap — so TIS calls never run on the tap thread. A single worker
// also serializes access to previousLayout.
func requestSwitch(longPress bool) {
	select {
	case switchRequests <- longPress:
	default:
	}
}

func switchWorker() {
	for longPress := range switchRequests {
		switchInputSource(longPress)
	}
}

func getCurrentLayout() string {
	cstr := C.getCurrentInputSourceString()
	return C.GoString(cstr)
}

func setLayout(layoutID string) error {
	cstr := C.CString(layoutID)
	defer C.free(unsafe.Pointer(cstr))

	result := C.selectInputSource(cstr)
	if result != 0 {
		return fmt.Errorf("failed to select input source: %s", layoutID)
	}
	return nil
}

func getKeyboardLayouts() []string {
	cstr := C.getSelectableKeyboardLayouts()
	raw := C.GoString(cstr)
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

func listLayouts() {
	for _, l := range getKeyboardLayouts() {
		fmt.Println(l)
	}
}

type config struct {
	Layouts  []string `json:"layouts"`
	Cycle    *bool    `json:"cycle,omitempty"`
	Shortcut string   `json:"shortcut,omitempty"`
}

func configFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "fn-switcher", "config.json")
}

func loadConfigFile() (*config, error) {
	path := configFilePath()
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	return &cfg, nil
}

func createDefaultConfig() error {
	path := configFilePath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	shortLayouts := make([]string, len(layouts))
	for i, l := range layouts {
		shortLayouts[i] = strings.TrimPrefix(l, keylayoutPrefix)
	}

	cfg := config{Layouts: shortLayouts, Cycle: &cycleMode}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return err
	}

	fmt.Printf("Config created: %s\n", path)
	return nil
}

func loadEnvVars() *config {
	cfg := &config{}
	hasAny := false

	if val := os.Getenv("FN_SWITCHER_LAYOUTS"); val != "" {
		parts := strings.Split(val, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				cfg.Layouts = append(cfg.Layouts, p)
			}
		}
		if len(cfg.Layouts) > 0 {
			hasAny = true
		}
	}

	if val := os.Getenv("FN_SWITCHER_CYCLE"); val != "" {
		switch strings.ToLower(val) {
		case "true", "1":
			b := true
			cfg.Cycle = &b
			hasAny = true
		case "false", "0":
			b := false
			cfg.Cycle = &b
			hasAny = true
		}
	}

	if val := os.Getenv("FN_SWITCHER_SHORTCUT"); val != "" {
		cfg.Shortcut = strings.ToLower(strings.TrimSpace(val))
		hasAny = true
	}

	if !hasAny {
		return nil
	}
	return cfg
}

func normalizeLayouts(raw []string) []string {
	var result []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, keylayoutPrefix) {
			p = keylayoutPrefix + p
		}
		result = append(result, p)
	}
	return result
}

func findIndex(list []string, val string) int {
	for i, v := range list {
		if v == val {
			return i
		}
	}
	return -1
}

func switchInputSource(longPress bool) {
	current := getCurrentLayout()

	var target string
	if cycleMode {
		idx := findIndex(layouts, current)
		if idx == -1 {
			target = layouts[0]
		} else {
			target = layouts[(idx+1)%len(layouts)]
		}
	} else if longPress {
		// MRU long press: cycle to next, skipping previousLayout
		idx := findIndex(layouts, current)
		if idx == -1 {
			target = layouts[0]
		} else {
			next := (idx + 1) % len(layouts)
			if layouts[next] == previousLayout && len(layouts) > 2 {
				next = (next + 1) % len(layouts)
			}
			target = layouts[next]
		}
		previousLayout = current
	} else {
		if previousLayout == "" || previousLayout == current {
			idx := findIndex(layouts, current)
			if idx == -1 {
				target = layouts[0]
			} else {
				target = layouts[(idx+1)%len(layouts)]
			}
		} else {
			target = previousLayout
		}
		previousLayout = current
	}

	if err := setLayout(target); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] Error: %v\n", timestamp(), err)
	}
}

func printUsage() {
	fmt.Printf(`fn-switcher v%s - Fast Fn key input source switcher for macOS

Usage:
  fn-switcher [flags]           Start the switcher daemon
  fn-switcher -list             List available keyboard layouts
  fn-switcher -get              Show current input source
  fn-switcher -set <source_id>  Set input source

Flags:
  -layouts <list>   Comma-separated list of layouts to switch between
                    (default: auto-detect all com.apple.keylayout.* sources)
                    Use short names without the com.apple.keylayout. prefix.
  -cycle            Use cycle mode instead of MRU (most recently used)
  -shortcut <key>   Additional shortcut trigger (e.g., "shift+option")
                    Fn always works; this adds an extra trigger.
  -list             List all available keyboard layouts
  -get              Get current input source
  -set <source_id>  Set input source
  -version          Show version
  -help             Show this help

Configuration:
  fn-switcher uses layered configuration (highest priority first):
    1. CLI flags        -layouts "ABC,Russian" -cycle
    2. Environment      FN_SWITCHER_LAYOUTS=ABC,Russian FN_SWITCHER_CYCLE=true
    3. Config file      ~/.config/fn-switcher/config.json
    4. Defaults         Auto-detect layouts, MRU mode

  Config file example (~/.config/fn-switcher/config.json):
    {"layouts": ["ABC", "Russian"], "cycle": true, "shortcut": "shift+option"}

  Environment variables:
    FN_SWITCHER_LAYOUTS   Comma-separated layout names (short names)
    FN_SWITCHER_CYCLE     true/1 or false/0
    FN_SWITCHER_SHORTCUT  Additional shortcut (e.g., "shift+option")

Modes:
  MRU (default)     Short press (<500ms): toggle between current and previous layout.
                    Long press (>=500ms): cycle to next layout (skips previous).
  Cycle (-cycle)    Cycle through layouts in order on each press (instant).

Examples:
  fn-switcher                                        # Auto-detect layouts, MRU mode
  fn-switcher -cycle                                 # Auto-detect layouts, cycle mode
  fn-switcher -layouts "ABC,Russian"                 # Specific layouts, MRU mode
  fn-switcher -cycle -layouts "ABC,Russian,Australian"  # Specific layouts, cycle mode
  fn-switcher -shortcut "shift+option"               # Enable Shift+Option as extra trigger
  fn-switcher -list
  fn-switcher -get
  fn-switcher -set com.apple.keylayout.Russian

Note: Requires Accessibility permissions in System Settings.
`, version)
}

func printVersion() {
	fmt.Printf("fn-switcher v%s\n", version)
	if commit != "" {
		fmt.Printf("commit: %s\n", commit)
	}
	if buildDate != "" {
		fmt.Printf("built:  %s\n", buildDate)
	}
}

func main() {
	layoutsFlag := flag.String("layouts", "", "Comma-separated list of layouts (short names without com.apple.keylayout. prefix)")
	cycle := flag.Bool("cycle", false, "Use cycle mode instead of MRU")
	shortcut := flag.String("shortcut", "", "Additional shortcut trigger (e.g., \"shift+option\")")
	list := flag.Bool("list", false, "List available keyboard layouts")
	get := flag.Bool("get", false, "Get current input source")
	set := flag.String("set", "", "Set input source")
	showVersion := flag.Bool("version", false, "Show version")
	help := flag.Bool("help", false, "Show help")

	flag.Parse()

	if *help {
		printUsage()
		return
	}

	if *showVersion {
		printVersion()
		return
	}

	if *list {
		listLayouts()
		return
	}

	if *get {
		fmt.Println(getCurrentLayout())
		return
	}

	if *set != "" {
		if err := setLayout(*set); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return
	}

	// Detect which CLI flags were explicitly passed
	cliFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { cliFlags[f.Name] = true })

	// Load config layers: config file, then env vars
	fileCfg, err := loadConfigFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	envCfg := loadEnvVars()

	configPath := "" // track for startup banner
	if fileCfg != nil {
		configPath = configFilePath()
	}

	// Resolve cycle mode: CLI > env > config > default
	if cliFlags["cycle"] {
		cycleMode = *cycle
	} else if envCfg != nil && envCfg.Cycle != nil {
		cycleMode = *envCfg.Cycle
	} else if fileCfg != nil && fileCfg.Cycle != nil {
		cycleMode = *fileCfg.Cycle
	}

	// Resolve shortcut: CLI > env > config > default (disabled)
	var shortcutName string
	if cliFlags["shortcut"] {
		shortcutName = strings.ToLower(strings.TrimSpace(*shortcut))
	} else if envCfg != nil && envCfg.Shortcut != "" {
		shortcutName = envCfg.Shortcut
	} else if fileCfg != nil && fileCfg.Shortcut != "" {
		shortcutName = strings.ToLower(strings.TrimSpace(fileCfg.Shortcut))
	}
	if shortcutName == "shift+option" {
		shortcutEnabled = true
	} else if shortcutName != "" {
		fmt.Fprintf(os.Stderr, "Warning: unknown shortcut %q (supported: \"shift+option\")\n", shortcutName)
	}

	// Resolve layouts: CLI > env > config > auto-detect
	if cliFlags["layouts"] {
		layouts = normalizeLayouts(strings.Split(*layoutsFlag, ","))
	} else if envCfg != nil && len(envCfg.Layouts) > 0 {
		layouts = normalizeLayouts(envCfg.Layouts)
	} else if fileCfg != nil && len(fileCfg.Layouts) > 0 {
		layouts = normalizeLayouts(fileCfg.Layouts)
	} else {
		layouts = getKeyboardLayouts()
	}

	// Validate configured layouts against available system layouts
	available := getKeyboardLayouts()
	availableSet := make(map[string]bool, len(available))
	for _, l := range available {
		availableSet[l] = true
	}
	var validLayouts []string
	for _, l := range layouts {
		if availableSet[l] {
			validLayouts = append(validLayouts, l)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: layout %q is not available on this system, skipping\n", l)
		}
	}
	layouts = validLayouts

	if len(layouts) < 2 {
		fmt.Fprintln(os.Stderr, "Error: need at least 2 keyboard layouts to switch between.")
		fmt.Fprintln(os.Stderr, "Available layouts:")
		for _, l := range available {
			fmt.Fprintf(os.Stderr, "  %s\n", l)
		}
		os.Exit(1)
	}

	if fileCfg == nil {
		if err := createDefaultConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not create config: %v\n", err)
		}
	}

	previousLayout = layouts[1]

	mode := "MRU"
	if cycleMode {
		mode = "Cycle"
	}

	fmt.Printf("fn-switcher v%s", version)
	if commit != "" {
		fmt.Printf(" (%s)", commit)
	}
	if buildDate != "" {
		fmt.Printf(" built %s", buildDate)
	}
	fmt.Printf(" started at %s\n", timestamp())
	if configPath != "" {
		fmt.Printf("Config: %s\n", configPath)
	}
	fmt.Printf("Mode: %s\n", mode)
	if shortcutEnabled {
		fmt.Println("Shortcut: Shift+Option")
	} else {
		fmt.Println("Shortcut: Fn only")
	}
	fmt.Printf("Layouts: %s\n", strings.Join(layouts, " -> "))
	fmt.Printf("Current: %s\n", getCurrentLayout())
	if shortcutEnabled {
		fmt.Println("Press Fn or Shift+Option to switch. Ctrl+C to exit.")
	} else {
		fmt.Println("Press Fn to switch. Ctrl+C to exit.")
	}

	go switchWorker()
	C.startEventTap()
}
