package wm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/mattn/go-shellwords"
)

var (
	XUtil *xgbutil.XUtil
)

// config
var k = koanf.New(".")

type Config struct {
	// tiling window gaps, unfocused/focused window border colors, mod key for all wm actions, window border width, keybinds
	Layouts        map[int][]Layout `koanf:"layouts"`
	Gap            uint32           `koanf:"gaps"`
	Resize            uint32           `koanf:"resize-amount"`
	OuterGap       uint32           `koanf:"outer-gap"`
	StartTiling    bool             `koanf:"default-tiling"`
	BorderUnactive uint32           `koanf:"unactive-border-color"`
	BorderActive   uint32           `koanf:"active-border-color"`
	ModKey         string           `koanf:"mod-key"`
	BorderWidth    uint32           `koanf:"border-width"`
	Keybinds       []Keybind        `koanf:"keybinds"`
	AutoFullscreen bool				`koanf:"auto-fullscreen"`
}

type Keybind struct {
	// keycode, the letter of the key, if shift should be pressed, command (can be empty), role in wm (can be empty)
	Keycode uint32
	Key     string `koanf:"key"`
	Shift   bool   `koanf:"shift"`
	Exec    string `koanf:"exec"`
	Role    string `koanf:"role"`
}

// where a window is on a layout (dynamic by using percentages)
type LayoutWindow struct {
	WidthPercentage  float64 `koanf:"width"`
	HeightPercentage float64 `koanf:"height"`
	XPercentage      float64 `koanf:"x"`
	YPercentage      float64 `koanf:"y"`
}

// a tiling layout of windows
type Layout struct {
	Windows []LayoutWindow `koanf:"windows"`
}

type RLayoutWindow struct{
	Width, Height, X, Y uint16
}

type ResizeLayout struct{
	Windows []RLayoutWindow
}

// basic window struct
type Window struct {
	id            xproto.Window
	X, Y          int
	Width, Height int
	Fullscreen    bool
	Client        xproto.Window
}

// an area on the screen
type Space struct {
	X, Y          int
	Width, Height int
}

// a map from client windows to the frame, the reverse of that, window IDs to windows, and if that workspace is tiling or not (incase it needs to update to sync with the main wm)
type Workspace struct {
	tiling        bool
	layoutIndex   int
	detachTiling  bool
	windowList    []*Window
	resized       bool
	resizedLayout ResizeLayout
}

// the connection, root window, width and height of screen, workspaces, the current workspace index, the current workspace, atoms for EMWH, if the wm is tiling, the space for tiling windows to be, the different tiling layouts, the wm condig, the mod key
type WindowManager struct {
	conn           *xgb.Conn
	root           xproto.Window
	width, height  int
	workspaces     []Workspace
	workspaceIndex int
	currWorkspace  *Workspace
	atoms          map[string]xproto.Atom
	tiling         bool
	tilingspace    Space
	config         Config
	mod            uint16
	windows        map[xproto.Window]*Window
	layoutIndex    int
}

func (wm *WindowManager) cursor() {
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

// read and create config, if certain values, aren't provided, use the defualt values
func createConfig(f koanf.Provider) Config {
	// Set defaults manually
	cfg := Config{
		Gap:            6,
		OuterGap:       0,
		BorderWidth:    3,
		ModKey:         "Mod1",
		BorderUnactive: 0x8bd5ca,
		BorderActive:   0xa6da95,
		Keybinds:       []Keybind{},
		Layouts:        createLayouts(),
		StartTiling:    false,
		AutoFullscreen: false,
	}

	// Load the config file
	if err := k.Load(f, yaml.Parser()); err == nil {
		// Unmarshal — existing keys override the defaults
		k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "koanf", FlatPaths: false})

	} else {
		slog.Warn("couldn't load config, using defaults")
		exec.Command("notify-send", "'error in doWM config, using defaults'").Start()
	}

	fmt.Println(cfg.Layouts)
	return cfg
}

// create the X connection and get the root window, create workspaces and create window manager struct
func Create() (*WindowManager, error) {
	// establish connection
	X, err := xgb.NewConn()
	if err != nil {
		slog.Error("Couldn't open X display")
		return nil, fmt.Errorf("Couldn't open X display")
	}

	// get xgbutil connection aswell for keybinds
	XUtil, err = xgbutil.NewConnXgb(X)
	if err != nil {
		return nil, fmt.Errorf("couldn't create xgbutil connection: %w", err)
	}

	keybind.Initialize(XUtil)

	// get root and dimensions of screen
	setup := xproto.Setup(X)
	screen := setup.DefaultScreen(X)

	root := screen.Root

	dimensions, err := xproto.GetGeometry(X, xproto.Drawable(root)).Reply()

	if err != nil {
		return nil, fmt.Errorf("couldn't get screen dimensions: %w", err)
	}

	// create workspaces
	workspaces := make([]Workspace, 10)
	for i := range workspaces {
		workspaces[i] = Workspace{
			windowList:    []*Window{},
			tiling:        false,
			detachTiling:  false,
			layoutIndex:   0,
			resized: 	   false,
			resizedLayout: ResizeLayout{},
		}
	}

	// return the window manager struct
	return &WindowManager{
		conn:           X,
		root:           root,
		width:          int(dimensions.Width),
		height:         int(dimensions.Height),
		workspaces:     workspaces,
		currWorkspace:  &workspaces[0],
		workspaceIndex: 0,
		atoms:          map[string]xproto.Atom{},
		tiling:         false,
		windows:        map[xproto.Window]*Window{},
		layoutIndex:    0,
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
		slog.Error("failed to get modifier mapping: %v","error: " ,err)
	}

	// Each modifier (Shift, Lock, Control, Mod1-Mod5) has modMap.KeycodesPerModifier keycodes
	for modIndex := 0; modIndex < 8; modIndex++ {
		for i := 0; i < int(modMap.KeycodesPerModifier); i++ {
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
		offset := int(kc - firstKeycode) * int(keymap.KeysymsPerKeycode)
		for i := 0; i < int(keymap.KeysymsPerKeycode); i++ {
			if keymap.Keysyms[offset+i] == targetKeysym {
				return kc
			}
		}
	}
	return 0
}



// gets keycode of key and sets it, then tells the X server to notify us when this keybind is pressed
func (wm *WindowManager) createKeybind(kb *Keybind) Keybind {
	code := keybind.StrToKeycodes(XUtil, kb.Key)
	if len(code) < 1 {
		return Keybind{
			Keycode: 0,
			Key:     "",
			Shift:   false,
			Exec:    "",
		}
	}
	KeyCode := code[0]
	kb.Keycode = uint32(KeyCode)
	Mask := wm.mod
	if kb.Shift {
		Mask = Mask | xproto.ModMaskShift
	}
	err := xproto.GrabKeyChecked(wm.conn, true, wm.root, Mask, KeyCode, xproto.GrabModeAsync, xproto.GrabModeAsync).Check()
	err = xproto.GrabKeyChecked(wm.conn, true, wm.root, Mask | xproto.ModMaskLock, KeyCode, xproto.GrabModeAsync, xproto.GrabModeAsync).Check()
	numlock := getNumLockMask(wm.conn)
	if numlock!=wm.mod{
		err = xproto.GrabKeyChecked(wm.conn, true, wm.root, Mask | numlock, KeyCode, xproto.GrabModeAsync, xproto.GrabModeAsync).Check()
	}

	if err != nil {
		slog.Error("couldn't create keybind", "error:", err)
	}

	return *kb
}

func (wm *WindowManager) reload(focused xproto.ButtonPressEvent) {
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
	for i, kb := range wm.config.Keybinds {
		wm.config.Keybinds[i] = wm.createKeybind(&kb)
	}

	// workspace keybinds, ik not very idiomatic but its fine :)
	wm.config.Keybinds = append(wm.config.Keybinds, []Keybind{
		wm.createKeybind(&Keybind{Key: "0", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "1", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "2", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "3", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "4", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "5", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "6", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "7", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "8", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "9", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "0", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "1", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "2", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "3", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "4", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "5", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "6", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "7", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "8", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "9", Shift: true, Keycode: 0}),
	}...)

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
			err := xproto.ConfigureWindowChecked(wm.conn, window, xproto.ConfigWindowBorderWidth, []uint32{wm.config.BorderWidth}).Check()
			if err != nil {
				slog.Error("couldn't set border width", "error", err)
			}

			// Set border color
			err = xproto.ChangeWindowAttributesChecked(wm.conn, window, xproto.CwBorderPixel, []uint32{col}).Check()
			if err != nil {
				slog.Error("couldn't set border color", "error", err)
			}
		}
	}

	wm.fitToLayout()
}

