// Package wm provides an X11 window manager.
package wm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/mattn/go-shellwords"
)

// for moving and resizing, basically the window that will be moved/resized
var start xproto.ButtonPressEvent
var attr *xproto.GetGeometryReply

// Mod key mask
var mMask uint16

// XUtil represents the state of xgbutil.
var XUtil *xgbutil.XUtil

// Colormap represents the default colormap of the screen
var Colormap xproto.Colormap

// MonitorConfig is the position of monitors defined in the user config
type MonitorConfig struct {
	X int `yaml:"x"`
	Y int `yaml:"y"`
}

// Keybind represents a keybind: keycode, the letter of the key, if shift should be pressed,
// command (can be empty), role in wm (can be empty).
type Keybind struct {
	Keycode uint32
	Key     string `yaml:"key"`
	Shift   bool   `yaml:"shift"`
	Exec    string `yaml:"exec"`
	Role    string `yaml:"role"`
}

// LayoutWindow represents where a window is on a layout (dynamic by using percentages).
type LayoutWindow struct {
	WidthPercentage  float64 `yaml:"width"`
	HeightPercentage float64 `yaml:"height"`
	XPercentage      float64 `yaml:"x"`
	YPercentage      float64 `yaml:"y"`
}

// Layout represents a tiling layout of windows.
type Layout struct {
	Windows []LayoutWindow `yaml:"windows"`
}

// RLayoutWindow represents a resized layout window.
type RLayoutWindow struct {
	Width, Height, X, Y uint16
}

// ResizeLayout represents a resized layout.
type ResizeLayout struct {
	Windows []RLayoutWindow
}

// Window represents a basic window struct.
type Window struct {
	id            xproto.Window
	X, Y          int
	Width, Height int
	Fullscreen    bool
	Client        xproto.Window
}

// Space represents an area on the screen.
type Space struct {
	X, Y          int
	Width, Height int
}

// Workspace is a map from client windows to the frame, the reverse of that, window IDs to windows, and if that
// workspace is tiling or not (in case it needs to update to sync with the main wm).
type Workspace struct {
	tiling        bool
	layoutIndex   int
	detachTiling  bool
	windowList    []*Window
	resized       bool
	resizedLayout ResizeLayout
}

// Monitor is representing a monitor which effectively houses its own workspaces and windows etc. the monitor is
// actually just a space in the root window
type Monitor struct {
	X                  int16
	Y                  int16
	Width              uint16
	Height             uint16
	workspaceIndex     int
	lastWorkspaceIndex int
	Workspaces         []Workspace
	CurrWorkspace      *Workspace
	TilingSpace        Space
	layoutIndex        int
	tiling             bool
	crtc               randr.Crtc
}

// WindowManager represents the connection, root window, width and height of screen, workspaces,
// the current workspace index,the current workspace, atoms for EMWH, if the wm is tiling, the space for tiling
// windows to be, the different tiling layouts, the wm config, the mod key.
type WindowManager struct {
	conferror     bool
	configWatcher *fsnotify.Watcher
	screen        *xproto.ScreenInfo
	conn          *xgb.Conn
	root          xproto.Window
	atoms         map[string]xproto.Atom
	monitors      []Monitor
	currMonitor   *Monitor
	config        Config
	mod           uint16
	windows       map[xproto.Window]*Window
	crtcToMonitor map[randr.Crtc]*Monitor
}

func (wm *WindowManager) cursor() { //nolint:unused
	// Load the default cursor ("left_ptr") from the theme
	cursorFont, err := xproto.NewFontId(wm.conn)
	if err != nil {
		slog.Error("Failed to allocate font ID:", "error:", err)
		return
	}

	cursorID, _ := xproto.NewCursorId(wm.conn)

	// Open the cursor font
	err = xproto.OpenFontChecked(wm.conn, cursorFont, uint16(len("cursor")), "cursor").Check()
	if err != nil {
		slog.Error("Failed to open cursor font:", "error:", err)
		return
	}

	// Create a cursor from the font - 68 = "left_ptr" in the standard cursor font
	// You can look up other cursor IDs from X11 cursor font tables if you want other styles
	err = xproto.CreateGlyphCursorChecked(
		wm.conn, cursorID, cursorFont, cursorFont,
		68, 69, // source and mask glyph (left_ptr)
		255, 255, 255, // foreground RGB
		0, 0, 0). // background RGB
		Check()
	if err != nil {
		slog.Error("Failed to create cursor: %v", "error:", err)
	}

	// Set the cursor on the root window
	err = xproto.ChangeWindowAttributesChecked(
		wm.conn, wm.root, xproto.CwCursor, []uint32{uint32(cursorID)}).Check()
	if err != nil {
		slog.Error("Failed to set cursor on root window: %v", "error:", err)
	}
}

// creates simple tiling layouts for 1-4 windows, any more is simply left on top to be moved
func createLayouts() map[int][]Layout {
	return map[int][]Layout{
		1: {{
			Windows: []LayoutWindow{
				{
					XPercentage:      0,
					YPercentage:      0,
					WidthPercentage:  1,
					HeightPercentage: 1,
				},
			},
		}},
		2: {{
			Windows: []LayoutWindow{
				{
					XPercentage:      0,
					YPercentage:      0,
					WidthPercentage:  0.5,
					HeightPercentage: 1,
				},
				{
					XPercentage:      0.5,
					YPercentage:      0,
					WidthPercentage:  0.5,
					HeightPercentage: 1,
				},
			},
		}},
		3: {{
			Windows: []LayoutWindow{
				{
					XPercentage:      0.0,
					YPercentage:      0,
					WidthPercentage:  1.0 / 3,
					HeightPercentage: 1,
				},
				{
					XPercentage:      1.0 / 3,
					YPercentage:      0,
					WidthPercentage:  1.0 / 3,
					HeightPercentage: 1,
				},
				{
					XPercentage:      2.0 / 3,
					YPercentage:      0,
					WidthPercentage:  1.0 / 3,
					HeightPercentage: 1,
				},
			},
		}},
		4: {{
			Windows: []LayoutWindow{
				{
					XPercentage:      0,
					YPercentage:      0,
					WidthPercentage:  0.5,
					HeightPercentage: 0.5,
				},
				{
					XPercentage:      0.5,
					YPercentage:      0,
					WidthPercentage:  0.5,
					HeightPercentage: 0.5,
				},
				{
					XPercentage:      0,
					YPercentage:      0.5,
					WidthPercentage:  0.5,
					HeightPercentage: 0.5,
				},
				{
					XPercentage:      0.5,
					YPercentage:      0.5,
					WidthPercentage:  0.5,
					HeightPercentage: 0.5,
				},
			},
		}},
	}
}

