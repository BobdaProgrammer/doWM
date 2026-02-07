<div align="center">
      <h1>doWM</h1>
     </div>
<p align="center"> <a href="https://github.com/BobdaProgrammer/doWM" target="_blank"><img alt="" src="https://img.shields.io/badge/Github-302D41?style=for-the-badge&logo=github" style="vertical-align:center" /></a>
</p>
<p align="center">
    <a href="https://github.com/BobdaProgrammer/doWM/pulse" target="_blank"><img src="https://img.shields.io/github/last-commit/BobdaProgrammer/doWM?style=for-the-badge&logo=github&color=7dc4e4&logoColor=D9E0EE&labelColor=302D41"></a>
    <a href="https://github.com/BobdaProgrammer/doWM/stargazers" target="_blank"><img src="https://img.shields.io/github/stars/BobdaProgrammer/doWM?style=for-the-badge&logo=apachespark&color=eed49f&logoColor=D9E0EE&labelColor=302D41"></a>
</p><p align="center">
      <a href="https://visitorbadge.io/status?path=https%3A%2F%2Fgithub.com%2FBobdaProgrammer%2FdoWM"><img src="https://api.visitorbadge.io/api/visitors?path=https%3A%2F%2Fgithub.com%2FBobdaProgrammer%2FdoWM&label=visitors&labelColor=%23ff8a65&countColor=%23111133" /></a>
      <a href="https://github.com/BobdaProgrammer/doWM/issues" target="_blank">
      <img alt="Issues" src="https://img.shields.io/github/issues/BobdaProgrammer/doWM?style=for-the-badge&logo=bilibili&color=F5E0DC&logoColor=D9E0EE&labelColor=302D41" />
    </a>
       <a href="https://github.com/BobdaProgrammer/doWM/blob/main/LICENSE" target="_blank">
      <img alt="License" src="https://img.shields.io/github/license/BobdaProgrammer/doWM?style=for-the-badge&logo=starship&color=ee999f&logoColor=D9E0EE&labelColor=302D41" />
    </a>
</p>