func (wm *WindowManager) pointerToWindow(window xproto.Window) error {
	geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(window)).Reply()
	if err != nil {
		return err
	}

	trans, err := xproto.TranslateCoordinates(wm.conn, window, xproto.Setup(wm.conn).DefaultScreen(wm.conn).Root, 0, 0).Reply()
	if err != nil {
		return err
	}

	x := int16(trans.DstX) + int16(geom.Width)/2
	y := int16(trans.DstY) + int16(geom.Height)/2

	return xproto.WarpPointerChecked(wm.conn, 0, xproto.Setup(wm.conn).DefaultScreen(wm.conn).Root, 0, 0, 0, 0, x, y).Check()
}

func (wm *WindowManager) Run() {
	fmt.Println("window manager up and running")

	// get autostart
	user, err := user.Current()
	if err == nil {
		scriptPath := filepath.Join(user.HomeDir, ".config", "doWM", "autostart.sh")

		if fileExists(scriptPath) {
			fmt.Println("autostart exists..., running")
			exec.Command(scriptPath).Start()
		}
	}

	// basically asks the X server for WM access
	err = xproto.ChangeWindowAttributesChecked(
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
			slog.Error("other window manager running on display")
			return
		}
	}

	//wm.cursor()

	// retrieve config and set values
	home, _ := os.UserHomeDir()
	f := file.Provider(filepath.Join(home, ".config", "doWM", "doWM.yml"))
	cfg := createConfig(f)
	wm.config = cfg
	if wm.config.StartTiling {
		wm.toggleTiling()
		wm.fitToLayout()
	}
	//TODO: make auto-reload

	// for things like polybar, to show workspaces
	wm.broadcastWorkspace(0)
	wm.broadcastWorkspaceCount()

	// grab the server whilst we manage pre-exisiting windows
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
		slog.Error("tree root not equal to window manager root", "error:", err.Error())
		return
	}

	for _, window := range TopLevelWindows {
		if !shouldIgnoreWindow(wm.conn, window) {
			wm.Frame(window, true)
		}
	}

	err = xproto.UngrabServerChecked(wm.conn).Check()

	if err != nil {
		slog.Error("couldn't ungrab server", "error:", err.Error())
		return
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
	for i, kb := range wm.config.Keybinds {
		wm.config.Keybinds[i] = wm.createKeybind(&kb)
	}

	// workspace keybinds, ik not very idiomatic but its fine :)
	wm.config.Keybinds = append(wm.config.Keybinds, []Keybind{
		wm.createKeybind(&Keybind{Key: "0", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "1", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "2", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "3", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "4", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "5", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "6", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "7", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "8", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "9", Shift: false, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "0", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "1", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "2", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "3", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "4", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "5", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "6", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "7", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "8", Shift: true, Keycode: 0}),
		wm.createKeybind(&Keybind{Key: "9", Shift: true, Keycode: 0}),
	}...)

	fmt.Println(wm.config.Keybinds)

	// Only grab with Mod + left or right click (not plain Button1)
	err = xproto.GrabButtonChecked(wm.conn, false, wm.root, uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskPointerMotion), xproto.GrabModeAsync, xproto.GrabModeAsync, xproto.WindowNone, xproto.AtomNone, xproto.ButtonIndex1, mMask).Check()

	err = xproto.GrabButtonChecked(wm.conn, false, wm.root, uint16(xproto.EventMaskButtonPress|xproto.EventMaskButtonRelease|xproto.EventMaskPointerMotion), xproto.GrabModeAsync, xproto.GrabModeAsync, xproto.WindowNone, xproto.AtomNone, xproto.ButtonIndex3, mMask).Check()

	if err != nil {
		slog.Error("couldn't grab window+c key", "error:", err.Error())
	}

	// for moving and resizing, basically the window that will be moved/resized
	var start xproto.ButtonPressEvent
	var attr *xproto.GetGeometryReply

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
			slog.Error("event error", "error:", err.Error())
			continue
		}
		if event == nil {
			continue
		}
		if len(wm.currWorkspace.windowList) == 0 {
			xproto.SetInputFocusChecked(wm.conn, xproto.InputFocusPointerRoot, wm.root, xproto.TimeCurrentTime).Check()
		}
		switch event.(type) {
		case xproto.ButtonPressEvent:
			// set values on current window, used later with moving and resizing
			ev := event.(xproto.ButtonPressEvent)
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
			// if we don't have the mouse down, we don't want to move or resize
			if (wm.tiling){
				ev := event.(xproto.ButtonReleaseEvent)
				found := false
				for _, window := range wm.currWorkspace.windowList{
					geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(window.id)).Reply()
					if err != nil {
						continue
					}
					fmt.Println("id", window.id, "mouse X:", ev.EventX, "mouse Y:", ev.EventY, "win X:" , geom.X, "win Y:", geom.Y, "win width", geom.Width, "win height", geom.Height ,"RELEASE")
					if window.id != ev.Child&& ev.EventX < geom.X+int16(geom.Width) && ev.EventX > int16(geom.X)&&ev.EventY<geom.Y+int16(geom.Height)&&ev.EventY>int16(geom.Y){
						fmt.Println("MOVING", ev.Child, window.id)
						swapWindowsId(&wm.currWorkspace.windowList, ev.Child, window.id)
						wm.fitToLayout()
						found = true
						break
					}
				}
				if !found{
					wm.fitToLayout()
				}
			}
			start.Child = 0
			xproto.AllowEvents(wm.conn, xproto.AllowReplayPointer, xproto.TimeCurrentTime)
		case xproto.MotionNotifyEvent:
			ev := event.(xproto.MotionNotifyEvent)
			// if we have the mouse down and we are holding the mod key, and if we are not tiling and the window is not full screen then do some simple maths to move and resize
			focusWindow(wm.conn, ev.Child)
			if start.Child != 0 && ev.State&mMask != 0 {
				if (wm.windows[start.Child] != nil && wm.windows[start.Child].Fullscreen) {
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
					if wm.currWorkspace.tiling { break }
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
			break
		case xproto.ConfigureRequestEvent:
			wm.OnConfigureRequest(event.(xproto.ConfigureRequestEvent))
			break
		case xproto.MapRequestEvent:
			fmt.Println("MapRequest")
			wm.OnMapRequest(event.(xproto.MapRequestEvent))
			break
		case xproto.ReparentNotifyEvent:
			fmt.Println("reparent notify")
			break
		case xproto.MapNotifyEvent:
			fmt.Println("MapNotify")
			break
		case xproto.ConfigureNotifyEvent:
			fmt.Println("ConfigureNotify")
			break
		case xproto.UnmapNotifyEvent:
			fmt.Println("unmapping")
			wm.OnUnmapNotify(event.(xproto.UnmapNotifyEvent))
			break
		case xproto.DestroyNotifyEvent:
			fmt.Println("DestroyNotify")
			ev := event.(xproto.DestroyNotifyEvent)
			fmt.Println("Window:")
			fmt.Println(ev.Window)
			fmt.Println("Event:")
			fmt.Println(ev.Event)
			// if the destroy notify has come through but we haven't registered any kind of deletion then handle it
			if _, ok := wm.windows[ev.Window]; ok {
				wm.remDestroyedWin(ev.Window)
			}
			fmt.Println("finished destroying")
			break
		case xproto.EnterNotifyEvent:
			// when we enter the frame, change the border color
			fmt.Println("EnterNotify")
			ev := event.(xproto.EnterNotifyEvent)
			fmt.Println(ev.Event)
			wm.OnEnterNotify(event.(xproto.EnterNotifyEvent))
			break
		case xproto.LeaveNotifyEvent:
			// when we leave the frame, change the border color
			fmt.Println("LeaveNotify")
			ev := event.(xproto.LeaveNotifyEvent)
			fmt.Println(ev.Event)
			wm.OnLeaveNotify(event.(xproto.LeaveNotifyEvent))
			break
		case xproto.KeyPressEvent:
			fmt.Println("keyPress")
			ev := event.(xproto.KeyPressEvent)
			// if mod key is down
			if ev.State&mMask != 0 {
				// go through keybinds if the keybind matches up to the current event then continue
				for _, kb := range wm.config.Keybinds {
					if ev.Detail == xproto.Keycode(kb.Keycode) && (ev.State&(mMask|xproto.ModMaskShift) == (mMask | xproto.ModMaskShift) == kb.Shift) {
						// if it has an exec then just execute it
						if kb.Exec != "" {
							fmt.Println("executing:", kb.Exec)
							runCommand(kb.Exec)
							fmt.Println("excuted")
						}
						switch kb.Role {
						case "resize-x-scale-up":
							if wm.currWorkspace.tiling==true {
								wm.pointerToWindow(ev.Child)
								if !wm.resizeTiledX(true, ev){
									break
								}
							}else{
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil{
									break
								}
								xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowWidth, []uint32{uint32(geom.Width+uint16(wm.config.Resize))})
								wm.pointerToWindow(ev.Child)
							}
						case "resize-x-scale-down":
							if wm.currWorkspace.tiling==true {
								wm.pointerToWindow(ev.Child)
								if !wm.resizeTiledX(false, ev){
									break
								}
							}else{
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil{
									break
								}
								if geom.Width>10{
									xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowWidth, []uint32{uint32(geom.Width-uint16(wm.config.Resize))})
									wm.pointerToWindow(ev.Child)
								}
							}
						case "resize-y-scale-up":
							if wm.currWorkspace.tiling==true {
								wm.pointerToWindow(ev.Child)
								if !wm.resizeTiledY(true, ev){
									break
								}
							}else{
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil{
									break
								}
								xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowHeight, []uint32{uint32(geom.Height+uint16(wm.config.Resize))})
								wm.pointerToWindow(ev.Child)
							}
						case "resize-y-scale-down":
							if wm.currWorkspace.tiling==true {
								wm.pointerToWindow(ev.Child)
								if !wm.resizeTiledY(false, ev){
									break
								}
							}else{
								if wm.currWorkspace.tiling==true {break}
								geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
								if err != nil{
									break
								}
								if geom.Height>10{
									xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowHeight, []uint32{uint32(geom.Height-uint16(wm.config.Resize))})							
									wm.pointerToWindow(ev.Child)
								}
							}
						case "move-x-right":
							if wm.currWorkspace.tiling==true {break}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil{
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowX, []uint32{uint32(geom.X+10)})
							wm.pointerToWindow(ev.Child)
						case "move-x-left":
							if wm.currWorkspace.tiling==true {break}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil{
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowX, []uint32{uint32(geom.X-10)})
							wm.pointerToWindow(ev.Child)
						case "move-y-up":
							if wm.currWorkspace.tiling==true {break}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil{
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowY, []uint32{uint32(geom.Y-10)})
							wm.pointerToWindow(ev.Child)
						case "move-y-down":
							if wm.currWorkspace.tiling==true {break}
							geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
							if err != nil{
								break
							}
							xproto.ConfigureWindowChecked(wm.conn, ev.Child, xproto.ConfigWindowY, []uint32{uint32(geom.Y+10)})
							wm.pointerToWindow(ev.Child)
						case "quit":
							if _, ok := wm.windows[ev.Child]; ok {
								// EMWH way of politely saying to destroy
								wm.SendWmDelete(wm.conn, wm.windows[ev.Child].id)
								fmt.Println("closing window:", wm.windows[ev.Child].id, "frame:", ev.Child)
							}
							break
						case "force-quit":
							// force close
							err := xproto.DestroyWindowChecked(wm.conn, wm.windows[ev.Child].id).Check()
							if err != nil {
								fmt.Println("Couldn't force destroy:", err)
							}
							break
						case "toggle-tiling":
							wm.toggleTiling()
							break
						case "detach-tiling":
							if wm.currWorkspace.detachTiling {
								wm.currWorkspace.detachTiling = false
								if wm.tiling && !wm.currWorkspace.tiling {
									wm.enableTiling()
								} else if !wm.tiling && wm.currWorkspace.tiling {
									wm.disableTiling()
								}
							} else {
								wm.currWorkspace.detachTiling = true
							}
							wm.fitToLayout()
						case "toggle-fullscreen":
							wm.toggleFullScreen(ev.Child)
						case "swap-window-left":
							fmt.Println("swap left")
							if wm.currWorkspace.tiling {
								currWindow := ev.Child
							swapLeft:
								for i := range wm.currWorkspace.windowList {
									if currWindow == wm.currWorkspace.windowList[i].id {
										if i == 0 {
											swapWindows(&wm.currWorkspace.windowList, i, len(wm.currWorkspace.windowList)-1)
										} else {
											swapWindows(&wm.currWorkspace.windowList, i, i-1)
										}
										wm.fitToLayout()
										err := wm.pointerToWindow(currWindow)
										if err != nil {
											slog.Error("couldn't move pointer to window", "error:", err)
										}
										break swapLeft
									}
								}
							}
						case "swap-window-right":
							fmt.Println("swap right")
							if wm.currWorkspace.tiling {
								currWindow := ev.Child
							swapRight:
								for i := range wm.currWorkspace.windowList {
									if currWindow == wm.currWorkspace.windowList[i].id {
										if i == len(wm.currWorkspace.windowList)-1 {
											swapWindows(&wm.currWorkspace.windowList, i, 0)
										} else {
											swapWindows(&wm.currWorkspace.windowList, i, i+1)
										}
										wm.fitToLayout()
										wm.pointerToWindow(currWindow)
										break swapRight
									}
								}
							}
						case "focus-window-right":
							if wm.currWorkspace.tiling {
								currWindow := ev.Child
							focusRight:
								for i := range wm.currWorkspace.windowList {
									if currWindow == wm.currWorkspace.windowList[i].id {
										if i == len(wm.currWorkspace.windowList)-1 {
											wm.pointerToWindow(wm.currWorkspace.windowList[0].id)
										} else {
											wm.pointerToWindow(wm.currWorkspace.windowList[i+1].id)
										}
										break focusRight
									}
								}
							}
						case "focus-window-left":
							if wm.currWorkspace.tiling {
								currWindow := ev.Child
							focusLeft:
								for i := range wm.currWorkspace.windowList {
									if currWindow == wm.currWorkspace.windowList[i].id {
										if i == 0 {
											wm.pointerToWindow(wm.currWorkspace.windowList[len(wm.currWorkspace.windowList)-1].id)
										} else {
											wm.pointerToWindow(wm.currWorkspace.windowList[i-1].id)
										}
										break focusLeft
									}
								}
							}
						case "reload-config":
							f = file.Provider(filepath.Join(home, ".config", "doWM", "doWM.yml"))
							cfg := createConfig(f)
							wm.config = cfg
							wm.reload(start)
							mMask = wm.mod

						case "next-layout":
							windowNum := len(wm.currWorkspace.windowList)
							if windowNum < 1 {
								break
							}
							totalLen := len(wm.config.Layouts[windowNum]) - 1
							if wm.currWorkspace.layoutIndex == totalLen {
								wm.currWorkspace.layoutIndex = 0
							} else {
								wm.currWorkspace.layoutIndex++
							}
							wm.layoutIndex = wm.currWorkspace.layoutIndex
							wm.currWorkspace.resized = false
							wm.currWorkspace.resizedLayout = ResizeLayout{}
							wm.fitToLayout()
						case "increase-gap":
							wm.config.Gap++
							wm.fitToLayout()
						case "decrease-gap":
							if wm.config.Gap > 0 {
								wm.config.Gap--
							}
							wm.fitToLayout()
						}
						switch kb.Key {
						case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
							// if shift is pressed we want to move the window to the next workspace, so delete it from the record of the current workspace so when they unmap all the other windows (giving the illusion of changing workspace) this one stays then afterwards reparent it to the workspace that has been changed to
							w := ev.Child
							var window Window
							var shiftok bool=false
							if kb.Shift {
								if _, ok := wm.windows[w]; ok{ 
									shiftok = ok
									window = *wm.windows[w]
									fmt.Println("moving window")
									xproto.ConfigureWindow(
										wm.conn,
										w,
										xproto.ConfigWindowStackMode,
										[]uint32{xproto.StackModeAbove},
									)
									remove(&wm.currWorkspace.windowList, w)
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
							if kb.Shift && shiftok{
								wm.currWorkspace.windowList = append(wm.currWorkspace.windowList, &window)
								wm.setWindowDesktop(w, uint32(wm.workspaceIndex))
							}
							wm.fitToLayout()

							break
						}
					}

				}
			}
			break

		case xproto.ClientMessageEvent:
			fmt.Println("client message")
			ev := event.(xproto.ClientMessageEvent)

			atomName, _ := xproto.GetAtomName(wm.conn, xproto.Atom(ev.Type)).Reply()
			fmt.Println("ClientMessage atom:", atomName.Name)

			if atomName.Name == "_NET_CURRENT_DESKTOP" {
				desktop := int(ev.Data.Data32[0])
				wm.switchWorkspace(desktop)
			}

			if atomName.Name == "_NET_WM_STATE"&& wm.config.AutoFullscreen {
				fullscreenAtom, _ := wm.internAtom("_NET_WM_STATE_FULLSCREEN")
				maxHorzAtom, _ := wm.internAtom("_NET_WM_STATE_MAXIMIZED_HORZ")
				maxVertAtom, _ := wm.internAtom("_NET_WM_STATE_MAXIMIZED_VERT")
				

				action := ev.Data.Data32[0] // 0 = remove, 1 = add, 2 = toggle
				prop1 := ev.Data.Data32[1]
				prop2 := ev.Data.Data32[2]

				if _, ok := wm.windows[ev.Window]; !ok{
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
			fmt.Println(event.Bytes())

		}
	}
}