// Create creates the X connection and get the root window, create workspaces and create window manager struct.
func Create() (*WindowManager, error) {
	// establish connection
	X, err := xgb.NewConn()
	if err != nil {
		slog.Error("Couldn't open X display", "error", err)
		return nil, fmt.Errorf("could not open X display %w", err)
	}

	// Every extension must be initialized before it can be used.
	err = randr.Init(X)
	if err != nil {
		slog.Error("Couldn't init", "error:", err)
		return nil, fmt.Errorf("could not use randr for monitors %w", err)
	}

	// get xgbutil connection aswell for keybinds
	XUtil, err = xgbutil.NewConnXgb(X)
	if err != nil {
		return nil, fmt.Errorf("could not create xgbutil connection: %w", err)
	}

	keybind.Initialize(XUtil)

	// get root and dimensions of screen
	setup := xproto.Setup(X)
	screen := setup.DefaultScreen(X)
	Colormap = screen.DefaultColormap
	root := screen.Root

	// Gets the current screen resources. Screen resources contains a list
	// of names, crtcs, outputs and modes, among other things.
	resources, err := randr.GetScreenResources(X, root).Reply()
	if err != nil {
		slog.Error("Couldn't get resources", "error:", err)
		return nil, fmt.Errorf("could not get resources %w", err)
	}

	monitors := []Monitor{}
	crtcToMonitor := map[randr.Crtc]*Monitor{}

	for i, crtc := range resources.Crtcs {
		info, err := randr.GetCrtcInfo(X, crtc, 0).Reply()
		if err != nil {
			slog.Error("Couldn't get Crtc monitor info :(", "error:", err)
			break
		}

		// Skip disabled CRTCs
		if info.Width == 0 || info.Height == 0 {
			continue
		}

		monitors = append(monitors, Monitor{})
		monitors[i].X = info.X
		monitors[i].Y = info.Y
		monitors[i].Width = info.Width
		monitors[i].Height = info.Height
		monitors[i].crtc = crtc
		crtcToMonitor[crtc] = &monitors[i]
		fmt.Println("X", info.X, "Y", info.Y, "Width", info.Width, "Height", info.Height, "crtc", crtc)
	}

	/*dimensions, err := xproto.GetGeometry(X, xproto.Drawable(root)).Reply()
	if err != nil {
		return nil, fmt.Errorf("couldn't get screen dimensions: %w", err)
	}*/

	for i := range monitors {
		// create workspaces
		workspaces := make([]Workspace, 10)
		for i := range workspaces {
			workspaces[i] = Workspace{
				windowList:    []*Window{},
				tiling:        false,
				detachTiling:  false,
				layoutIndex:   0,
				resized:       false,
				resizedLayout: ResizeLayout{},
			}
		}
		monitors[i].Workspaces = workspaces
		monitors[i].workspaceIndex = 0
		monitors[i].lastWorkspaceIndex = 0
		monitors[i].CurrWorkspace = &workspaces[0]
		monitors[i].layoutIndex = 0
		monitors[i].tiling = false
	}

	// Tell RandR to send us events. (I think these are all of them, as of 1.3.)
	err = randr.SelectInputChecked(X, root,
		randr.NotifyMaskScreenChange|
			randr.NotifyMaskCrtcChange|
			randr.NotifyMaskOutputChange|
			randr.NotifyMaskOutputProperty).Check()
	if err != nil {
		slog.Error("Can't get notified from randr", "error:", err)
	}

	// return the window manager struct
	return &WindowManager{
		conferror:     false,
		screen:        screen,
		conn:          X,
		root:          root,
		monitors:      monitors,
		currMonitor:   &monitors[0],
		atoms:         map[string]xproto.Atom{},
		windows:       map[xproto.Window]*Window{},
		crtcToMonitor: crtcToMonitor,
	}, nil
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func getNumLockMask(conn *xgb.Conn) uint16 {
	numLockSym := uint32(0xff7f) // XK_Num_Lock
	numLockKeycode := getKeycodeForKeysym(conn, numLockSym)
	if numLockKeycode == 0 {
		slog.Info("Num Lock keycode not found")
		return 0
	}

	modMap, err := xproto.GetModifierMapping(conn).Reply()
	if err != nil {
		slog.Error("failed to get modifier mapping: %v", "error: ", err)
	}

	// Each modifier (Shift, Lock, Control, Mod1-Mod5) has modMap.KeycodesPerModifier keycodes
	for modIndex := range 8 {
		for i := range int(modMap.KeycodesPerModifier) {
			index := modIndex*int(modMap.KeycodesPerModifier) + i
			if modMap.Keycodes[index] == numLockKeycode {
				return 1 << uint(modIndex)
			}
		}
	}

	return 0
}

func getKeycodeForKeysym(conn *xgb.Conn, keysym uint32) xproto.Keycode {
	setup := xproto.Setup(conn)
	firstKeycode := setup.MinKeycode
	lastKeycode := setup.MaxKeycode

	// Number of keycodes in range:
	count := lastKeycode - firstKeycode + 1

	keymap, err := xproto.GetKeyboardMapping(conn, firstKeycode, uint8(count)).Reply()
	if err != nil {
		slog.Error("failed to get keyboard mapping", "error:", err)
		return 0
	}

	targetKeysym := xproto.Keysym(keysym)

	for kc := firstKeycode; kc <= lastKeycode; kc++ {
		offset := int(kc-firstKeycode) * int(keymap.KeysymsPerKeycode)
		for i := range int(keymap.KeysymsPerKeycode) {
			if keymap.Keysyms[offset+i] == targetKeysym {
				return kc
			}
		}
	}
	return 0
}
func getKeycodesForKeysym(conn *xgb.Conn, keysym uint32) []xproto.Keycode {
	setup := xproto.Setup(conn)
	first := int(setup.MinKeycode)
	last := int(setup.MaxKeycode)
	count := last - first + 1

	keymap, err := xproto.GetKeyboardMapping(conn, setup.MinKeycode, uint8(count)).Reply()
	if err != nil {
		slog.Error("failed to get keyboard mapping", "error", err)
		return nil
	}

	target := xproto.Keysym(keysym)
	kpk := int(keymap.KeysymsPerKeycode)
	var keycodes []xproto.Keycode

	for kc := first; kc <= last; kc++ {
		offset := (kc - first) * kpk
		for i := range kpk {
			if keymap.Keysyms[offset+i] == target {
				keycodes = append(keycodes, xproto.Keycode(kc))
				break
			}
		}
	}
	return keycodes
}

// Converts a keysym (layout-agnostic symbol like XK_b) to a keycode (layout-specific)
func keysymToKeycode(conn *xgb.Conn, target xproto.Keysym) (xproto.Keycode, error) {
	setup := xproto.Setup(conn)
	first := setup.MinKeycode
	last := setup.MaxKeycode
	count := last - first + 1

	reply, err := xproto.GetKeyboardMapping(conn, first, uint8(count)).Reply()
	if err != nil {
		return 0, fmt.Errorf("GetKeyboardMapping failed: %w", err)
	}

	for kc := first; kc <= last; kc++ {
		offset := int(kc-first) * int(reply.KeysymsPerKeycode)
		for i := 0; i < int(reply.KeysymsPerKeycode); i++ {
			if reply.Keysyms[offset+i] == target {
				return kc, nil
			}
		}
	}
	return 0, fmt.Errorf("no keycode for keysym 0x%X", target)
}

func GetKeysym(input string) (xproto.Keysym, bool) {
	// 1. Exact match (most important)
	if ks, ok := Keysyms[input]; ok {
		return ks, true
	}

	// 2. Try uppercase version
	upper := strings.ToUpper(input)
	if ks, ok := Keysyms[upper]; ok {
		return ks, true
	}

	// 3. Try lowercase version
	lower := strings.ToLower(input)
	if ks, ok := Keysyms[lower]; ok {
		return ks, true
	}

	return 0, false
}

// gets keycode of key and sets it, then tells the X server to notify us when this keybind is pressed.
func (wm *WindowManager) createKeybind(kb *Keybind) {
	keysym, ok := GetKeysym(kb.Key)
	if !ok {
		return
	}
	KeyCodes := getKeycodesForKeysym(wm.conn, uint32(keysym))
	for _, KeyCode := range KeyCodes {
		keybind := *kb
		keybind.Keycode = uint32(KeyCode)
		wm.grabKey(keybind, KeyCode)

		wm.config.Keybindings = append(wm.config.Keybindings, keybind)
	}
}

func (wm *WindowManager) grabKey(kb Keybind, KeyCode xproto.Keycode) {
	Mask := wm.mod
	if kb.Shift {
		Mask |= xproto.ModMaskShift
	}
	if len(kb.Key) >= 2 && kb.Key[0:2] == "XF" {
		Mask = 0
	}
	err := xproto.GrabKeyChecked(wm.conn, true, wm.root, Mask, KeyCode, xproto.GrabModeAsync, xproto.GrabModeAsync).
		Check()
	if err != nil {
		slog.Error("Couldn't grab key", "error:", err)
	}

	err = xproto.GrabKeyChecked(
		wm.conn,
		true,
		wm.root,
		Mask|xproto.ModMaskLock,
		KeyCode,
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
	).
		Check()
	if err != nil {
		slog.Error("Couldn't grab key", "error:", err)
	}
	numlock := getNumLockMask(wm.conn)
	if numlock != wm.mod {
		err = xproto.GrabKeyChecked(
			wm.conn,
			true,
			wm.root,
			Mask|numlock,
			KeyCode,
			xproto.GrabModeAsync,
			xproto.GrabModeAsync,
		).
			Check()
	}
	if err != nil {
		slog.Error("Couldn't create keybind", "error:", err)
	}
}

func (wm *WindowManager) reload(focused xproto.ButtonPressEvent) {
	if wm.config.AutoReload && wm.configWatcher == nil {
		wm.configListener()
	} else if !wm.config.AutoReload && wm.configWatcher != nil {
		wm.configWatcher.Close()
		wm.configWatcher = nil
	}
	// set the mod key for the wm
	var mMask uint16
	switch wm.config.ModKey {
	case "Mod1":
		mMask = xproto.ModMask1
	case "Mod2":
		mMask = xproto.ModMask2
	case "Mod3":
		mMask = xproto.ModMask3
	case "Mod4":
		mMask = xproto.ModMask4
	case "Mod5":
		mMask = xproto.ModMask5
	}

	wm.mod = mMask

	// manage keybinds for keybinds in the config
	for _, kb := range wm.config.TempKeybinds {
		wm.createKeybind(&kb)
	}
	wm.setKeyBinds()

	windowsParent, err := xproto.QueryTree(wm.conn, wm.root).Reply()
	if err != nil {
		return
	}
	windows := windowsParent.Children
	fmt.Println("windows:", windows)
	fmt.Println("new border width:", wm.config.BorderWidth)

	for _, window := range windows {
		if win, ok := wm.windows[window]; ok && !win.Fullscreen {
			col := wm.config.BorderUnactive
			if window == focused.Child {
				col = wm.config.BorderActive
			}

			// Set border width
			err := xproto.ConfigureWindowChecked(
				wm.conn,
				window,
				xproto.ConfigWindowBorderWidth,
				[]uint32{wm.config.BorderWidth},
			).
				Check()
			if err != nil {
				slog.Error("Couldn't set border width", "error", err)
			}

			// Set border color
			err = xproto.ChangeWindowAttributesChecked(wm.conn, window, xproto.CwBorderPixel, []uint32{col}).
				Check()
			if err != nil {
				slog.Error("Couldn't set border color", "error", err)
			}
		}
	}

	wm.fitToLayout()
}

func (wm *WindowManager) positionMonitors() {
	resources, err := randr.GetScreenResources(wm.conn, wm.root).Reply()
	if err != nil {
		slog.Error("Couldn't get resources", "error:", err)
		return
	}

	count := 0
	for i, crtc := range resources.Crtcs {
		info, err := randr.GetCrtcInfo(wm.conn, crtc, 0).Reply()
		if err != nil {
			continue
		}
		if info.Width == 0 || info.Height == 0 {
			continue
		}

		if len(wm.config.Monitors) == count {
			break
		}
		X, Y := wm.config.Monitors[i].X, wm.config.Monitors[i].Y

		if info.Width == 0 || info.Height == 0 {
			continue
		}
		randr.SetCrtcConfig(wm.conn, crtc, 0, 0, int16(X), int16(Y), info.Mode, info.Rotation, info.Outputs)
		wm.monitors[count].X = int16(X)
		wm.monitors[count].Y = int16(Y)
		count++
	}

	widths := 0
	heights := 0
	for _, mon := range wm.monitors {
		widths += int(mon.Width)
		heights += int(mon.Height)
	}

	widthsMM := 0
	heightsMM := 0
	for _, out := range resources.Outputs {
		info, _ := randr.GetOutputInfo(wm.conn, out, 0).Reply()
		widthsMM += int(info.MmWidth)
		heightsMM += int(info.MmHeight)
	}

	err = randr.SetScreenSizeChecked(
		wm.conn,
		wm.root,
		uint16(widths),
		uint16(heights),
		uint32(widthsMM),
		uint32(heightsMM),
	).Check()
	if err != nil {
		slog.Error("Couldnt set screen size", "error", err)
	}
}

func (wm *WindowManager) pointerToWindow(window xproto.Window) error {
	geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(window)).Reply()
	if err != nil {
		return err
	}

	trans, err := xproto.TranslateCoordinates(wm.conn, window, xproto.Setup(wm.conn).DefaultScreen(wm.conn).Root, 0, 0).
		Reply()
	if err != nil {
		return err
	}

	x := trans.DstX + int16(geom.Width)/2
	y := trans.DstY + int16(geom.Height)/2

	return xproto.WarpPointerChecked(wm.conn, 0, xproto.Setup(wm.conn).DefaultScreen(wm.conn).Root, 0, 0, 0, 0, x, y).
		Check()
}