## Contents
- [Description](#description)
- [Discussions](#discussions)
- [Screenshots](#screenshots)
- [Installation](#installation)
- [Configuration](#configuration)
- [Monitors](#monitors)
- [Star History](#star-history)
- [Progress](#progress)

## Description
doWM is a beautiful floating and tiling window manager for X11 designed for efficiency, beauty and performance, completely written in golang.

## Discussions
I highly recommend that if you have any questions that aren't issue related, you post something on the [discussions](https://github.com/BobdaProgrammer/doWM/discussions) page, I check it regularly and hopefully I can respond and help you with any questions you have.

## screenshots
<div align="center">
<img src="https://github.com/BobdaProgrammer/doWM-web/blob/main/images/gruvbox.png?raw=true" width="500px">
<img src="https://github.com/BobdaProgrammer/doWM-web/blob/main/images/pinkgradient.png?raw=true" width="500px">

<img src="https://github.com/BobdaProgrammer/doWM-web/blob/main/images/8windowsweirdlayout.png?raw=true" width="500px">
<img src="https://github.com/BobdaProgrammer/doWM-web/blob/main/images/fabricnotification.png?raw=true" width="500px">

<img src="https://github.com/BobdaProgrammer/doWM-web/blob/main/images/musicwithnotification.png?raw=true" width="500px">
<img src="https://github.com/BobdaProgrammer/doWM-web/blob/main/images/workspaceviewergruvbox.png?raw=true" width="500px">
</div>

-------------


## Installation
Currently the best way is to build from source:

You will want to have go installed

```bash
git clone https://github.com/BobdaProgrammer/doWM
cd doWM
go build -o ./doWM
make install config
```
`make install config` will install doWM and move an example config into the config folder, you can just run make install and write your own from scratch but the example config is a good place to start

-------------

This is in case you just run `make install`, it is what you can use to copy the default config into the folder
```
mkdir ~/.config/doWM
cp -r ./exampleConfig/* ~/.config/doWM/
chmod +x ~/.config/doWM/autostart.sh
```

> [!WARNING]
> Make sure to look through the config, to alter startup programs and keybinds so that you can use doWM out of the box

> [!NOTE]
> To logout, I suggest you use `kill $(pgrep -o doWM)`

## Configuration
doWM is configured with `~/.config/doWM/doWM.yml` and `~/.config/doWM/autostart.sh`
simply put any autostart commands in autostart.sh, and then remember to chmod +x it.
the main config file is very simple and is described clearly in the comments on /exampleConfig/doWM.yml

Colors are to be written in hex format starting with 0x for example white: 0xffffff (could be 0xFFFFFF it is case insensitive)

You have a few general options:
- outer-gap (gap from edge of tiling space to windows)
- gaps (pixel gaps in tiling)
- resize-amount (the amount of pixels by which a window will increase/decrease with each resize)
- auto-reload (whether the WM should auto reload the config when changes are made, the config will not auto update if errors are present)
- default-tiling (if true, tiling will be enabled on start)
- auto-fullscreen (if true, windows will auto fullscreen when they request)
- mod-key (which key should be used for all wm commands)
- border-width (border width of windows)
- unactive-border-color (the color for the border of unactive windows)
- active-border-color (the color for the border of an active window)
- workspace-auto-back-and-forth (whether switching to the current workspace number switches to the previous workspace, default is false)

The multi monitor system is fairly simple, you don't need to add it to your config but you can if you want to specify positions for your monitors. The only rule for the positions is they cannot be negative, this means that the monitor that is the highest has a Y of 0, where as ones lower than that could be something like 1080, same with X, the one on the furthest left would be 0, then to the right of that could be 1920. Here is an example of two monitors, one it above and to the right and the other below and to the left:
```yml
monitors:
  # highest and to the right
  - x: 960
    y: 0

  # furthest to the left and below
  - x: 0
    y: 1080
```
To move a window between monitors, just drag it between and it will follow

Although there are some default tiling layouts which will serve you well, you can easily customize your tiling layouts. The system works quite simply, in the `layouts:` you would have a list of each of the window numbers you want to have a layout/s for, for example 1 through 5 so you would have layouts for up to 5 windows in a workspace, any more than that and the window would just be placed above the windows to be moved to a separate workspace or closed. For each window number, you specify `- windows:` for each layout, in side of windows you would have a list of windows, represented like this:
```yml
- x: 0.0 # the X percentage in the tiling space, 0.5 would have the top left corner halfway through the width of the tiling space
  y: 0.0 # the Y percentage in the tiling space
  width: 1.0 # The width percentage in the tiling space, 1.0 is the whole width
  height: 1.0 # The height percentage in the tiling space
```
In the example above, it would have one window that takes up the whole space since its top left corner is at (0, 0) and its width and height are the full tiling space.
Below is an example of a simple layout config for 1 and 2 windows:
```yml
layouts:
  - 1:
    - windows: # 1 window - takes up whole space
      - x: 0.0
        y: 0.0
        width: 1.0
        height: 1.0


  - 2:
    - windows: # 2 windows - 1st layout is split halfway - 2nd layout is for one being 2/3 of the width, the other 1/3
      - x: 0.0
        y: 0.0
        width: 0.5
        height: 1.0
      - x: 0.5
        y: 0.0
        width: 0.5
        height: 1.0
    - windows:
      - x: 0.0
        y: 0.0
        width: 0.66666666
        height: 1.0
      - x: 0.6666666666666
        y: 0.0
        width: 0.33333333333333
        height: 1
```
There is much longer one that goes up to 10 windows in the example config that you can check out


there are also some default keybinds like modkey+(0-9) to switch workspaces and with a shift to move a window between workspaces, but you can also set your own keybinds

each keybind either executes a command or plays a role in the wm. Here are all the roles:
- quit (close window)
- force-quit (force close window)
- toggle-tiling (toggle tiling mode)
- toggle-fullscreen (toggle fullscreen on window)
- swap-window-left (shift window left in tiling mode)
- swap-window-right (shift window right in tiling mode)
- focus-window-left (focus the window to the left in tiling mode)
- focus-window-right (focus the window to the right in tiling mode)
- reload-config (reload doWM.yml)
- increase-gap (increase gap between windows in tiling temporarily - reset next session)
- decrease-gap (decrease gap between windows in tiling, also temporary)
- detach-tiling (separate a workspace from global tiling - e.g that workspace could be floating with rest tiling - it is also toggling, so if detached it will re-attach)
- next-layout (switch to the next layout for the current window number)
- resize-x-scale-up (increases the width of the current window)
- resize-x-scale-down (decreases the width of the current window)
- resize-y-scale-up (increases the height of the current window)
- resize-y-scale-down (decreases height of current window)
- move-x-left (moves window to the left)
- move-x-right (moves window to the right)
- move-y-up (moves window up)
- move-y-down (moves window down)

each keybind also has a key and a shift option, key is the character of the key (can also be things like "f1" "space" or "return") and shift is a bool for if shift should be pressed or not to register.

for example:
```yml
  # When mod + t is pressed then open kitty
  - key: "t"
    shift: false
    exec: "kitty"
  # When mod + shift + right arrow is pressed then switch the focused window to the right
  - key: "right"
    shift: true
    role: "swap-window-right"
```

For an example config, look at [/exampleConfig](https://github.com/BobdaProgrammer/doWM/tree/main/exampleConfig)

## Monitors
doWM supports multiple monitors and you can see how to configure them in the configuration section. Each monitor has 10 workspaces and are independent of the other monitors unless you drag a window between them, it will then move it to the other monitor.

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=BobdaProgrammer/doWM&type=Timeline)](https://www.star-history.com/#BobdaProgrammer/doWM&Timeline)

## progress
- [x] move/resize
- [x] workspaces
- [x] move window between workspaces
- [x] focus on hover
- [x] configuration
- [x] auto reloading config
- [x] keybinds
- [x] floating
- [x] tiling
- [x] swap windows in tiling
- [x] change focus in tiling
- [x] many layouts
- [x] bar support
- [x] fullscreen
- [x] startup commands
- [x] picom support
- [x] multi monitor support
- [ ] auto update monitors if new one is plugged in