func (wm *WindowManager) resizeTiledX(increase bool, ev xproto.KeyPressEvent) bool{
	geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
	if err != nil{
		return false
	}
	X := uint16(geom.X - int16(wm.config.Gap)-int16(wm.tilingspace.X))
	W := geom.Width + uint16(wm.config.Gap*2)
	if math.Abs(float64(uint16(wm.tilingspace.X+wm.tilingspace.Width)-(X+W)))<=10{
		return false
	}

	var resizeLayout ResizeLayout
	var ok bool = true
	for _, win := range wm.currWorkspace.windowList{
		geomwin, err := xproto.GetGeometry(wm.conn, xproto.Drawable(win.id)).Reply()
		if err != nil {continue}
		winX := uint16(geomwin.X - int16(wm.config.Gap)-int16(wm.tilingspace.X))
		winY := uint16(geomwin.Y - int16(wm.config.Gap)-int16(wm.tilingspace.Y))
		winH := geomwin.Height + uint16(wm.config.Gap*2)
		winW := geomwin.Width + uint16(wm.config.Gap*2)
		// if diff between ends of windows it less than five, they are same column
		if math.Abs( float64( (X+W)-(winX+winW) ) ) <= 10{
			if increase{
				winW+=uint16(wm.config.Resize)
			}else{
				winW-=uint16(wm.config.Resize)
			}
			fmt.Println(winW)		
		}else if math.Abs( float64( int(winX)-(int(X)+int(W)) ) ) <= 10 {
			if increase{
				winX+=uint16(wm.config.Resize)
				winW-=uint16(wm.config.Resize)
				if winW < 50{
					ok = false
					break
				}
			}else{
				winX -= uint16(wm.config.Resize)
				winW += uint16(wm.config.Resize)
			}
		}
		
		fmt.Println(winX, winY, winW, winH)
		resizeLayout.Windows = append(resizeLayout.Windows, RLayoutWindow{
			X: winX,
			Y: winY,
			Width: winW,
			Height: winH,
		})

	}

	if ok{
		wm.currWorkspace.resized = true
		wm.currWorkspace.resizedLayout = resizeLayout
		wm.fitToLayout()
		return true
	}else{
		return false
	}
}