// Run runs the window manager.
func (wm *WindowManager) Run() { //nolint:cyclop
	fmt.Println("window manager up and running")

	// basically asks the X server for WM access
	err := xproto.ChangeWindowAttributesChecked(
		wm.conn,
		wm.root,
		xproto.CwEventMask,
		[]uint32{
			xproto.EventMaskSubstructureNotify |
				xproto.EventMaskSubstructureRedirect,
		},
	).Check()
	if err != nil {
		if err.Error() == "BadAccess" {
			slog.Error("Other window manager running on display")
			return
		}
	}

	// wm.cursor()

	// retrieve config and set values
	cfg := wm.createConfig(false)
	wm.config = cfg
	if len(wm.config.Monitors) != 0 {
		wm.positionMonitors()
	}
	if wm.config.StartTiling {
		cm := wm.currMonitor
		for i := range wm.monitors {
			wm.currMonitor = &wm.monitors[i]
			wm.toggleTiling()
			wm.fitToLayout()
		}
		wm.currMonitor = cm
	}
	if wm.config.AutoReload {
		wm.configListener()
	}
	defer func() {
		if wm.configWatcher != nil {
			wm.configWatcher.Close()
		}
	}()

	// get autostart
	user, err := user.Current()
	if err == nil {
		scriptPath := filepath.Join(user.HomeDir, ".config", "doWM", "autostart.sh")

		if fileExists(scriptPath) {
			fmt.Println("autostart exists..., running")
			_ = exec.Command(scriptPath).Start()
		}
	}
	// for things like polybar, to show workspaces
	wm.broadcastWorkspace(0)
	wm.broadcastWorkspaceCount()

	// grab the server whilst we manage pre-existing windows
	err = xproto.GrabServerChecked(
		wm.conn,
	).Check()
	if err != nil {
		slog.Error("Couldn't grab X server", "error:", err)
		return
	}

	// if there are any pre-existing windows, we need to manage them
	tree, err := xproto.QueryTree(
		wm.conn,
		wm.root,
	).Reply()
	if err != nil {
		slog.Error("Couldn't query tree", "error:", err)
		return
	}

	root, TopLevelWindows := tree.Root, tree.Children

	if root != wm.root {
		slog.Error("Tree root not equal to window manager root")
		return
	}

	for _, window := range TopLevelWindows {
		if !shouldIgnoreWindow(wm.conn, window) {
			wm.frame(window, true)
		}
	}

	err = xproto.UngrabServerChecked(wm.conn).Check()
	if err != nil {
		slog.Error("Couldn't ungrab server", "error:", err.Error())
		return
	}

	// set the mod key for the wm
	switch wm.config.ModKey {
	case "Mod1":
		mMask = xproto.ModMask1
	case "Mod2":
		mMask = xproto.ModMask2
	case "Mod3":
		mMask = xproto.ModMask3
	case "Mod4":
		mMask = xproto.ModMask4
	case "Mod5":
		mMask = xproto.ModMask5
	}

	wm.mod = mMask

	// manage keybinds for keybinds in the config
	for _, kb := range wm.config.TempKeybinds {
		wm.createKeybind(&kb)
	}

	wm.setKeyBinds()
	fmt.Println(wm.config.Keybindings)

	// Only grab with Mod + left or right click (not plain Button1)
	err = xproto.GrabButtonChecked(
		wm.conn,
		false,
		wm.root,
		uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskPointerMotion),
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
		xproto.WindowNone,
		xproto.AtomNone,
		xproto.ButtonIndex1,
		mMask,
	).
		Check()
	if err != nil {
		slog.Error("Couldn't grab button", "error:", err.Error())
	}

	err = xproto.GrabButtonChecked(
		wm.conn,
		false,
		wm.root,
		uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskPointerMotion),
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
		xproto.WindowNone,
		xproto.AtomNone,
		xproto.ButtonIndex3,
		mMask,
	).
		Check()
	if err != nil {
		slog.Error("Couldn't grab window+c key", "error:", err.Error())
	}

	// create EMWH atoms
	atoms := []string{
		"_NET_WM_STATE",
		"_NET_WM_STATE_FULLSCREEN",
		"_NET_WM_STATE_ABOVE",
		"_NET_WM_STATE_BELOW",
		"_NET_WM_STATE_MAXIMIZED_HORZ",
		"_NET_WM_STATE_MAXIMIZED_VERT",
		"_NET_WM_WINDOW_TYPE",
		"_NET_WM_WINDOW_TYPE_DOCK",
		"_NET_WM_STRUT_PARTIAL",
		"_NET_WORKAREA",
		"_NET_CURRENT_DESKTOP",
	}

	for _, name := range atoms {
		a, _ := xproto.InternAtom(wm.conn, false, uint16(len(name)), name).Reply()
		fmt.Printf("%s = %d\n", name, a.Atom)
		wm.atoms[name] = a.Atom
	}
	wm.declareSupportedAtoms()

	for {
		// get next event
		event, err := wm.conn.WaitForEvent()
		if err != nil {
			slog.Error("Event error", "error:", err.Error())
			continue
		}
		if event == nil {
			return
		}

		pointer, ptrerr := xproto.QueryPointer(wm.conn, wm.root).Reply()
		if ptrerr == nil {
			for i, mon := range wm.monitors {
				if pointer.RootX >= mon.X && pointer.RootX <= mon.X+int16(mon.Width) && pointer.RootY >= mon.Y &&
					pointer.RootY <= mon.Y+int16(mon.Height) {
					wm.currMonitor = &wm.monitors[i]
					wm.setNetClientList()
					wm.setNetWorkArea()
				}
			}
		}

		if len(wm.currMonitor.CurrWorkspace.windowList) == 0 {
			err := xproto.SetInputFocusChecked(wm.conn, xproto.InputFocusPointerRoot, wm.root, xproto.TimeCurrentTime).
				Check()
			if err != nil {
				slog.Error("Couldn't set input focus", "error", err)
			}
		}
		switch ev := event.(type) {
		case randr.NotifyEvent:
			fmt.Println("RANDR NOTIFY", ev)

		case xproto.ButtonPressEvent:
			// set values on current window, used later with moving and resizing
			if ev.Child != 0 && ev.State&mMask != 0 {
				attr, _ = xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
				start = ev
				if ev.Detail == xproto.ButtonIndex1 {
					xproto.ConfigureWindow(
						wm.conn,
						ev.Child,
						xproto.ConfigWindowStackMode,
						[]uint32{xproto.StackModeAbove},
					)
				}
			} else if ev.State&mMask == 0 {
				xproto.AllowEvents(wm.conn, xproto.AllowReplayPointer, xproto.TimeCurrentTime)
			}
		case xproto.ButtonReleaseEvent:
			ev, ok := event.(xproto.ButtonReleaseEvent)
			if !ok {
				break
			}

			// if we don't have the mouse down, we don't want to move or resize

			var startmon *Monitor
			var endmon *Monitor
			for _, mon := range wm.monitors {
				if start.RootX >= mon.X && start.RootX <= mon.X+int16(mon.Width) && start.RootY >= mon.Y && start.RootY <= mon.Y+int16(mon.Height) {
					startmon = &mon
				}
				if ev.RootX >= mon.X && ev.RootX <= mon.X+int16(mon.Width) && ev.RootY >= mon.Y && ev.RootY <= mon.Y+int16(mon.Height) {
					endmon = &mon
				}
			}

			if endmon != nil && startmon != endmon {
				orx := wm.windows[ev.Child].X - int(startmon.X)
				ory := wm.windows[ev.Child].Y - int(startmon.Y)
				wm.currMonitor.CurrWorkspace.windowList = append(wm.currMonitor.CurrWorkspace.windowList, &Window{
					id:     ev.Child,
					X:      int(endmon.X) + orx,
					Y:      int(endmon.Y) + ory,
					Width:  wm.windows[ev.Child].Width,
					Height: wm.windows[ev.Child].Height,
					Client: ev.Child,
				})
				remove(&startmon.CurrWorkspace.windowList, ev.Child)
				wm.currMonitor = startmon
				wm.fitToLayout()
				wm.currMonitor = endmon
				wm.fitToLayout()
			}
			if wm.currMonitor.tiling {
				found := false
				for _, window := range wm.currMonitor.CurrWorkspace.windowList {
					geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(window.id)).Reply()
					if err != nil {
						continue
					}
					if window.id != ev.Child &&
						ev.EventX < geom.X+int16(geom.Width) &&
						ev.EventX > geom.X &&
						ev.EventY < geom.Y+int16(geom.Height) &&
						ev.EventY > geom.Y {
						fmt.Println("MOVING", ev.Child, window.id)
						swapWindowsID(&wm.currMonitor.CurrWorkspace.windowList, ev.Child, window.id)
						wm.fitToLayout()
						found = true
						break
					}
				}
				if !found {
					wm.fitToLayout()
				}
			}
			start.Child = 0
			xproto.AllowEvents(wm.conn, xproto.AllowReplayPointer, xproto.TimeCurrentTime)
		case xproto.MotionNotifyEvent:
			// if we have the mouse down and we are holding the mod key, and if we are not tiling and the window is not
			// full screen then do some simple maths to move and resize

			fmt.Println("X:", ev.RootX, "Y:", ev.RootY)
			for _, mon := range wm.monitors {
				if ev.RootX >= mon.X && ev.RootX <= mon.X+int16(mon.Width) && ev.RootY >= mon.Y && ev.RootY <= mon.Y+int16(mon.Height) {
					wm.currMonitor = &mon
				}
			}

			focusWindow(wm.conn, ev.Child)
			if start.Child != 0 && ev.State&mMask != 0 {
				if wm.windows[start.Child] != nil && wm.windows[start.Child].Fullscreen {
					break
				}
				xdiff := ev.RootX - start.RootX
				ydiff := ev.RootY - start.RootY
				Xoffset := attr.X + xdiff
				Yoffset := attr.Y + ydiff
				sizeY := attr.Height
				sizeX := attr.Width
				fmt.Println("start detail")
				fmt.Println(start.Detail)
				if start.Detail == xproto.ButtonIndex3 {
					if wm.currMonitor.CurrWorkspace.tiling {
						break
					}
					Xoffset = attr.X
					Yoffset = attr.Y
					sizeX = uint16(max(10, int(int16(attr.Width)+xdiff)))
					sizeY = uint16(max(10, int(int16(attr.Height)+ydiff)))
				}

				xproto.ConfigureWindow(
					wm.conn,
					start.Child,
					xproto.ConfigWindowX|xproto.ConfigWindowY|
						xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
					[]uint32{uint32(Xoffset), uint32(Yoffset), uint32(sizeX), uint32(sizeY)},
				)
			}
		case xproto.CreateNotifyEvent:
			fmt.Println("create notify")
		case xproto.ConfigureRequestEvent:
			wm.onConfigureRequest(ev)
		case xproto.MapRequestEvent:
			fmt.Println("MapRequest")
			wm.onMapRequest(ev)
		case xproto.ReparentNotifyEvent:
			fmt.Println("reparent notify")
		case xproto.MapNotifyEvent:
			fmt.Println("MapNotify")
		case xproto.ConfigureNotifyEvent:
			fmt.Println("ConfigureNotify")
		case xproto.UnmapNotifyEvent:
			fmt.Println("unmapping")
			wm.onUnmapNotify(ev)
		case xproto.DestroyNotifyEvent:
			fmt.Println("DestroyNotify")
			fmt.Println("Window:")
			fmt.Println(ev.Window)
			fmt.Println("Event:")
			fmt.Println(ev.Event)
			// if the destroy notify has come through but we haven't registered any kind of deletion then handle it
			if _, ok := wm.windows[ev.Window]; ok {
				wm.remDestroyedWin(ev.Window)
			}
			fmt.Println("finished destroying")
		case xproto.EnterNotifyEvent:
			// when we enter the frame, change the border color
			fmt.Println("EnterNotify")
			fmt.Println(ev.Event)
			wm.onEnterNotify(ev)
		case xproto.LeaveNotifyEvent:
			// when we leave the frame, change the border color
			fmt.Println("LeaveNotify")
			fmt.Println(ev.Event)
			wm.onLeaveNotify(ev)
		case xproto.KeyPressEvent:
			fmt.Println("keyPress")
			// if mod key is down
			if true {
				// go through keybinds if the keybind matches up to the current event then continue
				for _, kb := range wm.config.Keybindings {
					if ev.Detail == xproto.Keycode(kb.Keycode) {
						fmt.Println(int(ev.Detail), kb.Key)
					}
					if ev.Detail == xproto.Keycode(kb.Keycode) && ((ev.State&(mMask|xproto.ModMaskShift) ==
						(mMask | xproto.ModMaskShift) == kb.Shift) || (len(kb.Key) >= 2 && kb.Key[0:2] == "XF")) {
						// if it has an exec then just execute it
						if kb.Exec != "" {
							fmt.Println("executing:", kb.Exec)
							runCommand(kb.Exec)
							fmt.Println("excuted")
						}
						switch kb.Role {
						case "resize-x-scale-up":
							if wm.currMonitor.CurrWorkspace.tiling {
								if err := wm.pointerToWindow(ev.Child); err != nil {
									slog.Error("Couldn't move pointer to window", "error:", err)
								}
								if !wm.resizeTiledX(true, ev) {
									break
								}
							} else {
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil {
									break
								}
								xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowWidth,
									[]uint32{uint32(geom.Width + uint16(wm.config.Resize))})
								if err := wm.pointerToWindow(ev.Child); err != nil {
									slog.Error("Couldn't move pointer to window", "error:", err)
								}
							}
						case "resize-x-scale-down":
							if wm.currMonitor.CurrWorkspace.tiling {
								if err := wm.pointerToWindow(ev.Child); err != nil {
									slog.Error("Couldn't move pointer to window", "error:", err)
								}
								if !wm.resizeTiledX(false, ev) {
									break
								}
							} else {
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil {
									break
								}
								if geom.Width > 10 {
									xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowWidth,
										[]uint32{uint32(geom.Width - uint16(wm.config.Resize))})
									if err := wm.pointerToWindow(ev.Child); err != nil {
										slog.Error("Couldn't move pointer to window", "error:", err)
									}
								}
							}
						case "resize-y-scale-up":
							if wm.currMonitor.CurrWorkspace.tiling {
								if err := wm.pointerToWindow(ev.Child); err != nil {
									slog.Error("Couldn't move pointer to window", "error:", err)
								}
								if !wm.resizeTiledY(true, ev) {
									break
								}
							} else {
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil {
									break
								}
								xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowHeight,
									[]uint32{uint32(geom.Height + uint16(wm.config.Resize))})
								if err := wm.pointerToWindow(ev.Child); err != nil {
									slog.Error("Couldn't move pointer to window", "error:", err)
								}
							}
						case "resize-y-scale-down":
							if wm.currMonitor.CurrWorkspace.tiling {
								if err := wm.pointerToWindow(ev.Child); err != nil {
									slog.Error("Couldn't move pointer to window", "error:", err)
								}
								if !wm.resizeTiledY(false, ev) {
									break
								}
							} else {
								if wm.currMonitor.CurrWorkspace.tiling {
									break
								}
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil {
									break
								}
								if geom.Height > 10 {
									xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowHeight,
										[]uint32{uint32(geom.Height - uint16(wm.config.Resize))})
									if err := wm.pointerToWindow(ev.Child); err != nil {
										slog.Error("Couldn't move pointer to window", "error:", err)
									}
								}
							}
						case "move-x-right":
							if wm.currMonitor.CurrWorkspace.tiling {
								break
							}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil {
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowX, []uint32{uint32(geom.X + 10)})
							if err := wm.pointerToWindow(ev.Child); err != nil {
								slog.Error("Couldn't move pointer to window", "error:", err)
							}
						case "move-x-left":
							if wm.currMonitor.CurrWorkspace.tiling {
								break
							}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil {
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowX, []uint32{uint32(geom.X - 10)})
							if err := wm.pointerToWindow(ev.Child); err != nil {
								slog.Error("Couldn't move pointer to window", "error:", err)
							}
						case "move-y-up":
							if wm.currMonitor.CurrWorkspace.tiling {
								break
							}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil {
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowY, []uint32{uint32(geom.Y - 10)})
							if err := wm.pointerToWindow(ev.Child); err != nil {
								slog.Error("Couldn't move pointer to window", "error:", err)
							}
						case "move-y-down":
							if wm.currMonitor.CurrWorkspace.tiling {
								break
							}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil {
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowY, []uint32{uint32(geom.Y + 10)})
							if err := wm.pointerToWindow(ev.Child); err != nil {
								slog.Error("Couldn't move pointer to window", "error:", err)
							}
						case "quit":
							if _, ok := wm.windows[ev.Child]; ok {
								// EMWH way of politely saying to destroy
								if err := wm.sendWmDelete(wm.conn, wm.windows[ev.Child].id); err != nil {
									slog.Error("send WmDelete", "error", err)
								}
								fmt.Println("closing window:", wm.windows[ev.Child].id, "frame:", ev.Child)
							}
						case "force-quit":
							// force close
							if wm.windows[ev.Child] == nil {
								break
							}
							err := xproto.DestroyWindowChecked(wm.conn, wm.windows[ev.Child].id).Check()
							if err != nil {
								fmt.Println("Couldn't force destroy:", err)
							}
						case "toggle-tiling":
							wm.toggleTiling()
						case "detach-tiling":
							if wm.currMonitor.CurrWorkspace.detachTiling {
								wm.currMonitor.CurrWorkspace.detachTiling = false
								if wm.currMonitor.tiling && !wm.currMonitor.CurrWorkspace.tiling {
									wm.enableTiling()
								} else if !wm.currMonitor.tiling && wm.currMonitor.CurrWorkspace.tiling {
									wm.disableTiling()
								}
							} else {
								wm.currMonitor.CurrWorkspace.detachTiling = true
							}
							wm.fitToLayout()
						case "toggle-fullscreen":
							wm.toggleFullScreen(ev.Child)
						case "swap-window-left":
							fmt.Println("swap left")
							if wm.currMonitor.CurrWorkspace.tiling {
								currWindow := ev.Child
							swapLeft:
								for i := range wm.currMonitor.CurrWorkspace.windowList {
									if currWindow == wm.currMonitor.CurrWorkspace.windowList[i].id {
										if i == 0 {
											swapWindows(&wm.currMonitor.CurrWorkspace.windowList, i, len(wm.currMonitor.CurrWorkspace.windowList)-1)
										} else {
											swapWindows(&wm.currMonitor.CurrWorkspace.windowList, i, i-1)
										}
										wm.fitToLayout()
										if err := wm.pointerToWindow(currWindow); err != nil {
											slog.Error("Couldn't move pointer to window", "error:", err)
										}
										break swapLeft
									}
								}
							}
						case "swap-window-right":
							fmt.Println("swap right")
							if wm.currMonitor.CurrWorkspace.tiling {
								currWindow := ev.Child
							swapRight:
								for i := range wm.currMonitor.CurrWorkspace.windowList {
									if currWindow == wm.currMonitor.CurrWorkspace.windowList[i].id {
										if i == len(wm.currMonitor.CurrWorkspace.windowList)-1 {
											swapWindows(&wm.currMonitor.CurrWorkspace.windowList, i, 0)
										} else {
											swapWindows(&wm.currMonitor.CurrWorkspace.windowList, i, i+1)
										}
										wm.fitToLayout()
										if err := wm.pointerToWindow(currWindow); err != nil {
											slog.Error("Couldn't move pointer to window", "error:", err)
										}
										break swapRight
									}
								}
							}
						case "focus-window-right":
							if wm.currMonitor.CurrWorkspace.tiling {
								currWindow := ev.Child
							focusRight:
								for i := range wm.currMonitor.CurrWorkspace.windowList {
									if currWindow == wm.currMonitor.CurrWorkspace.windowList[i].id {
										if i == len(wm.currMonitor.CurrWorkspace.windowList)-1 {
											if err := wm.pointerToWindow(wm.currMonitor.CurrWorkspace.windowList[0].id); err != nil {
												slog.Error("Couldn't move pointer to window", "error:", err)
											}
										} else {
											if err := wm.pointerToWindow(wm.currMonitor.CurrWorkspace.windowList[i+1].id); err != nil {
												slog.Error("Couldn't move pointer to window", "error:", err)
											}
										}
										break focusRight
									}
								}
							}
						case "focus-window-left":
							if wm.currMonitor.CurrWorkspace.tiling {
								currWindow := ev.Child
							focusLeft:
								for i := range wm.currMonitor.CurrWorkspace.windowList {
									if currWindow == wm.currMonitor.CurrWorkspace.windowList[i].id {
										if i == 0 {
											err := wm.pointerToWindow(wm.currMonitor.CurrWorkspace.windowList[len(wm.currMonitor.CurrWorkspace.windowList)-1].id)
											if err != nil {
												slog.Error("Couldn't move pointer to window", "error:", err)
											}
										} else {
											if err := wm.pointerToWindow(wm.currMonitor.CurrWorkspace.windowList[i-1].id); err != nil {
												slog.Error("Couldn't move pointer to window", "error:", err)
											}
										}
										break focusLeft
									}
								}
							}
						case "focus-workspace-left":
							wm.switchWorkspace(wm.currMonitor.workspaceIndex - 1)
						case "focus-workspace-right":
							wm.switchWorkspace(wm.currMonitor.workspaceIndex + 1)
						case "reload-config":
							cfg := wm.createConfig(false)
							wm.config = cfg
							if len(wm.config.Monitors) != 0 {
								wm.positionMonitors()
							}
							wm.reload(start)
							mMask = wm.mod

						case "next-layout":
							windowNum := len(wm.currMonitor.CurrWorkspace.windowList)
							if windowNum < 1 {
								break
							}
							totalLen := len(wm.config.lyts[windowNum]) - 1
							if wm.currMonitor.CurrWorkspace.layoutIndex == totalLen {
								wm.currMonitor.CurrWorkspace.layoutIndex = 0
							} else {
								wm.currMonitor.CurrWorkspace.layoutIndex++
							}
							wm.currMonitor.layoutIndex = wm.currMonitor.CurrWorkspace.layoutIndex
							wm.currMonitor.CurrWorkspace.resized = false
							wm.currMonitor.CurrWorkspace.resizedLayout = ResizeLayout{}
							wm.fitToLayout()
						case "increase-gap":
							wm.config.Gap++
							wm.fitToLayout()
						case "decrease-gap":
							if wm.config.Gap > 0 {
								wm.config.Gap--
							}
							wm.fitToLayout()
						case "quit-wm":
							return
						}
						switch kb.Key {
						case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
							// if shift is pressed we want to move the window to the next workspace, so delete it from
							// the record of the current workspace so when they unmap all the other windows (giving the
							// illusion of changing workspace) this one stays then afterwards reparent it to the
							// workspace that has been changed to
							w := ev.Child
							var window Window
							shiftok := false
							if kb.Shift {
								if _, ok := wm.windows[w]; ok {
									shiftok = ok
									window = *wm.windows[w]
									fmt.Println("moving window")
									xproto.ConfigureWindow(
										wm.conn,
										w,
										xproto.ConfigWindowStackMode,
										[]uint32{xproto.StackModeAbove},
									)
									remove(&wm.currMonitor.CurrWorkspace.windowList, w)
								}
							}
							switch kb.Key {
							case "1":
								wm.switchWorkspace(0)
							case "2":
								wm.switchWorkspace(1)
							case "3":
								wm.switchWorkspace(2)
							case "4":
								wm.switchWorkspace(3)
							case "5":
								wm.switchWorkspace(4)
							case "6":
								wm.switchWorkspace(5)
							case "7":
								wm.switchWorkspace(6)
							case "8":
								wm.switchWorkspace(7)
							case "9":
								wm.switchWorkspace(8)
							case "0":
								wm.switchWorkspace(9)
							}
							if kb.Shift && shiftok {
								wm.currMonitor.CurrWorkspace.windowList = append(wm.currMonitor.CurrWorkspace.windowList, &window)
								wm.setWindowDesktop(w, uint32(wm.currMonitor.workspaceIndex))
							}
							wm.fitToLayout()
						}
					}
				}
			}

		case xproto.ClientMessageEvent:
			fmt.Println("client message")

			atomName, _ := xproto.GetAtomName(wm.conn, ev.Type).Reply()
			fmt.Println("ClientMessage atom:", atomName.Name)

			if atomName.Name == "_NET_CURRENT_DESKTOP" {
				desktop := int(ev.Data.Data32[0])
				wm.switchWorkspace(desktop)
			}

			if atomName.Name == "_NET_WM_STATE" && wm.config.AutoFullscreen {
				fullscreenAtom, _ := wm.internAtom("_NET_WM_STATE_FULLSCREEN")
				maxHorzAtom, _ := wm.internAtom("_NET_WM_STATE_MAXIMIZED_HORZ")
				maxVertAtom, _ := wm.internAtom("_NET_WM_STATE_MAXIMIZED_VERT")

				action := ev.Data.Data32[0] // 0 = remove, 1 = add, 2 = toggle
				prop1 := ev.Data.Data32[1]
				prop2 := ev.Data.Data32[2]

				if _, ok := wm.windows[ev.Window]; !ok {
					break
				}

				if prop1 == uint32(maxHorzAtom) || prop2 == uint32(maxHorzAtom) ||
					prop1 == uint32(maxVertAtom) || prop2 == uint32(maxVertAtom) {
					fmt.Println("maximized called, action", action)
					switch action {
					case 0: // remove
						wm.disableFullscreen(wm.windows[ev.Window], ev.Window)
					case 1: // add
						wm.fullscreen(wm.windows[ev.Window], ev.Window)
					case 2: // toggle
						wm.toggleFullScreen(ev.Window)
					}
					break
				}
				if prop1 == uint32(fullscreenAtom) || prop2 == uint32(fullscreenAtom) {
					fmt.Println("Fullscreen request! Action:", action)

					switch action {
					case 0: // remove
						wm.disableFullscreen(wm.windows[ev.Window], ev.Window)
					case 1: // add
						wm.fullscreen(wm.windows[ev.Window], ev.Window)
					case 2: // toggle
						wm.toggleFullScreen(ev.Window)
					}
				}
			}

		default:
			fmt.Println("event: " + event.String())
			fmt.Println(event.Bytes()) //nolint:staticcheck
		}
	}
}

