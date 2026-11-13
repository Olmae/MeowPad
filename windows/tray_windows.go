package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

const (
	trayTitle = "Meowpad Volume"
	wmClose   = 0x0010
	wmNull    = 0x0000
	wmContext = 0x007b
	wmTray    = 0x8001

	nimAdd        = 0
	nimDelete     = 2
	nimSetFocus   = 3
	nimSetVersion = 4
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	shellNotifyIcon     = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
	registerClass       = user32.NewProc("RegisterClassExW")
	registerMessage     = user32.NewProc("RegisterWindowMessageW")
	createWindow        = user32.NewProc("CreateWindowExW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	getMessage          = user32.NewProc("GetMessageW")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	loadIcon            = user32.NewProc("LoadIconW")
	messageBox          = user32.NewProc("MessageBoxW")
	postMessage         = user32.NewProc("PostMessageW")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	registerHotKey      = user32.NewProc("RegisterHotKey")
	unregisterHotKey    = user32.NewProc("UnregisterHotKey")
)

type windowClass struct {
	size, style                  uint32
	procedure                    uintptr
	classExtra, windowExtra      int32
	instance, icon, cursor, back uintptr
	menuName, className          *uint16
	smallIcon                    uintptr
}

type windowMessage struct {
	window         uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	x, y           int32
	private        uint32
}

type notifyIconData struct {
	size                       uint32
	window                     uintptr
	id, flags, callbackMessage uint32
	icon                       uintptr
	tip                        [128]uint16
	state, stateMask           uint32
	info                       [256]uint16
	version                    uint32
	infoTitle                  [64]uint16
	infoFlags                  uint32
	guid                       windows.GUID
	balloonIcon                uintptr
}

func utf16Text(value string) *uint16 {
	return windows.StringToUTF16Ptr(value)
}

func showError(err error) {
	messageBox.Call(0, uintptr(unsafe.Pointer(utf16Text(err.Error()))),
		uintptr(unsafe.Pointer(utf16Text(trayTitle))), 0x10)
}

func runTray(enumerator *wca.IMMDeviceEnumerator, overlays chan<- [33]byte) error {
	const (
		wmDestroy    = 2
		ninSelect    = 0x0400
		ninKeySelect = 0x0401
	)
	taskbarCreated, _, err := registerMessage.Call(uintptr(unsafe.Pointer(utf16Text("TaskbarCreated"))))
	if taskbarCreated == 0 {
		return err
	}
	icon, _, err := loadIcon.Call(0, 32512) // IDI_APPLICATION
	if icon == 0 {
		return err
	}
	data := notifyIconData{size: uint32(unsafe.Sizeof(notifyIconData{})), id: 1,
		flags: 0x01 | 0x02 | 0x04 | 0x80, callbackMessage: wmTray, icon: icon, version: 4}
	copy(data.tip[:], windows.StringToUTF16(trayTitle))

	closed := false
	class := windowClass{size: uint32(unsafe.Sizeof(windowClass{})), className: utf16Text("MeowpadVolumeTray")}
	class.procedure = syscall.NewCallback(func(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case wmDestroy:
			closed = true
			return 0
		case uint32(taskbarCreated):
			data.add()
			return 0
		case wmTray:
			switch uint16(lParam) {
			case wmContext, ninSelect, ninKeySelect:
				x, y := int16(wParam), int16(wParam>>16)
				showOutputMenu(enumerator, window, x, y)
				if !closed {
					shellNotifyIcon.Call(nimSetFocus, uintptr(unsafe.Pointer(&data)))
				}
			}
			return 0
		}
		result, _, _ := defWindowProc.Call(window, uintptr(message), wParam, lParam)
		return result
	})

	atom, _, err := registerClass.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return err
	}
	window, _, err := createWindow.Call(0, atom, uintptr(unsafe.Pointer(utf16Text(trayTitle))),
		0, 0, 0, 0, 0, 0, 0, 0, 0)
	if window == 0 {
		return err
	}
	defer func() {
		if !closed {
			destroyWindow.Call(window)
		}
	}()
	data.window = window
	if err := data.add(); err != nil {
		return err
	}
	defer shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))

	for id := uintptr(1); id <= 4; id++ {
		// Ctrl+Alt+F13–F16 can also be assigned in Vial for older firmware.
		ok, _, _ := registerHotKey.Call(window, id, 0x0003, 0x7b+id)
		if ok != 0 {
			defer unregisterHotKey.Call(window, id)
		}
	}
	for !closed {
		var message windowMessage
		result, _, err := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		switch int32(result) {
		case -1:
			return err
		case 0:
			return nil
		}
		if message.message == 0x0312 {
			appVolumeHotkey(enumerator, overlays, message.wParam)
			continue
		}
		dispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
	return nil
}

