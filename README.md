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
- [Screenshots](#screenshots)
- [Installation](#installation)
- [Configuration](#configuration)
- [Star History](#star-history)
- [Progress](#progress)
- [Bugs](#bugs)

## Description
doWM is a beautiful floating and tiling window manager for X11 completely written in golang.

## screenshots
<div align="center">
<img src="https://github.com/BobdaProgrammer/doWM/blob/main/images/gruvbox.png?raw=true" width="500px">
<img src="https://github.com/BobdaProgrammer/doWM/blob/main/images/pinkgradient.png?raw=true" width="500px">
  
<img src="https://github.com/BobdaProgrammer/doWM/blob/main/images/8windowsweirdlayout.png?raw=true" width="500px">
<img src="https://github.com/BobdaProgrammer/doWM/blob/main/images/fabricnotification.png?raw=true" width="500px">

<img src="https://github.com/BobdaProgrammer/doWM/blob/main/images/musicwithnotification.png?raw=true" width="500px">
<img src="https://github.com/BobdaProgrammer/doWM/blob/main/images/workspaceviewergruvbox.png?raw=true" width="500px">
</div>  

-------------


## Installation
Currently the best way is to build from source:

You will want to have go installed

```bash
git clone https://github.com/BobdaProgrammer/doWM
cd doWM
go build -o ./doWM
make install
```

then to see a normal config look at `exampleConfig` folder, you can copy this to ~/.config/doWM and then write your own configuration  

-------------

> [!WARNING]
> make sure to make the autostart.sh executable and to use a config, otherwise you could be left in the black with no way to escape!

```
mkdir ~/.config/doWM
cp -r ./exampleConfig/* ~/.config/doWM/ 
chmod +x ~/.config/doWM/autostart.sh        
```

> [!NOTE]
> To logout, I suggest you use `kill $(pgrep -o doWM)`

## Configuration
doWM is configured with `~/.config/doWM/doWM.yml` and `~/.confiig/doWM/autostart.sh`
simply put any autostart commands in autostart.sh, and then remember to chmod +x it.
the main config file is very simple and is described clearly in the comments on /exampleConfig/doWM.yml

Colors are to be written in hex format starting with 0x for example white: 0xffffff (could be 0xFFFFFF it is case insensitive)

You have a few general options:
- outer-gap (gap from edge of tiling space to windows)
- gaps (pixel gaps in tiling)
- default-tiling (if true, tiling will be enabled on start)
- auto-fullscreen (if true, windows will auto fullscreen when they request)
- mod-key (which key should be used for all wm commands)
- border-width (border width of windows)
- unactive-border-color (the color for the border of unactive windows
- active-border-color (the color for the border of an active window)

Although there are some default tiling layouts which will serve you well, you can easily customize your tiling layouts. The system works quite simply, in the `layouts:` you would have a list of each of the window numbers you want to have a layout/s for, for example 1 through 5 so you would have layouts for up to 5 windows in a workspace, any more than that and the window would just be placed above the windows to be moved to a seperate workspace or closed. For each window number, you specify `- windows:` for each layout, in side of windows you would have a list of windows, represented like this:
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
- detach-tiling (sepearate a workspace from global tiling - e.g that workspace could be floating with rest tiling - it is also toggling, so if detached it will re-attach)
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

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=BobdaProgrammer/doWM&type=Timeline)](https://www.star-history.com/#BobdaProgrammer/doWM&Timeline)

## progress
- [x] move/resize
- [x] workspaces
- [x] move window between workspaces
- [x] focus on hover
- [x] configuration
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
- [ ] multi monitor support

## bugs
 - Currently none known