func (wm *WindowManager) resizeTiledX(increase bool, ev xproto.KeyPressEvent) bool { //nolint:unparam
	geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
	if err != nil {
		return false
	}
	X := uint16(geom.X - int16(wm.config.Gap) - int16(wm.currMonitor.TilingSpace.X))
	W := geom.Width + uint16(wm.config.Gap*2)
	if math.Abs(float64(uint16(wm.currMonitor.TilingSpace.X+wm.currMonitor.TilingSpace.Width)-(X+W))) <= 10 {
		return false
	}

	var resizeLayout ResizeLayout
	ok := true
	for _, win := range wm.currMonitor.CurrWorkspace.windowList {
		geomwin, err := xproto.GetGeometry(wm.conn, xproto.Drawable(win.id)).Reply()
		if err != nil {
			continue
		}
		winX := uint16(geomwin.X - int16(wm.config.Gap) - int16(wm.currMonitor.TilingSpace.X))
		winY := uint16(geomwin.Y - int16(wm.config.Gap) - int16(wm.currMonitor.TilingSpace.Y))
		winH := geomwin.Height + uint16(wm.config.Gap*2)
		winW := geomwin.Width + uint16(wm.config.Gap*2)
		// if diff between ends of windows it less than five, they are same column
		if math.Abs(float64((X+W)-(winX+winW))) <= 10 {
			if increase {
				winW += uint16(wm.config.Resize)
			} else {
				winW -= uint16(wm.config.Resize)
			}
			fmt.Println(winW)
		} else if math.Abs(float64(int(winX)-(int(X)+int(W)))) <= 10 {
			if increase {
				winX += uint16(wm.config.Resize)
				winW -= uint16(wm.config.Resize)
				if winW < 50 {
					ok = false
					break
				}
			} else {
				winX -= uint16(wm.config.Resize)
				winW += uint16(wm.config.Resize)
				if W < 50 {
					ok = false
					break
				}
			}
		}

		fmt.Println(winX, winY, winW, winH)
		resizeLayout.Windows = append(resizeLayout.Windows, RLayoutWindow{
			X:      winX,
			Y:      winY,
			Width:  winW,
			Height: winH,
		})
	}

	if ok {
		wm.currMonitor.CurrWorkspace.resized = true
		wm.currMonitor.CurrWorkspace.resizedLayout = resizeLayout
		wm.fitToLayout()
		return true
	}
	return false
}