func (wm *WindowManager) resizeTiledY(increase bool, ev xproto.KeyPressEvent) bool{
	geom, err := xproto.GetGeometry(wm.conn, xproto.Drawable(ev.Child)).Reply()
	if err != nil{
		return false
	}
	Y := uint16(geom.Y - int16(wm.config.Gap)-int16(wm.tilingspace.Y))
	H := geom.Height + uint16(wm.config.Gap*2)
	if math.Abs(float64(uint16(wm.tilingspace.X+wm.tilingspace.Height)-(Y+H)))<=10{
		return false
	}

	var resizeLayout ResizeLayout
	var ok bool = true
	for _, win := range wm.currWorkspace.windowList{
		geomwin, err := xproto.GetGeometry(wm.conn, xproto.Drawable(win.id)).Reply()
		if err != nil {continue}
		winX := uint16(geomwin.X - int16(wm.config.Gap)-int16(wm.tilingspace.X))
		winY := uint16(geomwin.Y - int16(wm.config.Gap)-int16(wm.tilingspace.Y))
		winH := geomwin.Height + uint16(wm.config.Gap*2)
		winW := geomwin.Width + uint16(wm.config.Gap*2)
		// if diff between ends of windows it less than five, they are same column
		if math.Abs( float64( (int(Y)+int(H))-(int(winY)+int(winH)) ) ) <= 10{
			if increase{
				winH+=uint16(wm.config.Resize)
			}else{
				winH-=uint16(wm.config.Resize)
			}
			fmt.Println(winW)		
		}else if math.Abs( float64( int(winY)-(int(Y)+int(H)) ) ) <= 10 {
			if increase{
				winY+=uint16(wm.config.Resize)
				winH-=uint16(wm.config.Resize)
				if winH < 50{
					ok = false
					break
				}
			}else{
				winY -= uint16(wm.config.Resize)
				winH += uint16(wm.config.Resize)

			}
		}
		
		fmt.Println(winX, winY, winW, winH)
		resizeLayout.Windows = append(resizeLayout.Windows, RLayoutWindow{
			X: winX,
			Y: winY,
			Width: winW,
			Height: winH,
		})

	}

	if ok{
		wm.currWorkspace.resized = true
		wm.currWorkspace.resizedLayout = resizeLayout
		wm.fitToLayout()
		return true
	}else{
		return false
	}
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

	var atoms []xproto.Atom
	for _, name := range atomNames {
		atom, err := xproto.InternAtom(wm.conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			slog.Error("intern atom", "name", name, "err", err)
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
		slog.Error("could not set _NET_SUPPORTED", "err", err)
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

func swapWindowsId(arr *[]*Window, first xproto.Window, last xproto.Window){
	var res1 int
	var res2 int
	for i, win := range (*arr){
		if win.id == first{ res1 = i 
		}else if win.id == last{ res2 =i }
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
		slog.Error("parse error:", "error:", err)
		return
	}
	if len(args) == 0 {
		return
	}
	if len(args) < 2 {
		cmd := exec.Command(args[0])
		cmd.Start()
		return
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Start()
}
func (wm *WindowManager) getBar(vals []byte) (int, int, int, int) {
	// calculates where the bar is (more explanitary in createTilingSpace)

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
	// look at all windows and if it has the property _NET_WM_STRUT_PARTIAL (what most bars have) it means that it should be worked around
	windows, _ := xproto.QueryTree(wm.conn, wm.root).Reply()
	X := 0
	Y := 0
	width := wm.width
	height := wm.height

	for _, window := range windows.Children {
		attributes, err := xproto.GetWindowAttributes(wm.conn, window).Reply()
		if err != nil {
			continue
		}
		if attributes.MapState == xproto.MapStateViewable {
			atom := wm.atoms["_NET_WM_STRUT_PARTIAL"]
			prop, err := xproto.GetProperty(wm.conn, false, window, atom, xproto.AtomCardinal, 0, 12).Reply()

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
			width = wm.width - left - right
			height = wm.height - top - bottom

			// TODO: support multiple bars
			break
		}
	}

	fmt.Println("tiling container:", "X:", X, "Y:", Y, "Width:", width, "Height:", height)
	wm.tilingspace = Space{
		X:      X + int(wm.config.OuterGap),
		Y:      Y + int(wm.config.OuterGap),
		Width:  (width - 6) - (int(wm.config.OuterGap) * 2),
		Height: (height - 6) - (int(wm.config.OuterGap) * 2),
	}
}

func (wm *WindowManager) fitToLayout() {
	if !wm.currWorkspace.tiling {
		return
	}
	// if there are more than 4 windows then just don't do it

	windowNum := len(wm.currWorkspace.windowList)

	if _, ok := wm.config.Layouts[windowNum]; !ok {
		return
	}

	if len(wm.config.Layouts[windowNum])-1 < wm.layoutIndex && len(wm.config.Layouts[windowNum]) > 0 {
		wm.currWorkspace.layoutIndex = 0
		wm.layoutIndex = 0
	}

	if windowNum > len(wm.config.Layouts) || windowNum < 1 || windowNum > len(wm.config.Layouts[windowNum][wm.layoutIndex].Windows) {
		fmt.Println("too many or too few windows to fit to layout in workspace", wm.workspaceIndex+1)
		return
	}
	wm.createTilingSpace()
	layout := wm.config.Layouts[windowNum][wm.layoutIndex]
	if wm.currWorkspace.resized && len(wm.currWorkspace.resizedLayout.Windows)!=windowNum{
		wm.currWorkspace.resized = false
		wm.currWorkspace.resizedLayout = ResizeLayout{}
	}
	fmt.Println("fit to layout")
	fmt.Println(wm.currWorkspace.windowList)
	//fmt.Println(wm.currWorkspace.windows)
	//fmt.Println(len(wm.currWorkspace.windows))
	// for each window put it in its place and size specified by that layout
	fullscreen := []xproto.Window{}
	for i, WindowData := range wm.currWorkspace.windowList {
		fmt.Println(WindowData)
		if WindowData.Fullscreen {
			fullscreen = append(fullscreen, WindowData.id)
			continue
		}
		if wm.currWorkspace.resized{
			layoutWindow := wm.currWorkspace.resizedLayout.Windows[i]
			X := uint32(layoutWindow.X) + wm.config.Gap + uint32(wm.tilingspace.X)
			Y := uint32(layoutWindow.Y) + wm.config.Gap + uint32(wm.tilingspace.Y)
			Width := uint32(layoutWindow.Width) - (wm.config.Gap*2)
			Height := uint32(layoutWindow.Height) - (wm.config.Gap*2)
			fmt.Println("window:", WindowData.id, "X:", X, "rounded:", "Y:", Y, "Width:", Width, "Height:", Height)
			wm.configureWindow(WindowData.id, int(X), int(Y), int(Width), int(Height))
		}else{
			layoutWindow := layout.Windows[i]
			// because we use percentages we have to times the width and height of the tiling space to get the raw value, it is simple maths to do the gap, I shouldn't have to explain it (since I am 12 I would expect u to know XD)
			X := wm.tilingspace.X + int((float64(wm.tilingspace.Width) * layoutWindow.XPercentage)) + int(wm.config.Gap)
			Y := wm.tilingspace.Y + int((float64(wm.tilingspace.Height) * layoutWindow.YPercentage)) + int(wm.config.Gap)
			Width := (float64(wm.tilingspace.Width) * layoutWindow.WidthPercentage) - float64(wm.config.Gap*2)
			Height := (float64(wm.tilingspace.Height) * layoutWindow.HeightPercentage) - float64(wm.config.Gap*2)
			fmt.Println("window:", WindowData.id, "X:", X, "rounded:", int(math.Round(Width)), "Y:", Y, "Width:", Width, "Height:", Height)
			wm.configureWindow(WindowData.id, X, Y, int(math.Round(Width)), int(math.Round(Height)))
		}
	}
	if len(fullscreen) > 0 {
		for _, win := range fullscreen {
			xproto.ConfigureWindow(wm.conn, win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
			wm.fullscreen(wm.windows[win], win)
		}
	}
}

func (wm *WindowManager) configureWindow(Frame xproto.Window, X, Y, Width, Height int) {
	// configure the window to how it wants to be
	_ = xproto.ConfigureWindowChecked(wm.conn, Frame, xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{
		uint32(X), uint32(Y), uint32(Width), uint32(Height),
	}).Check()

}

func (wm *WindowManager) toggleTiling() {
	if !wm.currWorkspace.detachTiling {
		if !wm.tiling {
			wm.tiling = true
			wm.enableTiling()
		} else {
			wm.tiling = false
			wm.disableTiling()
		}
	} else {
		if !wm.currWorkspace.tiling {
			wm.enableTiling()
		} else {
			wm.disableTiling()
		}
	}
}

func (wm *WindowManager) disableTiling() {
	wm.currWorkspace.tiling = false
	fmt.Println("DISABLED TILING")
	// restore windows to there previous state (before tiling)
	for _, window := range wm.currWorkspace.windowList {

		wm.configureWindow(window.id, window.X, window.Y, window.Width, window.Height)
	}
	wm.setNetWorkArea()
}

func (wm *WindowManager) enableTiling() {
	wm.currWorkspace.tiling = true
	// make sure no windows are fullscreened and that there state is saved (so it can be restored later if/when the user disables tiling)
	for i, window := range wm.currWorkspace.windowList {
		fmt.Println(window.id)
		attr, _ := xproto.GetGeometry(wm.conn, xproto.Drawable(window.id)).Reply()
		wm.currWorkspace.windowList[i] = &Window{
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

func (wm *WindowManager) toggleFullScreen(Child xproto.Window) {
	win := wm.windows[Child]
	if win != nil {
		if win.Fullscreen {
			wm.disableFullscreen(win, Child)
		} else {
			wm.fullscreen(win, Child)
		}
	}
}

func (wm *WindowManager) disableFullscreen(win *Window, Child xproto.Window) {
	fmt.Println("DISABLING FULL SCREEN")
	wm.windows[Child].Fullscreen = false
	for i, window := range wm.currWorkspace.windowList {
		if window.id == Child {
			wm.currWorkspace.windowList[i].Fullscreen = false
		}
		fmt.Println(window.Fullscreen)
	}
	// set the frame back to what it used to be same with the client, but sort out tiling layout anyway just in case
	err := xproto.ConfigureWindowChecked(
		wm.conn,
		Child,
		xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight|xproto.ConfigWindowBorderWidth,
		[]uint32{uint32(win.X), uint32(win.Y), uint32(win.Width), uint32(win.Height), wm.config.BorderWidth},
	).Check()
	if err != nil {
		slog.Error("couldn't un fullscreen window", "error: ", err)
	}
	wm.fitToLayout()
}

func (wm *WindowManager) fullscreen(win *Window, Child xproto.Window) {
	// set window state so it can be restored later then configure window to be full width and height, sam with client, also take away border
	wm.windows[Child].Fullscreen = true
	for i, window := range wm.currWorkspace.windowList {
		if window.id == Child {
			wm.currWorkspace.windowList[i].Fullscreen = true
		}
	}
	xproto.ConfigureWindow(wm.conn, Child, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})
	attr, _ := xproto.GetGeometry(wm.conn, xproto.Drawable(Child)).Reply()
	win = wm.windows[Child]
	win.X = int(attr.X)
	win.Y = int(attr.Y)
	win.Width = int(attr.Width)
	win.Height = int(attr.Height)
	err := xproto.ConfigureWindowChecked(
		wm.conn,
		Child,
		xproto.ConfigWindowX|xproto.ConfigWindowY|
			xproto.ConfigWindowWidth|xproto.ConfigWindowHeight|xproto.ConfigWindowBorderWidth,
		[]uint32{0, 0, uint32(wm.width), uint32(wm.height), 0},
	).Check()
	if err != nil {
		slog.Error("couldn't fullscreen window", "error:", err)
	}
}

func (wm *WindowManager) broadcastWorkspaceCount() {
	// EMWH things for bars to show workspaces
	count := wm.workspaceIndex + 1
	otherCount := 0
	for i, workspace := range wm.workspaces {
		if len(workspace.windowList) > 0 {
			otherCount = i
		}
	}
	otherCount += 1
	if otherCount > count {
		count = otherCount
	}
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, uint32(count))

	netNumberAtom, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_NUMBER_OF_DESKTOPS")), "_NET_NUMBER_OF_DESKTOPS").Reply()
	cardinalAtom, _ := xproto.InternAtom(wm.conn, true, uint16(len("CARDINAL")), "CARDINAL").Reply()

	xproto.ChangePropertyChecked(
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

	netCurrentDesktopAtom, err := xproto.InternAtom(wm.conn, false, uint16(len("_NET_CURRENT_DESKTOP")), "_NET_CURRENT_DESKTOP").Reply()

	if err != nil {
		slog.Error("intern _NET_CURRENT_DESKTOP", "error:", err)
		return
	}

	cardinalAtom, err := xproto.InternAtom(wm.conn, true, uint16(len("CARDINAL")), "CARDINAL").Reply()
	if err != nil {
		slog.Error("intern CARDINAL", "error:", err)
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
		slog.Error("couldn't set _NET_CURRENT_DESKTOP", "error:", err)
	}

	wm.broadcastWorkspaceCount()
}

func (wm *WindowManager) switchWorkspace(workspace int) {
	if workspace > len(wm.workspaces) {
		return
	}

	if workspace == wm.workspaceIndex {
		return
	}

	// unmap all windows in current workspace
	for _, frame := range wm.currWorkspace.windowList {
		xproto.UnmapWindowChecked(wm.conn, frame.id)
	}

	// swap workspace
	wm.currWorkspace = &wm.workspaces[workspace]
	wm.workspaceIndex = workspace

	// map all the windows in the other workspace
	for _, frame := range wm.currWorkspace.windowList {
		xproto.MapWindowChecked(wm.conn, frame.id)
	}

	wm.conn.Sync()

	// update tiling
	if !wm.currWorkspace.detachTiling {
		if wm.tiling && !wm.currWorkspace.tiling {
			wm.enableTiling()
		} else if !wm.tiling && wm.currWorkspace.tiling {
			wm.disableTiling()
		}
	}
	wm.broadcastWorkspace(workspace)
	wm.layoutIndex = wm.currWorkspace.layoutIndex
}

func (wm *WindowManager) SendWmDelete(conn *xgb.Conn, window xproto.Window) error {
	// polite EMWH way of telling the window to delete itself
	wmProtocolsAtom, _ := xproto.InternAtom(conn, true, uint16(len("WM_PROTOCOLS")), "WM_PROTOCOLS").Reply()
	wmDeleteAtom, _ := xproto.InternAtom(conn, true, uint16(len("WM_DELETE_WINDOW")), "WM_DELETE_WINDOW").Reply()

	prop, err := xproto.GetProperty(conn, false, window, wmProtocolsAtom.Atom, xproto.AtomAtom, 0, (1<<32)-1).Reply()
	if err != nil || prop.Format != 32 {
		return fmt.Errorf("couldn't get WM_PROTOCOLS")
	}

	supportsDelete := false
	for i := 0; i < int(prop.ValueLen); i++ {
		atom := xgb.Get32(prop.Value[i*4:])
		if xproto.Atom(atom) == wmDeleteAtom.Atom {
			supportsDelete = true
			break
		}
	}

	if !supportsDelete {
		return fmt.Errorf("WM_DELETE_WINDOW not supported")
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

func (wm *WindowManager) OnLeaveNotify(event xproto.LeaveNotifyEvent) {
	// change border color when you leave a window
	Col := wm.config.BorderUnactive

	err := xproto.ChangeWindowAttributesChecked(
		wm.conn,
		event.Event,
		xproto.CwBackPixel|xproto.CwBorderPixel,
		[]uint32{
			Col, // background
			Col, // border color
		},
	).Check()
	if err != nil {
		slog.Error("couldn't remove focus from window", "error:", err)
	}
}

func setFrameWindowType(conn *xgb.Conn, win xproto.Window) {
	atomWindowType, _ := xproto.InternAtom(conn, true, uint16(len("_NET_WM_WINDOW_TYPE")), "_NET_WM_WINDOW_TYPE").Reply()
	atomNormal, _ := xproto.InternAtom(conn, true, uint16(len("_NET_WM_WINDOW_TYPE_NORMAL")), "_NET_WM_WINDOW_TYPE_NORMAL").Reply()

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
	atomActiveWin, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_ACTIVE_WINDOW")), "_NET_ACTIVE_WINDOW").Reply()

	// Convert uint32 to []byte
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, win)

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
	atomWorkArea, err := xproto.InternAtom(wm.conn, true, uint16(len("_NET_WORKAREA")), "_NET_WORKAREA").Reply()
	if err != nil {
		// handle error properly here
		return
	}

	buf := new(bytes.Buffer)

	spaceX, spaceY, spaceWidth, spaceHeight := wm.tilingspace.X, wm.tilingspace.Y, wm.tilingspace.Width, wm.tilingspace.Height

	for _, wksp := range wm.workspaces {
		if !wksp.tiling {
			_ = binary.Write(buf, binary.LittleEndian, uint32(0))
			_ = binary.Write(buf, binary.LittleEndian, uint32(0))
			_ = binary.Write(buf, binary.LittleEndian, uint32(wm.width))
			_ = binary.Write(buf, binary.LittleEndian, uint32(wm.height))
		} else {
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceX))
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceY))
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceWidth))
			_ = binary.Write(buf, binary.LittleEndian, uint32(spaceHeight))
		}
	}

	// Number of 32-bit CARDINAL values: 4 values per workspace
	numValues := uint32(4 * len(wm.workspaces))

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
		slog.Error("couldn't set the work area", "error:", err)
	}
}

func (wm *WindowManager) setNetClientList() {
	atomClientList, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_CLIENT_LIST")), "_NET_CLIENT_LIST").Reply()

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
func (wm *WindowManager) OnEnterNotify(event xproto.EnterNotifyEvent) {
	// set focus when we enter a window and change border color
	err := xproto.SetInputFocusChecked(wm.conn, xproto.InputFocusPointerRoot, event.Event, xproto.TimeCurrentTime).Check()
	Col := wm.config.BorderActive
	err = xproto.ChangeWindowAttributesChecked(
		wm.conn,
		event.Event,
		xproto.CwBackPixel|xproto.CwBorderPixel,
		[]uint32{
			Col, // background
			Col, // border color
		},
	).Check()
	if err != nil {
		slog.Error("couldn't set focus on window", "error:", err)
	}
	wm.setNetActiveWindow(event.Event)
}

func (wm *WindowManager) findWindow(window xproto.Window) (bool, int, xproto.Window) {
	fmt.Println("FINDING WINDOW", window)
	// look through all workspaces and windows to find a window (this is for if a window is deleted by a window from another workspace, we need to search for it)
	for i, workspace := range wm.workspaces {
		if i == wm.workspaceIndex {
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

func (wm *WindowManager) OnUnmapNotify(event xproto.UnmapNotifyEvent) {
	if event.Event == wm.root {
		slog.Info("Ignore UnmapNotify for reparented pre-existing window")
		fmt.Println(event.Window)
		return
	}


	var found bool = false
	for _, win := range wm.currWorkspace.windowList{
		if win.id==event.Window{
			found = true
			break
		}
	}
	if !found {
		fmt.Println("IN UNMAPPING COULDNT FIND WIN NOW SEARCHING")
		ok, index, _ := wm.findWindow(event.Window)
		if !ok {
			slog.Info("couldn't unmap since window wasn't in clients")
			fmt.Println(event.Window)
			return
		}else{
			wm.currWorkspace = &wm.workspaces[index]
			fmt.Println("IN WORKSPACE", index)
			wm.UnFrame(event.Window, false)
			wm.currWorkspace = &wm.workspaces[wm.workspaceIndex]
			return
		}
	}
	wm.UnFrame(event.Window, false)
	wm.fitToLayout()
}

func (wm *WindowManager) remDestroyedWin(Window xproto.Window){
	var found bool = false
	for _, win := range wm.currWorkspace.windowList{
		if win.id==Window{
			found = true
			break
		}
	}
	if !found {
		fmt.Println("IN UNMAPPING COULDNT FIND WIN NOW SEARCHING")
		ok, index, _ := wm.findWindow(Window)
		if !ok {
			slog.Info("couldn't unmap since window wasn't in clients")
			fmt.Println(Window)
			return
		}else{
			wm.currWorkspace = &wm.workspaces[index]
			fmt.Println("IN WORKSPACE", index, wm.currWorkspace.windowList)
			wm.UnFrame(Window, false)
			wm.currWorkspace = &wm.workspaces[wm.workspaceIndex]
			return
		}
	}

	wm.UnFrame(Window, false)
	wm.fitToLayout()
}

func (wm *WindowManager) UnFrame(w xproto.Window, unmapped bool) {

	// if it is already unmapped then no need to do it again
	err := xproto.UnmapWindowChecked(
		wm.conn,
		w,
	).Check()

	if err != nil {
		slog.Error("couldn't unmap frame", "error:", err.Error())
	}
	// remove window and frame from current workspace record
	remove(&wm.currWorkspace.windowList, w)
	delete(wm.windows, w)
	wm.setNetClientList()

	// delete window from x11 set
	err = xproto.ChangeSaveSetChecked(
		wm.conn,
		xproto.SetModeDelete,
		w,
	).Check()

	if err != nil {
		slog.Error("couldn't remove window from save", "error:", err.Error())
	}

	// destroy frame
	err = xproto.DestroyWindowChecked(
		wm.conn,
		w,
	).Check()

	if err != nil {
		slog.Error("couldn't destroy frame", "error:", err.Error())
		return
	}

	slog.Info("Unmapped", "window", w)
}

func (wm *WindowManager) setWindowDesktop(win xproto.Window, desktop uint32) {
	atomWmDesktop, _ := xproto.InternAtom(wm.conn, true, uint16(len("_NET_WM_DESKTOP")), "_NET_WM_DESKTOP").Reply()

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
	typeAtom, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE")), "_NET_WM_WINDOW_TYPE").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE atom", "error", err)
		return false
	}

	// Get the _NET_WM_WINDOW_TYPE property for the window
	actualType, err := xproto.GetProperty(conn, false, win, typeAtom.Atom, xproto.AtomAtom, 0, 1).Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE property", "error", err)
		return false
	}

	if len(actualType.Value) == 0 {
		return false
	}

	// Check if the window has the _NET_WM_WINDOW_TYPE_SPLASH, _NET_WM_WINDOW_TYPE_DIALOG, _NET_WM_WINDOW_TYPE_NOTIFICATION, or _NET_WM_WINDOW_TYPE_DOCK
	netWmSplash, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE_SPLASH")), "_NET_WM_WINDOW_TYPE_SPLASH").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_SPLASH atom", "error", err)
		return false
	}
	netWmPanel, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE_PANEL")), "_NET_WM_WINDOW_TYPE_PANEL").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_PANEL atom", "error", err)
		return false
	}

	netWmTooltip, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE_TOOLTIP")), "_NET_WM_WINDOW_TYPE_TOOLTIP").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_PANEL atom", "error", err)
		return false
	}

	netWmDialog, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE_DIALOG")), "_NET_WM_WINDOW_TYPE_DIALOG").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_DIALOG atom", "error", err)
		return false
	}

	netWmNotification, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE_NOTIFICATION")), "_NET_WM_WINDOW_TYPE_NOTIFICATION").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_NOTIFICATION atom", "error", err)
		return false
	}

	netWmDock, err := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_TYPE_DOCK")), "_NET_WM_WINDOW_TYPE_DOCK").Reply()
	if err != nil {
		slog.Error("Error getting _NET_WM_WINDOW_TYPE_DOCK atom", "error", err)
		return false
	}

	// Check if the window type matches any of the "ignore" types
	windowType := xproto.Atom(binary.LittleEndian.Uint32(actualType.Value))

	if windowType == netWmSplash.Atom || windowType == netWmDialog.Atom || windowType == netWmNotification.Atom || windowType == netWmDock.Atom || windowType == netWmPanel.Atom || windowType == netWmTooltip.Atom {
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

func (wm *WindowManager) OnMapRequest(event xproto.MapRequestEvent) {

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
	wm.Frame(event.Window, false)
	if wm.currWorkspace.tiling {
		wm.fitToLayout()
	}

	wm.setWindowDesktop(event.Window, uint32(wm.workspaceIndex))
}

func (wm *WindowManager) Frame(w xproto.Window, createdBeforeWM bool) {

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
	screenMidX := math.Round(float64(wm.width) / 2)
	screenMidY := math.Round(float64(wm.height) / 2)
	topLeftX := screenMidX - windowMidX
	topLeftY := screenMidY - windowMidY

	err = xproto.ConfigureWindowChecked(wm.conn, w, xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{
			uint32(topLeftX),
			uint32(topLeftY),
			uint32(geometry.Width),
			uint32(geometry.Height),
	}).Check()

	if err != nil {
		slog.Error("Couldn't create new window", "error:", err.Error())
		return
	}

	_ = xproto.ConfigureWindowChecked(wm.conn, w, xproto.ConfigWindowBorderWidth, []uint32{uint32(BorderWidth)})

	err = xproto.ChangeWindowAttributesChecked(
		wm.conn,
		w,
		xproto.CwBackPixel|xproto.CwBorderPixel|xproto.CwEventMask,
		[]uint32{
			Col, // background
			Col, // border color
			xproto.EventMaskSubstructureRedirect |
				xproto.EventMaskSubstructureNotify | xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease,
		},
	).Check()
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
		slog.Error("failed to set event mask on window", "error:", err)
	}

	setFrameWindowType(wm.conn, w)



	wins, err := xproto.QueryTree(wm.conn, wm.root).Reply()
	if err == nil {
		for _, win := range wins.Children {
			wm.isAbove(win)
		}
	}

	// add all of this to the current workspace record
	wm.currWorkspace.windowList = append(wm.currWorkspace.windowList, &Window{
		X:          int(topLeftX),
		Y:          int(topLeftY),
		Width:      int(geometry.Width),
		Height:     int(geometry.Height),
		Fullscreen: false,
		id:         w,
		Client:     w,
	})
	wm.windows[w] = wm.currWorkspace.windowList[len(wm.currWorkspace.windowList)-1]
	wm.setNetClientList()
	fmt.Println("Framed window" + strconv.Itoa(int(w)) + "[" + strconv.Itoa(int(w)) + "]")
}

func (wm *WindowManager) OnConfigureRequest(event xproto.ConfigureRequestEvent) {
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
		slog.Error("couldn't configure window", "error:", err.Error())
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

func (wm *WindowManager) Close() {
	// close the connection
	if wm.conn != nil {
		wm.conn.Close()
	}
}

// The end.