func appVolumeHotkey(enumerator *wca.IMMDeviceEnumerator, overlays chan<- [33]byte, id uintptr) {
	executable, mode := "spotify.exe", byte('S')
	if id >= 3 {
		executable, mode = foregroundExecutable(), 'A'
	}
	step := float32(0.05)
	if id%2 == 1 {
		step = -step
	}
	level, err := changeAppVolume(enumerator, executable, step)
	if err != nil {
		level = 255
	}
	report := [33]byte{0, 0xAB, 'M', 'P', 2, 'V', mode, byte(level)}
	copy(report[8:], oledText(strings.TrimSuffix(executable, ".exe")))
	select {
	case overlays <- report:
	default:
	}
}

func (data *notifyIconData) add() error {
	ok, _, _ := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(data)))
	if ok == 0 {
		return fmt.Errorf("Не удалось добавить значок Meowpad в область уведомлений")
	}
	ok, _, _ = shellNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(data)))
	if ok == 0 {
		shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(data)))
		return fmt.Errorf("Не удалось включить меню значка Meowpad")
	}
	return nil
}

func showOutputMenu(enumerator *wca.IMMDeviceEnumerator, window uintptr, x, y int16) {
	const (
		menuDisabled  = 0x0001
		menuChecked   = 0x0008
		menuSeparator = 0x0800
	)
	menu, _, err := createPopupMenu.Call()
	if menu == 0 {
		showError(err)
		return
	}
	defer destroyMenu.Call(menu)
	outputs, listErr := audioOutputs(enumerator)
	current, _ := defaultOutput(enumerator, wca.EConsole)
	addMenuItem(menu, menuDisabled, 0, "Вывод звука")
	if listErr != nil {
		addMenuItem(menu, menuDisabled, 0, "Не удалось прочитать список выходов")
	} else if len(outputs) == 0 {
		addMenuItem(menu, menuDisabled, 0, "Нет доступных выходов")
	}
	for i, output := range outputs {
		var flags uintptr
		if output.id == current {
			flags = menuChecked
		}
		addMenuItem(menu, flags, uintptr(i+1), output.name)
	}
	exit := uintptr(len(outputs) + 1)
	appendMenu.Call(menu, menuSeparator, 0, 0)
	addMenuItem(menu, 0, exit, "Выход")

	setForegroundWindow.Call(window)
	const returnCommandAndRightButton = 0x0102
	choice, _, _ := trackPopupMenu.Call(menu, returnCommandAndRightButton, uintptr(x), uintptr(y), 0, window, 0)
	postMessage.Call(window, wmNull, 0, 0)
	switch {
	case choice == exit:
		postMessage.Call(window, wmClose, 0, 0)
	case choice > 0 && choice < exit:
		if err := switchOutput(enumerator, outputs[choice-1].id); err != nil {
			showError(fmt.Errorf("Не удалось переключить выход звука: %w", err))
		}
	}
}

func addMenuItem(menu, flags, id uintptr, label string) {
	label = strings.ReplaceAll(label, "&", "&&")
	appendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16Text(label))))
}