func (wm *WindowManager) resizeTiledY(increase bool, ev xproto.KeyPressEvent) bool { //nolint:unparam
	geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
	if err != nil {
		return false
	}
	Y := uint16(geom.Y - int16(wm.config.Gap) - int16(wm.currMonitor.TilingSpace.Y))
	H := geom.Height + uint16(wm.config.Gap*2)
	if math.Abs(float64(uint16(wm.currMonitor.TilingSpace.X+wm.currMonitor.TilingSpace.Height)-(Y+H))) <= 10 {
		return false
	}

	var resizeLayout ResizeLayout
	ok := true
	for _, win := range wm.currMonitor.CurrWorkspace.windowList {
		geomwin, err := xproto.GetGeometry(wm.conn, xproto.Drawable(win.id)).Reply()
		if err != nil {
			continue
		}
		winX := uint16(geomwin.X - int16(wm.config.Gap) - int16(wm.currMonitor.TilingSpace.X))
		winY := uint16(geomwin.Y - int16(wm.config.Gap) - int16(wm.currMonitor.TilingSpace.Y))
		winH := geomwin.Height + uint16(wm.config.Gap*2)
		winW := geomwin.Width + uint16(wm.config.Gap*2)
		// if diff between ends of windows it less than five, they are same column
		if math.Abs(float64((int(Y)+int(H))-(int(winY)+int(winH)))) <= 10 {
			if increase {
				winH += uint16(wm.config.Resize)
			} else {
				winH -= uint16(wm.config.Resize)
			}
			fmt.Println(winW)
		} else if math.Abs(float64(int(winY)-(int(Y)+int(H)))) <= 10 {
			if increase {
				winY += uint16(wm.config.Resize)
				winH -= uint16(wm.config.Resize)
				if winH < 50 {
					ok = false
					break
				}
			} else {
				winY -= uint16(wm.config.Resize)
				winH += uint16(wm.config.Resize)
				if H < 50 {
					ok = false
					break
				}
			}
		}

		fmt.Println(winX, winY, winW, winH)
		resizeLayout.Windows = append(resizeLayout.Windows, RLayoutWindow{
			X:      winX,
			Y:      winY,
			Width:  winW,
			Height: winH,
		})
	}

	if ok {
		wm.currMonitor.CurrWorkspace.resized = true
		wm.currMonitor.CurrWorkspace.resizedLayout = resizeLayout
		wm.fitToLayout()
		return true
	}
	return false
}

