package wm

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/goccy/go-yaml"
)

// Config represents the application configuration.
// tiling window gaps, unfocused/focused window border colors, mod key for all wm actions, window border width, keybinds
type Config struct {
	lyts                      map[int][]Layout
	Layouts                   []map[int][]Layout `yaml:"layouts"`
	AutoReload                bool               `yaml:"auto-reload"`
	Gap                       uint32             `yaml:"gaps"`
	Resize                    uint32             `yaml:"resize-amount"`
	OuterGap                  uint32             `yaml:"outer-gap"`
	StartTiling               bool               `yaml:"default-tiling"`
	BorderUnactive            uint32             `yaml:"unactive-border-color"`
	BorderActive              uint32             `yaml:"active-border-color"`
	ModKey                    string             `yaml:"mod-key"`
	BorderWidth               uint32             `yaml:"border-width"`
	TempKeybinds              []Keybind          `yaml:"keybinds"`
	Keybindings               []Keybind
	AutoFullscreen            bool            `yaml:"auto-fullscreen"`
	WorkspaceAutoBackAndForth bool            `yaml:"workspace-auto-back-and-forth"`
	Monitors                  []MonitorConfig `yaml:"monitors"`
}

func (wm *WindowManager) configListener() {
	// Create new watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Couldn't create config listener", "error:", err)
	}
	wm.configWatcher = watcher

	home, _ := os.UserHomeDir()
	// Start listening for events.
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					slog.Info("Event error")
					return
				}
				log.Println("event:", event)
				if event.Has(fsnotify.Write) && event.Name == filepath.Join(home, ".config", "doWM", "doWM.yml") {
					log.Println("modified file:", event.Name)
					wm.config = wm.createConfig(true)
					if len(wm.config.Monitors) != 0 {
						wm.positionMonitors()
					}
					wm.reload(start)
					mMask = wm.mod
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					slog.Info("watcher error")
					return
				}
				slog.Error("error:", "error:", err)
			}
		}
	}()

	// Add config path.
	err = watcher.Add(filepath.Join(home, ".config", "doWM"))
	if err != nil {
		slog.Error("Couldn't listen to config file", "error:", err)
	}
}

// read and create config, if certain values, aren't provided, use the default values, if run automatically it will not update config if there are errors.
func (wm *WindowManager) createConfig(auto bool) Config {
	var tmp Config
	if auto {
		tmp = wm.config
	}
	// Set defaults manually
	cfg := Config{
		AutoReload:                false,
		Gap:                       6,
		OuterGap:                  0,
		BorderWidth:               3,
		ModKey:                    "Mod1",
		BorderUnactive:            0x8bd5ca,
		BorderActive:              0xa6da95,
		TempKeybinds:              []Keybind{},
		Keybindings:               []Keybind{},
		lyts:                      createLayouts(),
		Layouts:                   []map[int][]Layout{},
		StartTiling:               false,
		AutoFullscreen:            false,
		WorkspaceAutoBackAndForth: false,
		Monitors:                  []MonitorConfig{},
	}

	home, _ := os.UserHomeDir()
	f, err := os.ReadFile(filepath.Join(home, ".config", "doWM", "doWM.yml"))
	if err != nil {
		slog.Error("Couldn't read doWM.yml config file", "error:", err)
		if wm.conferror {
			errwinclose(wm.conn)
		}
		errstr := "doWM.yml file doesnt exist in config folder, after fixing, if your keybinds had an error, use mod+shift+r to reload config, otherwise use your reload-config keybind"
		if auto {
			errstr = "doWM.yml file doesnt exist in config folder"
		}
		wm.errwin(errstr)
		wm.conferror = true
		if auto {
			return tmp
		} else {
			return cfg
		}
	}

	if err := yaml.Unmarshal(f, &cfg); err != nil {
		slog.Error("Couldn't parse doWM.yml config file", "error:", err)
		if wm.conferror {
			errwinclose(wm.conn)
		}
		errstr := fmt.Sprint("Error in config file: ", parseConfigError(err.Error()), "\n", "after fixing, if your keybinds had an error, use mod+shift+r to reload config, otherwise use your reload-config keybind")
		if auto {
			errstr = fmt.Sprint("Error in config file: ", parseConfigError(err.Error()), "\n", "using auto reload, so once there are no errors, the config will automatically update")
		}
		wm.errwin(errstr)
		wm.conferror = true
		if auto {
			return tmp
		} else {
			return cfg
		}
	}

	if wm.conferror {
		errwinclose(wm.conn)
	}
	wm.conferror = false

	if len(cfg.Layouts) > 0 {
		lyts := map[int][]Layout{}
		for _, lyt := range cfg.Layouts {
			for key, val := range lyt {
				lyts[key] = val
				break
			}
		}

		cfg.lyts = lyts
	}

	return cfg
}