func (wm *WindowManager) internAtom(name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(wm.conn, true, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

func (wm *WindowManager) declareSupportedAtoms() {
	// List the names of EWMH atoms your WM supports
	atomNames := []string{
		"_NET_SUPPORTED",
		"_NET_WM_STATE",
		"_NET_WM_NAME",
		"_WM_NAME",
		"_NET_WM_STATE_FULLSCREEN",
		"_NET_CURRENT_DESKTOP",
		"_NET_NUMBER_OF_DESKTOPS",
		"_NET_ACTIVE_WINDOW",
		"_NET_WM_DESKTOP",
		"_NET_CLIENT_LIST",
		"_NET_CLOSE_WINDOW",
		"_NET_WM_MOVERESIZE",
		"_NET_WM_STATE_MAXIMIZED_HORZ",
		"_NET_WM_STATE_MAXIMIZED_VERT",
	}

	atoms := make([]xproto.Atom, 0, len(atomNames))
	for _, name := range atomNames {
		atom, err := xproto.InternAtom(wm.conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			slog.Error("Intern atom", "name", name, "err", err)
			continue
		}
		wm.atoms[name] = atom.Atom
		atoms = append(atoms, atom.Atom)
	}

	// Build the property data
	data := make([]byte, 4*len(atoms))
	for i, atom := range atoms {
		binary.LittleEndian.PutUint32(data[i*4:], uint32(atom))
	}

	// Set the _NET_SUPPORTED property
	err := xproto.ChangePropertyChecked(
		wm.conn,
		xproto.PropModeReplace,
		wm.root,
		wm.atoms["_NET_SUPPORTED"],
		xproto.AtomAtom,
		32,
		uint32(len(atoms)),
		data,
	).Check()
	if err != nil {
		slog.Error("Couldn't set _NET_SUPPORTED", "err", err)
	}
}

func focusWindow(conn *xgb.Conn, win xproto.Window) {
	err := xproto.SetInputFocusChecked(
		conn,
		xproto.InputFocusPointerRoot, // or InputFocusNone / InputFocusParent
		win,
		xproto.TimeCurrentTime,
	).Check()
	if err != nil {
		fmt.Println("Error focusing window:", err)
	}
}

func swapWindows(arr *[]*Window, first int, last int) {
	(*arr)[first], (*arr)[last] = (*arr)[last], (*arr)[first]
}

func swapWindowsID(arr *[]*Window, first xproto.Window, last xproto.Window) {
	var res1 int
	var res2 int
	for i, win := range *arr {
		if win.id == first { //nolint:staticcheck
			res1 = i
		} else if win.id == last {
			res2 = i
		}
	}
	swapWindows(arr, res1, res2)
}

func remove(arr *[]*Window, id xproto.Window) {
	if len(*arr) == 1 {
		*arr = []*Window{}
	}
	for index := range *arr {
		if (*arr)[index].id == id {
			*arr = append((*arr)[:index], (*arr)[index+1:]...)
			return
		}
	}
}

func runCommand(cmdStr string) {
	parser := shellwords.NewParser()
	args, err := parser.Parse(cmdStr)
	if err != nil {
		slog.Error("Parsing error:", "error:", err)
		return
	}
	if len(args) == 0 {
		return
	}
	if len(args) < 2 {
		cmd := exec.Command(args[0])
		_ = cmd.Start()
		return
	}
	cmd := exec.Command(args[0], args[1:]...)
	_ = cmd.Start()
}

func (wm *WindowManager) getBar(vals []byte) (int, int, int, int) {
	// calculates where the bar is (more explanatary in createTilingSpace)

	var maxLeft, maxRight, maxTop, maxBottom int
	left := int(binary.LittleEndian.Uint32(vals[0:4]))
	right := int(binary.LittleEndian.Uint32(vals[4:8]))
	top := int(binary.LittleEndian.Uint32(vals[8:12]))
	bottom := int(binary.LittleEndian.Uint32(vals[12:16]))

	if left > maxLeft {
		maxLeft = left
	}
	if right > maxRight {
		maxRight = right
	}
	if top > maxTop {
		maxTop = top
	}
	if bottom > maxBottom {
		maxBottom = bottom
	}
	return maxLeft, maxRight, maxTop, maxBottom
}

func (wm *WindowManager) createTilingSpace() {
	// look at all windows and if it has the property _NET_WM_STRUT_PARTIAL (what most bars have) it means that it
	// should be worked around
	windows, _ := xproto.QueryTree(wm.conn, wm.root).Reply()
	X := 0
	Y := 0
	width := wm.currMonitor.Width
	height := wm.currMonitor.Height
	fmt.Println("CURR MON VALS X", wm.currMonitor.X, "Y", wm.currMonitor.Y)

	for _, window := range windows.Children {
		geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(window)).Reply()
		if err != nil {
			continue
		}

		mon := wm.currMonitor
		if geom.X < mon.X || geom.X > mon.X+int16(mon.Width) || geom.Y < mon.Y || geom.Y > mon.Y+int16(mon.Height) {
			continue
		}

		attributes, err := xproto.GetWindowAttributes(wm.conn, window).Reply()
		if err != nil {
			continue
		}
		if attributes.MapState == xproto.MapStateViewable {
			atom := wm.atoms["_NET_WM_STRUT_PARTIAL"]
			prop, err := xproto.GetProperty(wm.conn, false, window, atom, xproto.AtomCardinal, 0, 12).
				Reply()

			if err != nil || prop == nil || prop.ValueLen < 4 {
				continue
			}

			vals := prop.Value
			if len(vals) < 16 {
				continue // need at least 4 uint32s
			}
			left, right, top, bottom := wm.getBar(vals)

			// create space to work around bar (if there is one)
			X = left
			Y = top
			width = uint16(int(wm.currMonitor.Width) - left - right)
			height = uint16(int(wm.currMonitor.Height) - top - bottom)

			// TODO: support multiple bars
			break
		}
	}

	fmt.Println("tiling container:", "X:", X, "Y:", Y, "Width:", width, "Height:", height)
	wm.currMonitor.TilingSpace = Space{
		X:      int(wm.currMonitor.X) + X + int(wm.config.OuterGap),
		Y:      int(wm.currMonitor.Y) + Y + int(wm.config.OuterGap),
		Width:  int(width-6) - (int(wm.config.OuterGap) * 2),
		Height: int(height-6) - (int(wm.config.OuterGap) * 2),
	}
}

func (wm *WindowManager) fitToLayout() {
	if !wm.currMonitor.CurrWorkspace.tiling {
		return
	}
	// if there are more than 4 windows then just don't do it

	windowNum := len(wm.currMonitor.CurrWorkspace.windowList)

	if _, ok := wm.config.lyts[windowNum]; !ok {
		return
	}

	if len(wm.config.lyts[windowNum])-1 < wm.currMonitor.layoutIndex &&
		len(wm.config.lyts[windowNum]) > 0 {
		wm.currMonitor.CurrWorkspace.layoutIndex = 0
		wm.currMonitor.layoutIndex = 0
	}

	if windowNum > len(wm.config.lyts) || windowNum < 1 ||
		windowNum > len(wm.config.lyts[windowNum][wm.currMonitor.layoutIndex].Windows) {
		fmt.Println(
			"too many or too few windows to fit to layout in workspace",
			wm.currMonitor.workspaceIndex+1,
		)
		return
	}
	wm.createTilingSpace()
	layout := wm.config.lyts[windowNum][wm.currMonitor.layoutIndex]
	if wm.currMonitor.CurrWorkspace.resized && len(wm.currMonitor.CurrWorkspace.resizedLayout.Windows) != windowNum {
		wm.currMonitor.CurrWorkspace.resized = false
		wm.currMonitor.CurrWorkspace.resizedLayout = ResizeLayout{}
	}
	fmt.Println("fit to layout")
	fmt.Println(wm.currMonitor.CurrWorkspace.windowList)
	// fmt.Println(wm.currMonitor.CurrWorkspace.windows)
	// fmt.Println(len(wm.currMonitor.CurrWorkspace.windows))
	// for each window put it in its place and size specified by that layout
	fullscreen := []xproto.Window{}
	for i, WindowData := range wm.currMonitor.CurrWorkspace.windowList {
		fmt.Println(WindowData)
		if WindowData.Fullscreen {
			fullscreen = append(fullscreen, WindowData.id)
			continue
		}
		if wm.currMonitor.CurrWorkspace.resized {
			layoutWindow := wm.currMonitor.CurrWorkspace.resizedLayout.Windows[i]
			X := uint32(layoutWindow.X) + wm.config.Gap + uint32(wm.currMonitor.TilingSpace.X)
			Y := uint32(layoutWindow.Y) + wm.config.Gap + uint32(wm.currMonitor.TilingSpace.Y)
			Width := uint32(layoutWindow.Width) - (wm.config.Gap * 2)
			Height := uint32(layoutWindow.Height) - (wm.config.Gap * 2)
			fmt.Println(
				"window:",
				WindowData.id,
				"X:",
				X,
				"rounded:",
				"Y:",
				Y,
				"Width:",
				Width,
				"Height:",
				Height,
			)
			wm.configureWindow(WindowData.id, int(X), int(Y), int(Width), int(Height))
		} else {
			layoutWindow := layout.Windows[i]
			// because we use percentages we have to times the width and height of the tiling space to get the raw
			// value, it is simple maths to do the gap, I shouldn't have to explain it
			X := wm.currMonitor.TilingSpace.X + int((float64(wm.currMonitor.TilingSpace.Width) * layoutWindow.XPercentage)) + int(wm.config.Gap)
			Y := wm.currMonitor.TilingSpace.Y + int((float64(wm.currMonitor.TilingSpace.Height) * layoutWindow.YPercentage)) + int(wm.config.Gap)
			Width := (float64(wm.currMonitor.TilingSpace.Width) * layoutWindow.WidthPercentage) - float64(wm.config.Gap*2)
			Height := (float64(wm.currMonitor.TilingSpace.Height) * layoutWindow.HeightPercentage) - float64(wm.config.Gap*2)
			fmt.Println("window:", WindowData.id, "X:", X, "rounded:", int(math.Round(Width)),
				"Y:", Y, "Width:", Width, "Height:", Height)
			wm.configureWindow(WindowData.id, X, Y, int(math.Round(Width)), int(math.Round(Height)))
		}
	}
	if len(fullscreen) > 0 {
		for _, win := range fullscreen {
			xproto.ConfigureWindow(
				wm.conn,
				win,
				xproto.ConfigWindowStackMode,
				[]uint32{xproto.StackModeAbove},
			)
			wm.fullscreen(wm.windows[win], win)
		}
	}
}

func (wm *WindowManager) configureWindow(frame xproto.Window, x, y, width, height int) {
	// configure the window to how it wants to be
	_ = xproto.ConfigureWindowChecked(
		wm.conn,
		frame,
		xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{
			uint32(x), uint32(y), uint32(width), uint32(height),
		},
	).
		Check()
}

func (wm *WindowManager) toggleTiling() {
	if !wm.currMonitor.CurrWorkspace.detachTiling {
		if !wm.currMonitor.tiling {
			wm.currMonitor.tiling = true
			wm.enableTiling()
		} else {
			wm.currMonitor.tiling = false
			wm.disableTiling()
		}
	} else {
		if !wm.currMonitor.CurrWorkspace.tiling {
			wm.enableTiling()
		} else {
			wm.disableTiling()
		}
	}
}

func (wm *WindowManager) disableTiling() {
	wm.currMonitor.CurrWorkspace.tiling = false
	fmt.Println("DISABLED TILING")
	// restore windows to there previous state (before tiling)
	for _, window := range wm.currMonitor.CurrWorkspace.windowList {
		wm.configureWindow(window.id, window.X, window.Y, window.Width, window.Height)
	}
	wm.setNetWorkArea()
}

func (wm *WindowManager) enableTiling() {
	wm.currMonitor.CurrWorkspace.tiling = true
	// make sure no windows are fullscreened and that there state is saved (so it can be restored later if/when the user
	// disables tiling)
	for i, window := range wm.currMonitor.CurrWorkspace.windowList {
		fmt.Println(window.id)
		attr, _ := xproto.GetGeometry(wm.conn, xproto.Drawable(window.id)).Reply()
		wm.currMonitor.CurrWorkspace.windowList[i] = &Window{
			id:         window.id,
			X:          int(attr.X),
			Y:          int(attr.Y),
			Width:      int(attr.Width),
			Height:     int(attr.Height),
			Fullscreen: false,
			Client:     window.Client,
		}
	}
	fmt.Println("tiling")
	// put the windows in the right tiling layout in the right space
	wm.createTilingSpace()
	wm.fitToLayout()
	wm.setNetWorkArea()
}

func (wm *WindowManager) toggleFullScreen(child xproto.Window) {
	win := wm.windows[child]
	if win != nil {
		if win.Fullscreen {
			wm.disableFullscreen(win, child)
		} else {
			wm.fullscreen(win, child)
		}
	}
}

func (wm *WindowManager) disableFullscreen(win *Window, child xproto.Window) {
	fmt.Println("DISABLING FULL SCREEN")
	wm.windows[child].Fullscreen = false
	for i, window := range wm.currMonitor.CurrWorkspace.windowList {
		if window.id == child {
			wm.currMonitor.CurrWorkspace.windowList[i].Fullscreen = false
		}
		fmt.Println(window.Fullscreen)
	}
	// set the frame back to what it used to be same with the client, but sort out tiling layout anyway just in case
	err := xproto.ConfigureWindowChecked(
		wm.conn,
		child,
		xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight|xproto.ConfigWindowBorderWidth,
		[]uint32{
			uint32(win.X),
			uint32(win.Y),
			uint32(win.Width),
			uint32(win.Height),
			wm.config.BorderWidth,
		},
	).Check()
	if err != nil {
		slog.Error("Couldn't un-fullscreen window", "error: ", err)
	}
	wm.removeFullScreenEWMH(child)
	wm.fitToLayout()
}

func (wm *WindowManager) setFullScreenEWMH(win xproto.Window) {
	// Intern the atoms we need
	netWmState := wm.atoms["_NET_WM_STATE"]
	netWmStateFullscreen := wm.atoms["_NET_WM_STATE_FULLSCREEN"]

	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(netWmStateFullscreen))
	// Change the window property to include fullscreen
	err := xproto.ChangePropertyChecked(
		wm.conn,
		xproto.PropModeReplace,
		win,
		netWmState,
		xproto.AtomAtom,
		32,
		1,
		data,
	).Check()
	if err != nil {
		return
	}
}

func (wm *WindowManager) removeFullScreenEWMH(win xproto.Window) {
	netWmState := wm.atoms["_NET_WM_STATE"]
	// Change the window property to remove fullscreen
	err := xproto.ChangePropertyChecked(
		wm.conn,
		xproto.PropModeReplace,
		win,
		netWmState,
		xproto.AtomAtom,
		32,
		0,
		nil,
	).Check()
	if err != nil {
		return
	}
}

func (wm *WindowManager) fullscreen(_ *Window, child xproto.Window) {
	// set window state so it can be restored later then configure window to be full width and height, sam with client,
	// also take away border
	wm.windows[child].Fullscreen = true
	for i, window := range wm.currMonitor.CurrWorkspace.windowList {
		if window.id == child {
			wm.currMonitor.CurrWorkspace.windowList[i].Fullscreen = true
		}
	}
	xproto.ConfigureWindow(
		wm.conn,
		child,
		xproto.ConfigWindowStackMode,
		[]uint32{xproto.StackModeAbove},
	)
	attr, _ := xproto.GetGeometry(wm.conn, xproto.Drawable(child)).Reply()
	win := wm.windows[child]
	win.X = int(attr.X)
	win.Y = int(attr.Y)
	win.Width = int(attr.Width)
	win.Height = int(attr.Height)
	err := xproto.ConfigureWindowChecked(
		wm.conn,
		child,
		xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight|xproto.ConfigWindowBorderWidth,
		[]uint32{
			uint32(wm.currMonitor.X),
			uint32(wm.currMonitor.Y),
			uint32(wm.currMonitor.Width),
			uint32(wm.currMonitor.Height),
			0,
		},
	).Check()
	if err != nil {
		slog.Error("Couldn't fullscreen window", "error:", err)
	}
	wm.setFullScreenEWMH(child)
}

func (wm *WindowManager) broadcastWorkspaceCount() {
	// EMWH things for bars to show workspaces
	count := wm.currMonitor.workspaceIndex + 1
	otherCount := 0
	for i, workspace := range wm.currMonitor.Workspaces {
		if len(workspace.windowList) > 0 {
			otherCount = i
		}
	}
	otherCount++
	if otherCount > count {
		count = otherCount
	}
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(count))

	netNumberAtom, _ := xproto.InternAtom(
		wm.conn,
		true,
		uint16(len("_NET_NUMBER_OF_DESKTOPS")),
		"_NET_NUMBER_OF_DESKTOPS",
	).
		Reply()
	cardinalAtom, _ := xproto.InternAtom(wm.conn, true, uint16(len("CARDINAL")), "CARDINAL").Reply()

	_ = xproto.ChangePropertyChecked(
		wm.conn,
		xproto.PropModeReplace,
		wm.root,
		netNumberAtom.Atom,
		cardinalAtom.Atom,
		32,
		1,
		data,
	).Check()
}

func (wm *WindowManager) broadcastWorkspace(num int) {
	// EMWH thing for bars to show workspaces
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(num))

	netCurrentDesktopAtom, err := xproto.InternAtom(
		wm.conn,
		false,
		uint16(len("_NET_CURRENT_DESKTOP")),
		"_NET_CURRENT_DESKTOP",
	).
		Reply()
	if err != nil {
		slog.Error("Intern _NET_CURRENT_DESKTOP", "error:", err)
		return
	}

	cardinalAtom, err := xproto.InternAtom(wm.conn, true, uint16(len("CARDINAL")), "CARDINAL").
		Reply()
	if err != nil {
		slog.Error("Intern CARDINAL", "error:", err)
		return
	}
	fmt.Println(netCurrentDesktopAtom.Atom)
	fmt.Println(cardinalAtom.Atom)
	err = xproto.ChangePropertyChecked(
		wm.conn,
		xproto.PropModeReplace,
		wm.root,
		netCurrentDesktopAtom.Atom, // must not be 0
		cardinalAtom.Atom,          // must not be 0
		32,
		1,
		data,
	).Check()
	if err != nil {
		slog.Error("Couldn't set _NET_CURRENT_DESKTOP", "error:", err)
	}

	wm.broadcastWorkspaceCount()
}

func (wm *WindowManager) switchWorkspace(workspace int) {
	if workspace < 0 || workspace >= len(wm.currMonitor.Workspaces) {
		return
	}

	if !wm.config.WorkspaceAutoBackAndForth && workspace == wm.currMonitor.workspaceIndex {
		return
	}

	// WorkspaceAutoBackAndForth is enabled
	if workspace == wm.currMonitor.workspaceIndex {
		// switch back to last workspace
		workspace = wm.currMonitor.lastWorkspaceIndex
		slog.Debug("Switch back to", "workspace", workspace)
	} else {
		// remember last workspace for next switch
		slog.Debug("Remember", "workspace", wm.currMonitor.workspaceIndex)
	}
	wm.currMonitor.lastWorkspaceIndex = wm.currMonitor.workspaceIndex

	// unmap all windows in current workspace
	for _, frame := range wm.currMonitor.CurrWorkspace.windowList {
		xproto.UnmapWindowChecked(wm.conn, frame.id)
	}

	// swap workspace
	wm.currMonitor.CurrWorkspace = &wm.currMonitor.Workspaces[workspace]
	wm.currMonitor.workspaceIndex = workspace

	// map all the windows in the other workspace
	for _, frame := range wm.currMonitor.CurrWorkspace.windowList {
		xproto.MapWindowChecked(wm.conn, frame.id)
	}

	wm.conn.Sync()

	// update tiling
	if !wm.currMonitor.CurrWorkspace.detachTiling {
		if wm.currMonitor.tiling && !wm.currMonitor.CurrWorkspace.tiling {
			wm.enableTiling()
		} else if !wm.currMonitor.tiling && wm.currMonitor.CurrWorkspace.tiling {
			wm.disableTiling()
		}
	}
	wm.broadcastWorkspace(workspace)
	wm.currMonitor.layoutIndex = wm.currMonitor.CurrWorkspace.layoutIndex
}

func (wm *WindowManager) sendWmDelete(conn *xgb.Conn, window xproto.Window) error {
	// polite EMWH way of telling the window to delete itself
	wmProtocolsAtom, _ := xproto.InternAtom(conn, true, uint16(len("WM_PROTOCOLS")), "WM_PROTOCOLS").
		Reply()
	wmDeleteAtom, _ := xproto.InternAtom(conn, true, uint16(len("WM_DELETE_WINDOW")), "WM_DELETE_WINDOW").
		Reply()

	prop, err := xproto.GetProperty(conn, false, window, wmProtocolsAtom.Atom, xproto.AtomAtom, 0, (1<<32)-1).
		Reply()
	if err != nil || prop.Format != 32 {
		return fmt.Errorf("could not get WM_PROTOCOLS %w", err)
	}

	supportsDelete := false
	for i := range int(prop.ValueLen) {
		atom := xgb.Get32(prop.Value[i*4:])
		if xproto.Atom(atom) == wmDeleteAtom.Atom {
			supportsDelete = true
			break
		}
	}

	if !supportsDelete {
		return errors.New("WM_DELETE_WINDOW not supported")
	}

	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: window,
		Type:   wmProtocolsAtom.Atom,
		Data: xproto.ClientMessageDataUnionData32New(
			[]uint32{
				uint32(wmDeleteAtom.Atom),
				uint32(xproto.TimeCurrentTime),
				0, 0, 0,
			},
		),
	}

	return xproto.SendEventChecked(
		conn,
		false,
		window,
		xproto.EventMaskNoEvent,
		string(ev.Bytes()),
	).Check()
}

func (wm *WindowManager) onLeaveNotify(event xproto.LeaveNotifyEvent) {
	// change border color when you leave a window
	Col := wm.config.BorderUnactive

	err := xproto.ChangeWindowAttributesChecked(
		wm.conn,
		event.Event,
		xproto.CwBorderPixel,
		[]uint32{
			Col, // border color
		},
	).Check()
	if err != nil {
		slog.Error("Couldn't remove focus from window", "error:", err)
	}
}

func setFrameWindowType(conn *xgb.Conn, win xproto.Window) {
	atomWindowType, _ := xproto.InternAtom(conn, true, uint16(len("_NET_WM_WINDOW_TYPE")), "_NET_WM_WINDOW_TYPE").
		Reply()
	atomNormal, _ := xproto.InternAtom(
		conn,
		true,
		uint16(len("_NET_WM_WINDOW_TYPE_NORMAL")),
		"_NET_WM_WINDOW_TYPE_NORMAL",
	).
		Reply()

	xproto.ChangeProperty(conn,
		xproto.PropModeReplace,
		win,
		atomWindowType.Atom,
		xproto.AtomAtom,
		32,
		1,
		[]byte{
			byte(atomNormal.Atom),
			byte(atomNormal.Atom >> 8),
			byte(atomNormal.Atom >> 16),
			byte(atomNormal.Atom >> 24),
		},
	)
}

func (wm *WindowManager) setNetActiveWindow(win xproto.Window) {
	atomActiveWin, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_ACTIVE_WINDOW")), "_NET_ACTIVE_WINDOW").
		Reply()

	// Convert uint32 to []byte
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, win)

	xproto.ChangeProperty(wm.conn,
		xproto.PropModeReplace,
		wm.root,            // Set on the root window
		atomActiveWin.Atom, // _NET_ACTIVE_WINDOW
		xproto.AtomWindow,  // Type: WINDOW
		32,                 // Format: 32-bit
		1,                  // Only one window
		buf.Bytes(),        // Here's the []byte version
	)
}

func (wm *WindowManager) setNetWorkArea() {
	atomWorkArea, err := xproto.InternAtom(wm.conn, true, uint16(len("_NET_WORKAREA")), "_NET_WORKAREA").
		Reply()
	if err != nil {
		// handle error properly here
		return
	}

	buf := new(bytes.Buffer)

	spaceX, spaceY, spaceWidth, spaceHeight := wm.currMonitor.TilingSpace.X, wm.currMonitor.TilingSpace.Y, wm.currMonitor.TilingSpace.Width, wm.currMonitor.TilingSpace.Height //nolint: lll

	for _, wksp := range wm.currMonitor.Workspaces {
		if !wksp.tiling {
			_ = binary.Write(buf, binary.LittleEndian, uint32(0))
			_ = binary.Write(buf, binary.LittleEndian, uint32(0))
			_ = binary.Write(buf, binary.LittleEndian, uint32(wm.currMonitor.Width))
			_ = binary.Write(buf, binary.LittleEndian, uint32(wm.currMonitor.Height))
		} else {
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceX))
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceY))
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceWidth))
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceHeight))
		}
	}

	// Number of 32-bit CARDINAL values: 4 values per workspace
	numValues := uint32(4 * len(wm.currMonitor.Workspaces))

	err = xproto.ChangePropertyChecked(
		wm.conn,
		xproto.PropModeReplace,
		wm.root,
		atomWorkArea.Atom,
		xproto.AtomCardinal,
		32,
		numValues,
		buf.Bytes(),
	).Check()
	if err != nil {
		slog.Error("Couldn't set the work area", "error:", err)
	}
}

func (wm *WindowManager) setNetClientList() {
	atomClientList, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_CLIENT_LIST")), "_NET_CLIENT_LIST").
		Reply()

	buf := new(bytes.Buffer)
	for _, info := range wm.windows {
		_ = binary.Write(buf, binary.LittleEndian, info.Client)
	}

	xproto.ChangeProperty(wm.conn,
		xproto.PropModeReplace,
		wm.root,
		atomClientList.Atom,
		xproto.AtomWindow,
		32,
		uint32(len(wm.windows)),
		buf.Bytes(),
	)
}

func (wm *WindowManager) onEnterNotify(event xproto.EnterNotifyEvent) {
	// set focus when we enter a window and change border color
	err := xproto.SetInputFocusChecked(wm.conn, xproto.InputFocusPointerRoot, event.Event, xproto.TimeCurrentTime).
		Check()
	if err != nil {
		slog.Error("Couldn't set input focus", "error", err)
	}
	Col := wm.config.BorderActive
	err = xproto.ChangeWindowAttributesChecked(
		wm.conn,
		event.Event,
		xproto.CwBorderPixel,
		[]uint32{
			Col, // border color
		},
	).Check()
	if err != nil {
		slog.Error("Couldn't set focus on window", "error:", err)
	}
	wm.setNetActiveWindow(event.Event)
}

func (wm *WindowManager) findWindow(window xproto.Window) (bool, int, xproto.Window) { //nolint:unparam
	fmt.Println("FINDING WINDOW", window)
	// look through all workspaces and windows to find a window (this is for if a window is deleted by a window from
	// another workspace, we need to search for it)
	for i, workspace := range wm.currMonitor.Workspaces {
		if i == wm.currMonitor.workspaceIndex {
			continue
		}

		for _, frame := range workspace.windowList {
			fmt.Println(frame.Width, frame.id)
			if frame.id == window {
				return true, i, frame.id
			}
		}
	}
	return false, 0, 0
}

func (wm *WindowManager) onUnmapNotify(event xproto.UnmapNotifyEvent) {
	if event.Event == wm.root {
		slog.Info("Ignore UnmapNotify for reparented pre-existing window")
		fmt.Println(event.Window)
		return
	}

	found := false
	for _, win := range wm.currMonitor.CurrWorkspace.windowList {
		if win.id == event.Window {
			found = true
			break
		}
	}
	if !found {
		fmt.Println("IN UNMAPPING COULDNT FIND WIN NOW SEARCHING")
		ok, index, _ := wm.findWindow(event.Window)
		if !ok {
			slog.Info("Couldn't unmap since window wasn't in clients")
			fmt.Println(event.Window)
			return
		}
		wm.currMonitor.CurrWorkspace = &wm.currMonitor.Workspaces[index]
		fmt.Println("IN WORKSPACE", index)
		wm.unFrame(event.Window, false)
		wm.currMonitor.CurrWorkspace = &wm.currMonitor.Workspaces[wm.currMonitor.workspaceIndex]
		return
	}
	wm.unFrame(event.Window, false)
	wm.fitToLayout()
}

func (wm *WindowManager) remDestroyedWin(window xproto.Window) {
	found := false
	for _, win := range wm.currMonitor.CurrWorkspace.windowList {
		if win.id == window {
			found = true
			break
		}
	}
	if !found {
		fmt.Println("IN UNMAPPING COULDNT FIND WIN NOW SEARCHING")
		ok, index, _ := wm.findWindow(window)
		if !ok {
			slog.Info("Couldn't unmap since window wasn't in clients")
			fmt.Println(window)
			return
		}
		wm.currMonitor.CurrWorkspace = &wm.currMonitor.Workspaces[index]
		fmt.Println("IN WORKSPACE", index, wm.currMonitor.CurrWorkspace.windowList)
		wm.unFrame(window, false)
		wm.currMonitor.CurrWorkspace = &wm.currMonitor.Workspaces[wm.currMonitor.workspaceIndex]
		return
	}

	wm.unFrame(window, false)
	wm.fitToLayout()
}

func (wm *WindowManager) unFrame(w xproto.Window, _ bool) {
	// if it is already unmapped then no need to do it again
	err := xproto.UnmapWindowChecked(
		wm.conn,
		w,
	).Check()
	if err != nil {
		slog.Error("Couldn't unmap frame", "error:", err.Error())
	}
	// remove window and frame from current workspace record
	remove(&wm.currMonitor.CurrWorkspace.windowList, w)
	delete(wm.windows, w)
	wm.setNetClientList()

	// delete window from x11 set
	err = xproto.ChangeSaveSetChecked(
		wm.conn,
		xproto.SetModeDelete,
		w,
	).Check()
	if err != nil {
		slog.Error("Couldn't remove window from save", "error:", err.Error())
	}

	// destroy frame
	err = xproto.DestroyWindowChecked(
		wm.conn,
		w,
	).Check()
	if err != nil {
		slog.Error("Couldn't destroy frame", "error:", err.Error())
		return
	}

	slog.Info("Unmapped", "window", w)
}

func (wm *WindowManager) setWindowDesktop(win xproto.Window, desktop uint32) {
	atomWmDesktop, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_WM_DESKTOP")), "_NET_WM_DESKTOP").
		Reply()

	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, desktop)

	xproto.ChangeProperty(wm.conn,
		xproto.PropModeReplace,
		win,                 // client window
		atomWmDesktop.Atom,  // _NET_WM_DESKTOP
		xproto.AtomCardinal, // CARDINAL
		32,
		1,
		buf.Bytes(),
	)
}

func shouldIgnoreWindow(conn *xgb.Conn, win xproto.Window) bool {
	// some windows don't want to be registered by the WM so we check that

	// Intern the _NET_WM_WINDOW_TYPE atom
	typeAtom, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE")), "_NET_WM_WINDOW_TYPE").
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE atom", "error", err)
		return false
	}

	// Get the _NET_WM_WINDOW_TYPE property for the window
	actualType, err := xproto.GetProperty(conn, false, win, typeAtom.Atom, xproto.AtomAtom, 0, 1).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE property", "error", err)
		return false
	}

	if len(actualType.Value) == 0 {
		return false
	}

	// Check if the window has the _NET_WM_WINDOW_TYPE_SPLASH, _NET_WM_WINDOW_TYPE_DIALOG,
	// _NET_WM_WINDOW_TYPE_NOTIFICATION, or _NET_WM_WINDOW_TYPE_DOCK
	netWmSplash, err := xproto.InternAtom(
		conn,
		false,
		uint16(len("_NET_WM_WINDOW_TYPE_SPLASH")),
		"_NET_WM_WINDOW_TYPE_SPLASH",
	).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_SPLASH atom", "error", err)
		return false
	}
	netWmPanel, err := xproto.InternAtom(
		conn,
		false,
		uint16(len("_NET_WM_WINDOW_TYPE_PANEL")),
		"_NET_WM_WINDOW_TYPE_PANEL",
	).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_PANEL atom", "error", err)
		return false
	}

	netWmTooltip, err := xproto.InternAtom(
		conn,
		false,
		uint16(len("_NET_WM_WINDOW_TYPE_TOOLTIP")),
		"_NET_WM_WINDOW_TYPE_TOOLTIP",
	).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_PANEL atom", "error", err)
		return false
	}

	netWmDialog, err := xproto.InternAtom(
		conn,
		false,
		uint16(len("_NET_WM_WINDOW_TYPE_DIALOG")),
		"_NET_WM_WINDOW_TYPE_DIALOG",
	).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_DIALOG atom", "error", err)
		return false
	}

	netWmNotification, err := xproto.InternAtom(
		conn,
		false,
		uint16(len("_NET_WM_WINDOW_TYPE_NOTIFICATION")),
		"_NET_WM_WINDOW_TYPE_NOTIFICATION",
	).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_NOTIFICATION atom", "error", err)
		return false
	}

	netWmDock, err := xproto.InternAtom(
		conn,
		false,
		uint16(len("_NET_WM_WINDOW_TYPE_DOCK")),
		"_NET_WM_WINDOW_TYPE_DOCK",
	).
		Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_DOCK atom", "error", err)
		return false
	}

	// Check if the window type matches any of the "ignore" types
	windowType := xproto.Atom(binary.LittleEndian.Uint32(actualType.Value))

	if windowType == netWmSplash.Atom || windowType == netWmDialog.Atom ||
		windowType == netWmNotification.Atom ||
		windowType == netWmDock.Atom ||
		windowType == netWmPanel.Atom ||
		windowType == netWmTooltip.Atom {
		return true
	}

	return false
}

func (wm *WindowManager) isAbove(w xproto.Window) {
	fmt.Println(wm.atoms)
	stateAtom, ok := wm.atoms["_NET_WM_STATE"]
	if ok {
		stateAboveAtom, ok := wm.atoms["_NET_WM_STATE_ABOVE"]
		if ok {
			// Get property
			prop, err := xproto.GetProperty(wm.conn, false, w, stateAtom,
				xproto.AtomAtom, 0, 1024).Reply()
			if err != nil {
				slog.Error("Error getting _NET_WM_STATE", "error:", err)
				return
			}

			// Iterate through atoms in the property
			for i := 0; i+4 <= len(prop.Value); i += 4 {
				atom := xproto.Atom(uint32(prop.Value[i]) |
					uint32(prop.Value[i+1])<<8 |
					uint32(prop.Value[i+2])<<16 |
					uint32(prop.Value[i+3])<<24)

				if atom == stateAboveAtom {
					xproto.ConfigureWindow(
						wm.conn,
						w,
						xproto.ConfigWindowStackMode,
						[]uint32{xproto.StackModeAbove},
					)
					break
				}
			}
		}
	}
}

func (wm *WindowManager) onMapRequest(event xproto.MapRequestEvent) {
	// if there is a window to be ignored then we just map it but don't handle it
	if shouldIgnoreWindow(wm.conn, event.Window) {
		fmt.Println("ignored window since it is either dock, splash, dialog or notify")
		err := xproto.MapWindowChecked(
			wm.conn,
			event.Window,
		).Check()
		if err != nil {
			slog.Error("Couldn't create new window id", "error:", err.Error())
		}
		return
	}

	// frame the window and make sure to work out the new tiling layout
	wm.frame(event.Window, false)
	if wm.currMonitor.CurrWorkspace.tiling {
		wm.fitToLayout()
	}

	wm.setWindowDesktop(event.Window, uint32(wm.currMonitor.workspaceIndex))
}

func (wm *WindowManager) frame(w xproto.Window, createdBeforeWM bool) {
	if _, exists := wm.windows[w]; exists {
		fmt.Println("Already framed", w)
		return
	}
	BorderWidth := wm.config.BorderWidth
	Col := wm.config.BorderUnactive

	// get the geometry of the window so we can match the frame to it
	geometry, err := xproto.GetGeometry(wm.conn, xproto.Drawable(w)).Reply()
	if err != nil {
		slog.Error("Couldn't get window geometry", "error:", err.Error())
		return
	}

	attribs, err := xproto.GetWindowAttributes(
		wm.conn,
		w,
	).Reply()
	if err != nil {
		slog.Error("Couldn't get window attributes", "error:", err.Error())
		return
	}

	wm.isAbove(w)

	// skips
	if attribs.OverrideRedirect {
		fmt.Println("Skipping override-redirect window", w)
		return
	}

	if createdBeforeWM && attribs.MapState != xproto.MapStateViewable {
		fmt.Println("Skipping unmapped pre-existing window", w)
		return
	}

	// map the frame
	_ = xproto.MapWindowChecked(
		wm.conn,
		w,
	).Check()

	// center it
	windowMidX := math.Round(float64(geometry.Width) / 2)
	windowMidY := math.Round(float64(geometry.Height) / 2)
	screenMidX := math.Round(float64(wm.currMonitor.Width) / 2)
	screenMidY := math.Round(float64(wm.currMonitor.Height) / 2)
	topLeftX := float64(wm.currMonitor.X) + (screenMidX - windowMidX)
	topLeftY := float64(wm.currMonitor.Y) + (screenMidY - windowMidY)

	err = xproto.ConfigureWindowChecked(
		wm.conn,
		w,
		xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{
			uint32(topLeftX),
			uint32(topLeftY),
			uint32(geometry.Width),
			uint32(geometry.Height),
		},
	).
		Check()
	if err != nil {
		slog.Error("Couldn't create new window", "error:", err.Error())
		return
	}

	_ = xproto.ConfigureWindowChecked(
		wm.conn,
		w,
		xproto.ConfigWindowBorderWidth,
		[]uint32{BorderWidth},
	)

	err = xproto.ChangeWindowAttributesChecked(
		wm.conn,
		w,
		xproto.CwBorderPixel|xproto.CwEventMask,
		[]uint32{
			Col, // border color
			xproto.EventMaskSubstructureRedirect |
				xproto.EventMaskSubstructureNotify | xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease,
		},
	).Check()
	if err != nil {
		slog.Error("Couldn't save window attributes", "error:", err.Error())
		return
	}
	// add it to the x11 save set
	err = xproto.ChangeSaveSetChecked(
		wm.conn,
		xproto.SetModeInsert, // add to save set
		w,                    // the client's window ID
	).Check()
	if err != nil {
		slog.Error("Couldn't save window to set", "error:", err.Error())
		return
	}

	err = xproto.ChangeWindowAttributesChecked(wm.conn, w, xproto.CwEventMask, []uint32{
		xproto.EventMaskEnterWindow | xproto.EventMaskLeaveWindow,
	}).Check()
	if err != nil {
		slog.Error("Failed to set event mask on window", "error:", err)
	}

	setFrameWindowType(wm.conn, w)

	wins, err := xproto.QueryTree(wm.conn, wm.root).Reply()
	if err == nil {
		for _, win := range wins.Children {
			wm.isAbove(win)
		}
	}

	// add all of this to the current workspace record
	wm.currMonitor.CurrWorkspace.windowList = append(wm.currMonitor.CurrWorkspace.windowList, &Window{
		X:          int(topLeftX),
		Y:          int(topLeftY),
		Width:      int(geometry.Width),
		Height:     int(geometry.Height),
		Fullscreen: false,
		id:         w,
		Client:     w,
	})
	wm.windows[w] = wm.currMonitor.CurrWorkspace.windowList[len(wm.currMonitor.CurrWorkspace.windowList)-1]
	wm.setNetClientList()
	fmt.Println("Framed window" + strconv.Itoa(int(w)) + "[" + strconv.Itoa(int(w)) + "]")
}

func (wm *WindowManager) onConfigureRequest(event xproto.ConfigureRequestEvent) {
	if _, ok := wm.windows[event.Window]; ok {
		if wm.currMonitor.tiling {
			return
		}
	}
	changes := createChanges(event)

	fmt.Println(event.ValueMask)
	fmt.Println(changes)

	err := xproto.ConfigureWindowChecked(
		wm.conn,
		event.Window,
		event.ValueMask,
		changes,
	).Check()
	if err != nil {
		slog.Error("Couldn't configure window", "error:", err.Error())
	}
}

func createChanges(event xproto.ConfigureRequestEvent) []uint32 {
	// selecting the right values that the window has asked to configure

	changes := make([]uint32, 0, 7)

	if event.ValueMask&xproto.ConfigWindowX != 0 {
		changes = append(changes, uint32(event.X))
	}
	if event.ValueMask&xproto.ConfigWindowY != 0 {
		changes = append(changes, uint32(event.Y))
	}
	if event.ValueMask&xproto.ConfigWindowWidth != 0 {
		changes = append(changes, uint32(event.Width))
	}
	if event.ValueMask&xproto.ConfigWindowHeight != 0 {
		changes = append(changes, uint32(event.Height))
	}
	if event.ValueMask&xproto.ConfigWindowBorderWidth != 0 {
		changes = append(changes, uint32(event.BorderWidth))
	}
	if event.ValueMask&xproto.ConfigWindowSibling != 0 {
		changes = append(changes, uint32(event.Sibling))
	}
	if event.ValueMask&xproto.ConfigWindowStackMode != 0 {
		changes = append(changes, uint32(event.StackMode))
	}

	return changes
}

// Close closes the window manager.
func (wm *WindowManager) Close() {
	// close the connection
	if wm.conn != nil {
		wm.conn.Close()
	}
}

func (wm *WindowManager) setKeyBinds() {
	// workspace keybinds, ik not very idiomatic but its fine :)
	wm.createKeybind(&Keybind{Key: "0", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "1", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "2", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "3", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "4", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "5", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "6", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "7", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "8", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "9", Shift: false, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "0", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "1", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "2", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "3", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "4", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "5", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "6", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "7", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "8", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "9", Shift: true, Keycode: 0})
	wm.createKeybind(&Keybind{Key: "r", Shift: true, Keycode: 0, Role: "reload-config"})
}

// The end.
